package gateway

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"errors"
	"fmt"
	"mime"
	"net"
	"net/http"
	"net/netip"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/madkoding/motita/internal/approvals"
	"github.com/madkoding/motita/internal/config"
	"github.com/madkoding/motita/internal/evals"
	"github.com/madkoding/motita/internal/friction"
	"github.com/madkoding/motita/internal/gitforge"
	"github.com/madkoding/motita/internal/llm"
	"github.com/madkoding/motita/internal/logx"
	"github.com/madkoding/motita/internal/netrules"
	"github.com/madkoding/motita/internal/oauth"
	"github.com/madkoding/motita/internal/peers"
	"github.com/madkoding/motita/internal/schedule"
	"github.com/madkoding/motita/internal/updater"
	"github.com/madkoding/motita/internal/voice"
	"github.com/madkoding/motita/internal/webpush"
	"github.com/madkoding/motita/internal/webui"
)

// Literals used more than once in this file.
const (
	headerContentType = "Content-Type"
)

// defaultMaxBodyKB caps a request body when nothing else is configured. A task or a prompt is a
// sentence; 256 KB is generous for one and small enough that a client cannot make the agent
// chew on a novel.
const defaultMaxBodyKB = 256

// defaultHeartbeat is how often a long-lived stream writes a comment line to keep the connection
// alive across a middlebox.
//
// A mobile carrier NAT commonly drops a connection that has been idle for 30 to 60 seconds, and a
// turn can easily run longer than that with nothing to say. The failure it prevents is silent and
// looks like the RUN died rather than the connection: the client sees the stream close, reattaches,
// and the user loses the tail of work that was still going.
//
// 15 seconds leaves generous room under the shortest timeout in common use while costing one
// 16-byte line - nothing against an event stream that carries a run's output.
const defaultHeartbeat = 15 * time.Second

// Options configure a Server.
type Options struct {
	// Service is the agent this gateway speaks for.
	Service Service
	// Listen is the address to bind, "host:port". Port 0 asks the kernel for a free one, which
	// is what an embedded gateway wants: no conflict with anything, and the address that was
	// actually granted is read back with Addr().
	Listen string
	// Token is the bearer token. Empty refuses to start: see Start.
	Token string
	// Allow is the ordered origin rule list, already parsed. Nil means every origin may connect,
	// which is the documented default: the gateway comes up the way a machine with a fresh, empty
	// firewall table accepts everything, and the operator narrows it by adding rules.
	//
	// It is the PARSED policy rather than the strings, because the parsing has to have happened
	// before this point: a malformed rule must be refused while the configuration is read, not
	// discovered request by request, where its only symptom is an origin being refused for a
	// reason nobody can see.
	Allow *netrules.Policy
	// Hosts are extra host names a request's Host header may carry (gateway.hosts). See
	// hostPolicy for the names that are always accepted.
	Hosts []string
	// Origins are page origins (scheme://host[:port]) trusted besides the gateway's own
	// (gateway.origins): for a front end that rewrites Host and cannot send X-Forwarded-Host.
	Origins []string
	// MaxBodyKB caps a request body. Zero means defaultMaxBodyKB.
	MaxBodyKB int
	// Version is reported by /v1/health, so a client can tell which build answered.
	Version string
	// ExePath is the running binary's path, used by the updater to replace it. It is also the
	// credential helper git is given when the gateway clones a repository.
	ExePath string
	// GitAuthDir is where the git logins live (config.AuthDir()). Empty means this gateway has no
	// place to keep them, and the /v1/git endpoints say so.
	GitAuthDir string
	// GitHTTP is the transport of the git hosts' APIs and OAuth servers. Nil means the default
	// client; a test injects a fake.
	GitHTTP oauth.HTTPClient
	// PRPollEvery is how often the gateway asks a host for the CI of a pull request it is
	// following; zero means 20 seconds. PRMaxFixes is how many times it sends the agent to fix a
	// failing CI before giving up and telling the user; zero means 5.
	PRPollEvery time.Duration
	PRMaxFixes  int
	// Restart is how this process starts running the binary the updater just installed. It is
	// called once the new binary is in place and the sessions are saved, and it is expected to
	// stop this process and bring up its replacement.
	//
	// Nil means this process CANNOT replace itself - the gateway that lives inside a terminal's
	// interface is the example, because its lifetime belongs to that terminal - and the upgrade
	// then installs the binary and says so, instead of cancelling a gateway nobody would start
	// again. Cancelling without a replacement is how an upgrade turns into an outage.
	Restart func()
	// Shutdown asks this process to stop the same way SIGTERM does on unix: the context the
	// service runs under is cancelled and the ordinary graceful close follows. It is what
	// `motita gateway stop` uses where there is no SIGTERM to send (Windows), through
	// POST /v1/shutdown. Nil means this gateway's lifetime belongs to something else - the one
	// inside a terminal's interface - and the endpoint then refuses instead of stopping it.
	Shutdown func()
	// NewService builds one more conversation when a client asks for one. Nil means this
	// gateway serves exactly one conversation, which is a real deployment: the embedded case
	// where the terminal that started this process is the only front end there will ever be.
	//
	// It is a factory rather than a list because a conversation holds a transcript and a
	// session, and building all of them up front would build transcripts nobody asked for. It
	// must NOT call back into the gateway: it is called with the registry lock held.
	NewService func() (Service, error)
	// MaxSessions caps how many conversations this process will hold. Zero means
	// defaultMaxSessions.
	MaxSessions int
	// FreeSessionGit makes a free-standing session's own directory a git repository when it is
	// created, unless the request says otherwise. See config.Gateway.FreeSessionGit.
	FreeSessionGit bool
	// Log receives the one line a gateway has to say when it starts: where it is listening.
	// That line is the only way an operator learns the port when 0 was asked for.
	Log *logx.Logger
	// Heartbeat is how often a stream writes a keepalive comment. Zero means defaultHeartbeat.
	// Negative turns it off, which is what a test needs in order to assert that the stream is
	// quiet: the tick only fires on a real timer, so a test that waits for one is a test that
	// sleeps for the heartbeat interval.
	Heartbeat time.Duration
	// WSHeartbeat is how often the WebSocket endpoint sends a heartbeat probe. Zero means
	// wsHeartbeatInterval (30s). Negative turns it off, which is what a test needs so the
	// heartbeat goroutine does not inject messages the test is not expecting.
	WSHeartbeat time.Duration
	// UpdateCheckInterval is how often the background checker asks the release API whether a
	// newer version exists. Zero means updateCheckInterval (one hour). Negative turns the checker
	// off entirely, which is what a test needs in order to assert what the gateway does WITHOUT
	// reaching the network: the tick only fires on a real timer, so a test that waits for the
	// default is a test that waits an hour. A value shorter than the startup delay also shortens
	// that delay, so a test is never caught between the two.
	UpdateCheckInterval time.Duration
	// RemoteWatchInterval is how often the gateway asks origin whether the projects' main branches
	// moved. Zero means remoteWatchInterval (15 minutes); negative turns the look off.
	RemoteWatchInterval time.Duration
	// UpdateAPIURL is the release API the updater asks for the latest release. Empty, the
	// production value, means GitHub's. It is a seam for an embedder's tests: a gateway started
	// for real answers /v1/update/check (which the text interface calls on start) by asking the
	// release API, so a suite that starts real gateways points it somewhere that is not the network.
	UpdateAPIURL string
	// WebUI serves the browser interface from this same mux. The page and the API therefore
	// share an origin, which is why no proxy and no CORS header are involved anywhere: the
	// browser asks this server for everything.
	//
	// The page is served WITHOUT a token - it holds no secret and it is the only way a browser
	// can obtain one - and the API it calls is authorised exactly as before.
	WebUI bool
	// SessionDir is the directory where conversations are persisted. Empty
	// means in-memory only (the behaviour the tests have always had). When
	// set, the gateway loads every session found there at startup and saves
	// each conversation's transcript, title and config after every turn.
	SessionDir string
	// ArtifactDir is the directory the files an agent produced are kept in, one folder per
	// session. Empty means artifacts are not kept.
	ArtifactDir string
	// NotesDir is the folder of the note vault: plain Markdown files, opened as they are by
	// Obsidian. Empty means there are no notes.
	NotesDir string
	// ArtifactDays is how many days an artifact is kept. Zero keeps them for ever.
	ArtifactDays int
	// EvalsFile is the file the results of the model checks are kept in. Empty keeps them in memory
	// only, until the process ends.
	EvalsFile string
	// ProjectDir is the directory where projects are persisted. Empty means
	// projects are not available.
	ProjectDir string
	// ScheduleDir is the directory where scheduled tasks are persisted. Empty means
	// scheduling is off for this process, and the endpoints answer an empty list
	// rather than an error: a deployment that does not schedule anything is a
	// deployment, not a failure.
	ScheduleDir string
	// ScheduleMinEvery is the shortest cadence a task may be created with. Zero
	// means the built-in default (one minute). It comes from the configuration so
	// the rule lives in ONE place and the handler does not invent its own.
	ScheduleMinEvery time.Duration
	// ScheduleTick is the resolution at which a due task is noticed. Zero means the
	// built-in default. It is injectable because the end-to-end test needs a firing
	// to happen in seconds, not in half a minute.
	ScheduleTick time.Duration
	// WorkspaceDir is the root workspace under which project folders are
	// created. Required when ProjectDir is set.
	WorkspaceDir string
	// Skills is the procedure library this gateway serves. Nil makes the skill
	// endpoints answer 501 rather than 404: "this gateway was started without a
	// library" is a different fact from "there is no such skill", and a client acts
	// differently on each - the same distinction CreateProject already draws for
	// projects.
	Skills SkillService
	// Curator is the maintenance pass these endpoints can trigger. Nil makes them
	// answer the same 501.
	Curator CuratorService
	// ConfigPath is the configuration file this gateway was started with: what PUT /v1/ui
	// writes the interface language into. Empty means no file is known (the defaults are in
	// force), and that endpoint then answers 409 instead of writing somewhere nobody reads.
	ConfigPath string
	// UILanguage is ui.language as the configuration had it at start: auto, en or es. Empty
	// means auto.
	UILanguage string
	// Voice transcribes the browser's recordings. Nil means speech input is off, and the
	// transcription endpoint answers 501. See voice.go.
	Voice VoiceService
	// WakePhrases are the phrases the interface's hands-free mode listens for.
	WakePhrases []string
	// MaxAudioBytes caps one recording. Zero means defaultMaxAudioBytes.
	MaxAudioBytes int64
	// VoiceFrom builds the transcriber of a configuration, so a change saved from the settings
	// applies without a restart. Nil means a saved change waits for the next start.
	VoiceFrom func(config.Config) VoiceService
	// Speech is the gateway's voice, for reading answers aloud. Nil means it is off, and the
	// speech endpoint answers 501. See speak.go.
	Speech SpeechService
	// SpeechVoices are the voices offered, the default first; SpeechMaxChars caps one text.
	SpeechVoices   []string
	SpeechMaxChars int
	// VoiceModels are the local voices the page can run itself: the catalog and where the
	// downloaded files are kept. Nil means there are none, and their endpoints answer 501. See
	// voicemodels.go.
	VoiceModels *voice.Library
	// TLSListen is a SECOND listener that speaks https, with the PEM pair in TLSCert and
	// TLSKey. Empty means there is none. It is a second socket, not a mode of the first, so the
	// local clients keep speaking plain HTTP to loopback and never have to trust a certificate;
	// a browser on another device uses this one, and only a page served over https (or from
	// localhost) may use the microphone.
	TLSListen string
	TLSCert   string
	TLSKey    string
	// AllowRules is gateway.allow as written, so the network settings can name the exposure in
	// force (see network.go). The policy enforced is Allow; this is only its description.
	AllowRules []string
	// Tailscale finds Tailscale on this machine and publishes the gateway inside the tailnet.
	// Nil means it is never looked for, and the network settings report it as not installed.
	Tailscale TailscaleService
	// PushDir is where the VAPID key and the browsers subscribed to phone notifications are kept
	// (see push.go). Empty means there are none.
	PushDir   string
	PrefsPath string // where the web page's preferences are kept (prefs.go); empty: in memory
	// FrictionFile is the friction journal (see friction.go): what got in the way, kept on this
	// computer. Empty keeps it in memory, until the gateway stops.
	FrictionFile string
	// PeerFile is where this gateway's identity and the other computers it is connected to are
	// kept (see peers.go). Empty keeps them in memory: the identity changes with every start.
	PeerFile string
	// Discovery announces this gateway on the local network and looks for the others there and
	// on the tailnet. Off, the other computers can still be added by address.
	Discovery bool
}

// Server is the HTTP face of the conversations this process holds.
type Server struct {
	opts     Options
	listener net.Listener
	// tlsListener is the https listener, nil when Options.TLSListen is empty.
	tlsListener net.Listener
	server      *http.Server
	mux         http.Handler

	// mainRefresh tracks the background updates of projects' main branches: which are running
	// (so a second request for the same project is dropped) and why the last one of each failed.
	mainRefresh mainRefreshState
	// remoteWatch is what the periodic look at origin found; remoteWG tracks that goroutine. See remotewatch.go.
	remoteWatch remoteWatchState
	remoteWG    sync.WaitGroup

	// scanJob is the progress of the notes scan running in the background, if any.
	scanJob scanTracker

	// intentGate rests auto mode's model call after it fails: one slow turn, not all. See runs.go.
	intentGate modelGate

	// evalJob is the model check running in the background, if any; evalStore holds its results.
	evalJob   evalTracker
	evalStore *evals.Store

	// closeOnce makes Close idempotent. Close is called from a defer in the app AND from the
	// shutdown path, and a second Shutdown on an already-closed listener returns an error the
	// caller would have to know to ignore.
	closeOnce sync.Once
	closeErr  error

	// uiLanguage is ui.language as PUT /v1/ui last set it; empty until then, when the value the
	// process started with (opts.UILanguage) is the answer. See ui.go.
	uiMu       sync.Mutex
	uiLanguage string
	prefs      *prefsStore // the web page's preferences, see prefs.go

	// voiceLive is speech input as PUT /v1/voice last applied it; nil until then, when the
	// options the process started with are the answer. See voice.go.
	voiceMu   sync.RWMutex
	voiceLive *voiceSettings

	// llmLogins are the provider sign-ins started from the browser (see llmlogin.go), and
	// llmListed the models each provider listed when its connection was last tested (llm.go).
	llmLoginMu  sync.Mutex
	llmLogins   map[string]*llmLogin
	llmListedMu sync.Mutex
	llmListed   map[string][]string
	// usageTotals replaces the process-wide token ledger in tests; nil reads the ledger.
	usageTotals func() []llm.ProviderUsage
	// quotaCache keeps the last read of the plan quotas providers state over an API.
	quotaCache quotaCache
	// claudeProbe is the question that keeps Claude's quotas fresh.
	claudeProbe claudeProbe

	// baseCtx is cancelled when this server closes, and every run derives its context from it.
	//
	// A run is NOT bounded by its connection - that is the whole point of it being an object - so
	// it needs some other bound or a gateway that shut down would leave turns running with
	// nowhere to report. The process's own lifetime is that bound, and it is deliberately not the
	// request's.
	baseCtx context.Context
	// indexing holds the folders whose code index is being built in the background.
	indexing   sync.Map
	baseCancel context.CancelFunc

	// voiceDownload is the local voice being downloaded, one at a time. See voicemodels.go.
	voiceDownload modelDownload

	// sessionsMu guards the registry. The conversations themselves are safe to use without it:
	// each is guarded by its own locks, and this is only read to find one.
	// heartbeat is the resolved keepalive interval, zero when it is off: see Options.Heartbeat.
	heartbeat time.Duration

	sessionsMu sync.Mutex
	// artifactsMu orders a pin against the prune: see removeIfUnpinned.
	artifactsMu sync.Mutex
	sessions    map[string]*conversation

	// store persists conversations to disk so they survive a restart. nil
	// when no directory was configured, which means sessions are in-memory
	// only — the behaviour the tests have always had.
	store *sessionStore
	// approvals is what people said to always allow, by project: see alwaysallow.go.
	approvals *approvals.Store
	// projects persists project definitions to disk. nil when no project
	// directory was configured.
	projects *projectStore
	// repoLocks holds one mutex per project id: see lockProjectRepos.
	repoLocks sync.Map
	// catalogLocks holds one mutex per repository URL (see lockCatalogRepo): what adds a repository
	// to the catalog, what deletes its clone and what takes it out of a project do not run at once
	// on the same one, while a clone of one repository no longer holds up the others.
	catalogLocks sync.Map
	// cloningMu guards cloning, the folders of the catalog a clone is being made in, so two clones
	// do not pick the same folder and a deletion does not take one half made.
	cloningMu sync.Mutex
	cloning   map[string]bool
	// schedules persists the tasks that fire on their own. nil when no directory was
	// configured, which means scheduling is off.
	schedules *schedule.Store
	// scheduleWG tracks the "run now" runs whose end is still to be written onto their task's
	// record. See recordManualRun.
	scheduleWG sync.WaitGroup
	// updater is the self-update checker, nil when no ExePath was provided.
	updater *updater.Updater
	// lastCheck is the cached result of the most recent update check.
	// Populated by the periodic checker goroutine and read by
	// handleUpdateCheck when the frontend polls. lastCheckAt is when it was taken, so a result
	// older than updateCacheTTL is checked again instead of served.
	lastCheck   updater.CheckResult
	lastCheckAt time.Time
	lastCheckMu sync.RWMutex

	// gitFlows are the logins to a git host that are waiting for the user's browser. See git.go.
	gitMu    sync.Mutex
	gitFlows map[string]*gitFlow
	// prWatching holds the sessions whose pull request is being followed. See prwatch.go.
	prMu       sync.Mutex
	prWatching map[string]bool
	baseCI     map[string]baseCIEntry
	prLists    prListCache
	// etags makes the reads of the git hosts conditional; see gitforge.ETagCache.
	etags gitforge.ETagCache
	// hosts are the host names, lower-cased, a request's Host header may carry besides an IP
	// address and localhost. See hostPolicy.
	hosts   map[string]bool
	hostsMu sync.RWMutex

	// push sends the phone notifications, nil when there is no PushDir; pushWG tracks the sends in
	// flight. See push.go.
	push   *webpush.Sender
	pushWG sync.WaitGroup

	// autopilotBusy holds the autopilot routines that run right now, so one never runs twice at once.
	autopilotMu   sync.Mutex
	autopilotBusy map[string]bool

	// learnWG tracks the reviews of closed sessions in flight. See closelearn.go.
	learnWG sync.WaitGroup

	// friction is the journal of what got in the way. Never nil once the server is started. See
	// friction.go.
	friction *friction.Journal

	// peers are the other gateways found and connected to, and how they are spoken to. See
	// peers.go.
	peers     *peers.Registry
	prober    *peers.Prober
	peerProxy *http.Transport
	// runningAt is when the connected computers' runs were last read, runningBusy while they are.
	runningAt   atomic.Int64
	runningBusy atomic.Bool
}

// Start binds the listener and returns a Server that is ready to Serve.
//
// It binds rather than calling ListenAndServe because a client has to be TOLD the address it
// must speak to, and with port 0 that address only exists after the bind has happened.
func Start(opts Options) (*Server, error) {
	if opts.Service == nil {
		return nil, errors.New("the gateway needs a service to speak for")
	}
	if strings.TrimSpace(opts.Token) == "" {
		// Refusing here rather than serving everyone is the whole point: an unauthenticated
		// gateway is a remote shell with a JSON envelope.
		return nil, errors.New("the gateway needs a token: an unauthenticated agent is a remote shell")
	}

	addr := listenAddr(opts.Listen)
	if _, _, err := net.SplitHostPort(addr); err != nil {
		return nil, fmt.Errorf("the listen address %q is not host:port: %w", addr, err)
	}
	// Any address may be bound, including a non-loopback one, and there is no longer a second act
	// required for it. The socket is not what decides who may connect: gateway.allow is, and it is
	// applied to every request below. Refusing a wildcard bind here would re-introduce exactly the
	// confusion this design removes - an operator who wrote the address they wanted being told to
	// turn on a setting that no longer exists.

	ln, err := net.Listen("tcp", addr)
	if err != nil {
		// Reported, never retried on another port: a client that was told an address must not
		// find a server somewhere else.
		return nil, fmt.Errorf("could not listen on %s: %w", addr, err)
	}

	tlsLn, err := listenTLS(opts)
	if err != nil {
		_ = ln.Close()
		return nil, err
	}

	if opts.MaxBodyKB <= 0 {
		opts.MaxBodyKB = defaultMaxBodyKB
	}
	first := newConversation(DefaultSession, opts.Service)
	baseCtx, baseCancel := context.WithCancel(context.Background())
	s := &Server{
		opts: opts, listener: ln, tlsListener: tlsLn, sessions: map[string]*conversation{DefaultSession: first},
		baseCtx: baseCtx, baseCancel: baseCancel, evalStore: evals.OpenStore(opts.EvalsFile),
		heartbeat: resolveHeartbeat(opts.Heartbeat),
	}
	// The self-update checker is built here, once. ExePath may be empty when
	// running from a test or a non-standard install, which is fine: the
	// updater endpoints simply report "no update available" rather than
	// crashing.
	if strings.TrimSpace(opts.ExePath) != "" {
		s.updater = updater.New(opts.Version, opts.ExePath)
		if apiURL := opts.UpdateAPIURL; apiURL != "" {
			s.updater.APIURL = func() string { return apiURL }
		}
	}

	// Projects first: a restored session finds its project, and with it the scope its agent is told,
	// its worktrees of the project's other repositories and its runtime.
	s.openProjectStore()
	s.openSessionStore()
	s.openApprovalStore()
	// The default session belongs to no project, so it works in a folder of its own like any free
	// session. Left in the workspace root, it - and every scheduled task that names no session -
	// would run in the parent of every project, worktree and session folder, and reach them all.
	s.placeFreeStanding(first, false)
	// Resume any sessions that were mid-run when the previous gateway shut down (e.g. for an
	// upgrade), and start what they had queued. This must happen AFTER loadPersistedSessions has
	// registered every conversation and wired its service, because starting a run needs a fully
	// constructed conversation - and after the default session has its own folder and the
	// projects are open, or its resumed run would work in the workspace root.
	//
	// And after the schedule store and the push sender are open: a resumed run that finishes
	// records itself in the former and notifies through the latter, and both are read without a
	// lock once the gateway serves.
	s.openScheduleStore()
	s.openPush()
	s.openFriction()
	s.openPeers()
	resumeInterrupted(s)
	s.indexProjects()
	// The origin policy wraps the ENTIRE routing table, including the page and /v1/health.
	//
	// Everything rather than only the API, and that is a decision: a rule set that still served the
	// page to a refused origin would be a rule set that only half applied, and the page is the one
	// thing a browser asks for BEFORE it holds any credential. Refusing /v1/health too means a
	// client that is not allowed in finds out immediately, with a 403 that names the reason,
	// instead of having its health probe succeed and its every real call fail.
	//
	// The policy is enforced BEFORE the token check, so a refused origin does not even learn
	// whether its credential was good.
	s.hosts = knownHosts(opts.Listen, opts.Hosts)
	// This machine's tailnet name is one of its own names, like its .local one, and the one
	// `tailscale serve` forwards under. Learned in the background: asking the daemon must not
	// hold the start, and a request under that name before the answer is a rare 421 that a
	// reload clears.
	if opts.Tailscale != nil {
		go s.learnTailnetName(s.baseCtx)
	}
	s.mux = bodyDeadline(s.originPolicy(s.hostPolicy(securityHeaders(s.routes()))))
	s.server = &http.Server{
		Handler: s.mux,
		// No WriteTimeout. It is a deadline on the WHOLE response, and half of these responses
		// are a run that streams until the agent is done: a deadline there would cut a slow
		// answer off mid-sentence, which reads to the user as "the agent stopped". No ReadTimeout
		// either: it stays armed while the response streams, and when it fires net/http cancels
		// the request context of that stream. A stalled header is bounded by ReadHeaderTimeout,
		// and a stalled body by bodyDeadline.
		ReadHeaderTimeout: 10 * time.Second,
		IdleTimeout:       90 * time.Second,
	}
	// Start the periodic update checker. It runs every hour, and the cached
	// result is what the frontend reads via /v1/update/check — so a user who
	// never opens the settings modal still sees a toast when a new release
	// appears, without the frontend having to poll GitHub itself.
	s.startUpdateChecker()
	s.startRemoteWatch()
	s.startArtifactPruner(artifactPruneInterval)
	return s, nil
}

// resumeInterrupted is what the start does about sessions that were mid-run; a variable so a test
// can see what is open by then.
var resumeInterrupted = (*Server).resumeInterruptedSessions

// listenTLS binds the https listener, or returns nil when none is configured. The key pair is
// loaded HERE, at start, so a missing or mismatched certificate is a startup error that names the
// file rather than a handshake failure every browser reports differently.
func listenTLS(opts Options) (net.Listener, error) {
	if strings.TrimSpace(opts.TLSListen) == "" {
		return nil, nil
	}
	cert, err := tls.LoadX509KeyPair(opts.TLSCert, opts.TLSKey)
	if err != nil {
		return nil, fmt.Errorf("gateway.tls: the certificate %s and key %s could not be loaded: %w",
			opts.TLSCert, opts.TLSKey, err)
	}
	ln, err := net.Listen("tcp", strings.TrimSpace(opts.TLSListen))
	if err != nil {
		return nil, fmt.Errorf("could not listen on %s for https: %w", opts.TLSListen, err)
	}
	return tls.NewListener(ln, &tls.Config{Certificates: []tls.Certificate{cert}, MinVersion: tls.VersionTLS12}), nil
}

// TLSAddr is the address of the https listener, or "" when there is none.
func (s *Server) TLSAddr() string {
	if s.tlsListener == nil {
		return ""
	}
	return s.tlsListener.Addr().String()
}

// listenAddr is the address the gateway binds: the configured one, or a free loopback port.
func listenAddr(listen string) string {
	addr := strings.TrimSpace(listen)
	if addr == "" {
		addr = "127.0.0.1:0"
	}
	return addr
}

// resolveHeartbeat is the heartbeat the stream loop uses. Resolved at construction so the loop
// never has to tell the default from off: a zero asks for the default, a negative for off, and off
// comes out as zero.
func resolveHeartbeat(heartbeat time.Duration) time.Duration {
	if heartbeat == 0 {
		heartbeat = defaultHeartbeat
	}
	if heartbeat < 0 {
		heartbeat = 0
	}
	return heartbeat
}

// info logs an event when the gateway has a logger.
func (s *Server) info(msg string, args ...any) {
	if s.opts.Log != nil {
		s.opts.Log.Info(msg, args...)
	}
}

// warn logs a warning when the gateway has a logger.
func (s *Server) warn(msg string, args ...any) {
	if s.opts.Log != nil {
		s.opts.Log.Warn(msg, args...)
	}
}

// openSessionStore opens the session store when a directory is configured, and loads every
// persisted conversation from it. A failure to open the store is NOT fatal: the gateway still
// works in-memory, and the operator can fix the directory without losing the running server. A
// failure to LOAD a session is skipped: one corrupt file must not block the rest.
func (s *Server) openSessionStore() {
	dir := strings.TrimSpace(s.opts.SessionDir)
	if dir == "" {
		return
	}
	st, err := newSessionStore(dir)
	if err != nil {
		s.warn("could not open the session store; sessions will not persist", "error", err.Error())
		return
	}
	s.store = st
	s.loadPersistedSessions()
	s.migrateSharedRepos(s.baseCtx)
}

// openScheduleStore opens the schedule store when a directory is configured, and starts the
// watcher. A failure to open it is NOT fatal: the gateway serves, and the endpoints report an
// empty list, which is the same answer a deployment without scheduling gives.
func (s *Server) openScheduleStore() {
	sdir := strings.TrimSpace(s.opts.ScheduleDir)
	if sdir == "" {
		return
	}
	st, err := schedule.Open(sdir)
	if err != nil {
		s.warn("could not open the schedule store; scheduled tasks will not run", "error", err.Error())
		return
	}
	s.schedules = st
	// The watcher is started HERE and not in a caller, so that a gateway with a schedule
	// directory always has one: without this line every scheduled task passes its tests and
	// never fires in production, because nothing ever looks at the clock.
	s.startScheduler()
	s.syncAutopilot()
}

// Addr is the address a CLIENT should call, with the real port when 0 was asked for.
//
// It is not simply what the listener reports, because for a wildcard bind the listener reports
// "[::]" — the dual-stack wildcard Go picks for "0.0.0.0". That string is an address to LISTEN on
// and not one to dial: put in a URL it names no host a client can reach. Measured with a real
// browser: a page served on a wildcard bind loads over 127.0.0.1, over localhost and over the
// machine's LAN address, and comes back as an empty document over [::].
//
// So the wildcard is translated into the only address that means "this machine" to every client
// and works everywhere: loopback, keeping the real port. A remote client does NOT use this address
// — it uses the host it reached us on — which is exactly why exposure is answered by
// ReachableFromNetwork rather than read out of this string.
//
// Reporting the listener's address here would have put an uncallable URL into the startup
// announcement, into the service file that `status`, `stop` and the client all discover the
// gateway from, and therefore into every client that ever resolved it.
func (s *Server) Addr() string {
	addr := s.listener.Addr().String()
	host, port, err := net.SplitHostPort(addr)
	if err != nil {
		return addr
	}
	if isLoopback(host) {
		return addr
	}
	return net.JoinHostPort("127.0.0.1", port)
}

// ReachableFromNetwork reports whether this gateway is bound beyond loopback.
//
// It exists because Addr() can no longer answer it: normalising the wildcard into loopback would
// make a network-bound gateway LOOK like a local one. The two are different questions — which
// address to call, and how far this is exposed — and collapsing them is how a service file claims a
// gateway is private while it answers the whole network.
func (s *Server) ReachableFromNetwork() bool {
	host, _, err := net.SplitHostPort(s.listener.Addr().String())
	if err != nil {
		return false
	}
	return !isLoopback(host)
}

// AllowDescription is the origin rule set this gateway enforces, as the SERVER understands it.
//
// It is read from the server rather than from the configuration it was built from so that the
// description and the enforcement cannot disagree: what is written to the service file is the
// policy this process is actually applying, and a caller that re-parsed the configuration would be
// describing its own reading of the file rather than the gateway's behaviour.
func (s *Server) AllowDescription() string {
	if s.opts.Allow == nil {
		// No policy at all is the same outcome as an empty one: no rule restricts anything. It is
		// reported through the policy's own words rather than a second phrase invented here, so the
		// two spellings cannot drift.
		return (&netrules.Policy{}).Describe()
	}
	return s.opts.Allow.Describe()
}

// BaseURL is the origin a local client should speak to.
func (s *Server) BaseURL() string { return "http://" + s.Addr() }

// Token is the bearer token this server requires.
func (s *Server) Token() string { return s.opts.Token }

// Handler is the router. It is exposed so a test can drive the endpoints without a socket when
// a socket is not the thing being tested.
func (s *Server) Handler() http.Handler { return s.mux }

// Serve blocks until Close. A closed listener is reported as nil: that is how this server
// stops, not a failure.
func (s *Server) Serve() error {
	if s.opts.Log != nil {
		s.opts.Log.Info("the gateway is listening", "address", s.Addr())
		if s.ReachableFromNetwork() {
			// Said in EVERY mode, not only by `gateway start`: an interface with an embedded
			// gateway on a LAN address exposes exactly the same plain-HTTP socket.
			s.opts.Log.Warn("the gateway is reachable from the network over plain HTTP: the token and "+
				"every request travel unencrypted; gateway.tls or an SSH tunnel to a loopback listen avoids this",
				"address", s.listener.Addr().String())
		}
	}
	if s.tlsListener != nil {
		s.info("the gateway is listening over https", "address", s.TLSAddr())
		// Same server, second socket: Shutdown closes both, and a failure of this one is
		// reported without taking the plain listener - the one the local clients use - down.
		go func() {
			if err := s.server.Serve(s.tlsListener); err != nil && !errors.Is(err, http.ErrServerClosed) {
				s.warn("the https listener stopped", "error", err.Error())
			}
		}()
	}
	if err := s.server.Serve(s.listener); err != nil && !errors.Is(err, http.ErrServerClosed) {
		return err
	}
	return nil
}

// Close stops the listener and waits for the connections in flight, bounded by ctx.
//
// The runs in flight are cancelled FIRST. A run is not bounded by its connection any more, so
// without this a gateway that shut down would leave a turn running with nowhere to report - and it
// is cancelled before the listener closes, so the client that was watching it gets the cancellation
// on its stream rather than a connection that just ends.
func (s *Server) Close(ctx context.Context) error {
	s.closeOnce.Do(func() {
		// Every session is written BEFORE the runs are cancelled, while the ones in flight
		// still hold their slot: they are persisted as running, with the task they were on,
		// and the next process resumes them. After the cancel a run's own final save races
		// the process exit, and a lost race is a lost session.
		s.saveAllSessions()
		s.baseCancel()
		// The "run now" runs just cancelled write their end onto their task's record: that is
		// done before the process leaves, as far as the caller is willing to wait.
		finished := make(chan struct{})
		go func() {
			s.scheduleWG.Wait()
			s.remoteWG.Wait()
			close(finished)
		}()
		select {
		case <-finished:
		case <-ctx.Done():
		}
		s.closeErr = s.server.Shutdown(ctx)
	})
	return s.closeErr
}

// voiceRoutes registers the speech endpoints, every one process-wide.
func (s *Server) voiceRoutes(mux *http.ServeMux, plain func(http.HandlerFunc) http.Handler) {
	// Speech input: process-wide, and the recording is never stored. See voice.go.
	mux.Handle("GET /v1/voice", plain(s.handleVoice))
	mux.Handle("PUT /v1/voice", plain(s.handlePutVoice))
	mux.Handle("POST /v1/voice/transcribe", plain(s.handleTranscribe))
	mux.Handle("POST /v1/voice/wake", plain(s.handleWake))
	// Speech output: the answer's text goes to the speech service, the audio comes back. See speak.go.
	mux.Handle("POST /v1/voice/speak", plain(s.handleSpeak))
	// Local voices: the catalog, its downloads, and the files the page runs. See voicemodels.go.
	mux.Handle("GET /v1/voice/models", plain(s.handleVoiceModels))
	mux.Handle("POST /v1/voice/models/{id}", plain(s.handleInstallVoiceModel))
	mux.Handle("DELETE /v1/voice/models/{id}", plain(s.handleDeleteVoiceModel))
	mux.Handle("GET /v1/voice/models/{id}/sample", plain(s.handleVoiceModelSample))
	mux.Handle("GET /v1/voice/models/{id}/{name}", plain(s.handleVoiceModelFile))
}

// skillRoutes registers the endpoints of the procedure library under prefix.
func (s *Server) skillRoutes(mux *http.ServeMux, prefix string, wrap func(http.HandlerFunc) http.Handler) {
	mux.Handle("GET "+prefix+"/skills", wrap(s.handleSkills))
	mux.Handle("POST "+prefix+"/skills", wrap(s.handleSaveSkill))
	mux.Handle("GET "+prefix+"/skills/archived", wrap(s.handleArchivedSkills))
	mux.Handle("GET "+prefix+"/skills/proposed", wrap(s.handleProposedSkills))
	mux.Handle("GET "+prefix+"/skills/proposed/{name}", wrap(s.handleProposedSkill))
	mux.Handle("POST "+prefix+"/skills/proposed/{name}/accept", wrap(s.handleAcceptProposedSkill))
	mux.Handle("DELETE "+prefix+"/skills/proposed/{name}", wrap(s.handleRejectProposedSkill))
	mux.Handle("GET "+prefix+"/skills/{name}", wrap(s.handleSkill))
	mux.Handle("POST "+prefix+"/skills/{name}/pin", wrap(s.handlePinSkill))
	mux.Handle("POST "+prefix+"/skills/{name}/disable", wrap(s.handleDisableSkill))
	mux.Handle("DELETE "+prefix+"/skills/{name}", wrap(s.handleDeleteSkill))
	mux.Handle("POST "+prefix+"/skills/{name}/restore", wrap(s.handleRestoreSkill))
}

// projectRoutes is the table of projects and their repositories; the notes have their own.
func (s *Server) projectRoutes(mux *http.ServeMux, plain func(http.HandlerFunc) http.Handler) {
	mux.Handle("GET /v1/projects", plain(s.handleListProjects))
	s.notesRoutes(mux, plain)
	s.nestedRoutes(mux, plain)
	mux.Handle("GET /v1/projects/{id}/code-index", plain(s.handleProjectCodeIndex))
	mux.Handle("POST /v1/projects/{id}/code-index", plain(s.handleBuildProjectCodeIndex))
	mux.Handle("POST /v1/projects", plain(s.handleCreateProject))
	mux.Handle("PATCH /v1/projects/{id}", plain(s.handleUpdateProject))
	mux.Handle("GET /v1/projects/{id}/branches", plain(s.handleProjectBranches))
	mux.Handle("DELETE /v1/projects/{id}", plain(s.handleDeleteProject))
	mux.Handle("POST /v1/projects/{id}/main/refresh", plain(s.handleRefreshProjectMain))
}

// routes builds the endpoint table.
//
// Every conversation endpoint is addressed by the session it is about, and only /v1/health is
// process-wide. One rule with no exceptions is worth more than a shorter table: a reader who
// knows it never has to check which endpoints are about a conversation.
func (s *Server) routes() *http.ServeMux {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /v1/health", s.handleHealth)
	// Who this gateway is, for another one on the network looking for it: no token, like health.
	mux.HandleFunc("GET "+peers.HelloPath, s.handlePeerHello)

	// The interface, when it is on, BEFORE the API: it is the one part of this server that is
	// deliberately reachable without a token, because it is the only way a browser can get one.
	//
	// Every name the webui package exposes gets a route. The root is registered as "GET /{$}"
	// so that it matches ONLY "/"; every other path uses its own exact pattern. A bare "GET /"
	// in Go's ServeMux matches everything not otherwise registered, which would answer a typo
	// with a page that renders - a bug that looks like it worked.
	if s.opts.WebUI {
		for _, name := range webui.Names() {
			pattern := "GET " + name
			if name == "/" {
				pattern = "GET /{$}"
			}
			s.page(mux, pattern, name)
		}
	}

	// auth wraps anything, not just a HandlerFunc: withConversation hands back a Handler, and
	// forcing it through a HandlerFunc would be a cast that says nothing.
	auth := s.requireAuth
	// plain is the same check for a bare handler function, so the session-list endpoints do not
	// have to be cast to satisfy a signature.
	plain := func(h http.HandlerFunc) http.Handler { return auth(h) }
	// scoped is every handler that speaks ABOUT a conversation: the token is checked, then the
	// session is resolved, then the handler runs with it in the request context.
	scoped := func(h http.HandlerFunc) http.Handler { return auth(s.withConversation(h)) }

	s.sessionRoutes(mux, plain)
	mux.Handle("DELETE /v1/sessions/{id}", scoped(s.handleDeleteSession))
	mux.Handle("GET /v1/sessions/{id}/deletion-preview", scoped(s.handleDeletionPreview))
	mux.Handle("GET /v1/projects/{id}/deletion-preview", plain(s.handleProjectDeletionPreview))

	s.gitRoutes(mux, plain)
	s.routeDisk(mux, plain)
	s.projectRoutes(mux, plain)
	// Scheduled tasks are addressed by the PROCESS, not by a conversation, for the same
	// reason projects are: a schedule exists whether or not anyone is talking to the
	// agent, and the conversation it fires INTO is a field of the record. Everything
	// that speaks ABOUT a conversation stays under /v1/sessions/{id}.
	//
	// Only the list and the create were registered first, because every commit's route
	// table is a table every route in it answers for. The three that change or run a
	// task arrive with the handlers that serve them.
	mux.Handle("GET /v1/schedules", plain(s.handleListSchedules))
	mux.Handle("POST /v1/schedules", plain(s.handleCreateSchedule))
	mux.Handle("PATCH /v1/schedules/{id}", plain(s.handleUpdateSchedule))
	mux.Handle("DELETE /v1/schedules/{id}", plain(s.handleDeleteSchedule))
	mux.Handle("POST /v1/schedules/{id}/run", plain(s.handleRunScheduleNow))
	mux.Handle("GET /v1/commands", plain(s.handleListCommands))
	// The interface language: PROCESS-WIDE, like the library, because it is one setting of the
	// one configuration every conversation runs on.
	mux.Handle("GET /v1/ui", plain(s.handleGetUI))
	mux.Handle("PUT /v1/ui", plain(s.handlePutUI))
	s.routePrefs(mux, plain)
	// The LLM providers and the default model: process-wide too, saved beside and in the
	// configuration. See llm.go.
	s.llmRoutes(mux, plain)
	s.evalRoutes(mux, plain)
	s.llmLoginRoutes(mux, plain)
	// Speech input and output, and the local voices: process-wide too. See voiceRoutes.
	s.voiceRoutes(mux, plain)
	// How much of the disk the agent reaches: process-wide, every conversation follows it. See
	// security.go.
	mux.Handle("GET /v1/sessions/{id}/scope", scoped(s.handleSessionScope))
	// How far the gateway reaches and its Tailscale publication: process-wide. See network.go.
	mux.Handle("GET /v1/network", plain(s.handleGetNetwork))
	mux.Handle("PUT /v1/network", plain(s.handlePutNetwork))
	mux.Handle("POST /v1/network/tailscale", plain(s.handleTailscale))
	mux.Handle("POST /v1/network/restart", plain(s.handleRestartNetwork))
	// Phone notifications: process-wide, the browsers subscribed to them. See push.go.
	s.peerRoutes(mux, plain)
	mux.Handle("GET /v1/push", plain(s.handleGetPush))
	mux.Handle("POST /v1/push/subscriptions", plain(s.handleAddPush))
	mux.Handle("DELETE /v1/push/subscriptions", plain(s.handleRemovePush))
	mux.Handle("POST /v1/push/test", plain(s.handleTestPush))

	// The friction journal: what got in the way, as a weekly summary the settings show.
	mux.Handle("GET /v1/friction", plain(s.handleGetFriction))
	mux.Handle("DELETE /v1/friction", plain(s.handleClearFriction))

	// The procedure library and its maintenance pass. PROCESS-WIDE, like /v1/projects
	// and /v1/commands: a skill is not a property of one conversation, and the ledger
	// behind it is shared by every conversation this process holds.
	//
	// "GET /v1/skills/archived" is registered EXPLICITLY and it is more specific than
	// "GET /v1/skills/{name}", so it wins: a skill that happens to be called "archived"
	// is still reachable by opening it from the index. That precedence is asserted by a
	// test rather than trusted to the mux.
	s.skillRoutes(mux, "/v1", plain)
	// The SAME library, addressed by the conversation it is for: every door of it, not only
	// the index.
	//
	// A library is not process-wide any more: a session that belongs to a project gets its
	// own documents layered over the shared shelf, and its own telemetry and proposals, so
	// "what does this session see" is a question only the session can answer - and opening,
	// saving, pinning, turning off or deleting a project's document from a window inside the
	// project must act on THAT document, never on a shared one of the same name. The
	// process-wide routes above stay exactly as they were: they are the shared shelf.
	//
	// They are scoped, so a session that does not exist is refused here rather than answered
	// about the wrong library.
	s.skillRoutes(mux, "/v1/sessions/{id}", scoped)
	mux.Handle("GET /v1/curator", plain(s.handleCurator))
	mux.Handle("POST /v1/curator/run", plain(s.handleCuratorRun))

	// Self-update endpoints: check for a newer release and stream the upgrade.
	// These are plain (not scoped to a session) because the upgrade is about
	// the gateway process itself, not any one conversation.
	mux.Handle("GET /v1/update/check", plain(s.handleUpdateCheck))
	mux.Handle("POST /v1/update/run", plain(s.handleUpdateRun))
	// The bearer token specifically, not the browser cookie: stopping the service is the CLI's
	// job (`gateway stop`), and no page has a reason to do it.
	mux.Handle("POST /v1/shutdown", requireBearer(s.opts.Token, http.HandlerFunc(s.handleShutdown)))

	mux.Handle("GET /v1/sessions/{id}", scoped(s.handleSession))
	mux.Handle("GET /v1/sessions/{id}/report", scoped(s.handleSessionReport))
	mux.Handle("GET /v1/sessions/{id}/artifacts", scoped(s.handleListArtifacts))
	mux.Handle("GET /v1/sessions/{id}/artifacts/{name}", scoped(s.handleGetArtifact))
	mux.Handle("PUT /v1/sessions/{id}/artifacts/{name}", scoped(s.handlePutArtifact))
	mux.Handle("POST /v1/sessions/{id}/artifacts/{name}/pin", scoped(s.handlePinArtifact))
	mux.Handle("DELETE /v1/sessions/{id}/artifacts/{name}", scoped(s.handleDeleteArtifact))
	mux.Handle("GET /v1/sessions/{id}/messages", scoped(s.handleMessages))
	mux.Handle("POST /v1/sessions/{id}/reset", scoped(s.handleReset))
	mux.Handle("GET /v1/sessions/{id}/checkpoints", scoped(s.handleCheckpoints))
	mux.Handle("POST /v1/sessions/{id}/checkpoints/{turn}/restore", scoped(s.handleRestoreCheckpoint))
	mux.Handle("PUT /v1/sessions/{id}/checkpoints/{turn}/answers", scoped(s.handleSuggestionAnswers))
	mux.Handle("PUT /v1/sessions/{id}/checkpoints/{turn}/question-answers", scoped(s.handleQuestionAnswers))
	mux.Handle("GET /v1/sessions/{id}/config", scoped(s.handleConfig))
	mux.Handle("PATCH /v1/sessions/{id}/config", scoped(s.handleUpdateConfig))
	mux.Handle("POST /v1/sessions/{id}/config/reload", scoped(s.handleReloadConfig))
	mux.Handle("PATCH /v1/sessions/{id}", scoped(s.handleRenameSession))
	mux.Handle("POST /v1/sessions/{id}/project", scoped(s.handleMoveSessionToProject))
	mux.Handle("POST /v1/sessions/{id}/isolate", scoped(s.handleIsolateSession))
	mux.Handle("GET /v1/sessions/{id}/models", scoped(s.handleModels))
	mux.Handle("GET /v1/sessions/{id}/providers", scoped(s.handleProviders))
	mux.Handle("GET /v1/sessions/{id}/model-list", scoped(s.handleModelList))
	mux.Handle("POST /v1/sessions/{id}/reasoning", scoped(s.handleReasoning))
	mux.Handle("POST /v1/sessions/{id}/verdict", scoped(s.handleVerdict))
	mux.Handle("POST /v1/sessions/{id}/friction", scoped(s.handleFlagAnswer))
	mux.Handle("GET /v1/sessions/{id}/reward", scoped(s.handleReward))
	mux.Handle("GET /v1/sessions/{id}/questions", scoped(s.handleQuestions))
	mux.Handle("POST /v1/sessions/{id}/task", scoped(s.handleTask))
	mux.Handle("POST /v1/sessions/{id}/plan", scoped(s.handlePlan))
	mux.Handle("POST /v1/sessions/{id}/run", scoped(s.handleRun))
	mux.Handle("POST /v1/sessions/{id}/narrate", scoped(s.handleNarrate))
	mux.Handle("POST /v1/sessions/{id}/next-suggestion", scoped(s.handleNextSuggestion))
	mux.Handle("POST /v1/sessions/{id}/merge", scoped(s.handleMergeSession))
	mux.Handle("POST /v1/sessions/{id}/continue", scoped(s.handleContinueSession))
	mux.Handle("GET /v1/sessions/{id}/pr", scoped(s.handleGetPR))
	mux.Handle("POST /v1/sessions/{id}/pr", scoped(s.handleCreatePR))
	mux.Handle("POST /v1/sessions/{id}/pr/fix", scoped(s.handleFixPR))
	mux.Handle("POST /v1/sessions/{id}/pr/merge", scoped(s.handleMergePR))
	mux.Handle("GET /v1/sessions/{id}/run", scoped(s.handleRunStatus))
	mux.Handle("GET /v1/sessions/{id}/agents", scoped(s.handleAgents))
	mux.Handle("GET /v1/sessions/{id}/events", scoped(s.handleAttach))
	mux.Handle("POST /v1/sessions/{id}/cancel", scoped(s.handleCancelRun))
	mux.Handle("POST /v1/sessions/{id}/queue", scoped(s.handleQueue))
	mux.Handle("POST /v1/sessions/{id}/steer", scoped(s.handleSteer))
	s.shellRoutes(mux, scoped)
	mux.Handle("POST /v1/sessions/{id}/interrupt", scoped(s.handleInterrupt))
	mux.Handle("POST /v1/sessions/{id}/runs/approval", scoped(s.handleApproval))
	mux.Handle("POST /v1/sessions/{id}/auto-approve", scoped(s.handleAutoApprove))

	// WebSocket endpoint: a bidirectional, persistent connection that speaks the
	// flag-based message protocol (auth, query, heartbeat, notification, error). It
	// shares the same conversation, token and run slot as the HTTP API — a second
	// transport, not a second agent.
	mux.Handle("GET /v1/sessions/{id}/ws", scoped(s.handleWebSocket))
	if s.opts.WebUI {
		// Authorised by the BEARER token specifically, not by the cookie: this is where the
		// token taken from the URL fragment is exchanged for the browser's cookie, and it must
		// stay single-entry. A browser that already holds a cookie has no business minting
		// itself another one, so requireBearer is used rather than the general check.
		mux.Handle("POST /v1/webui/session", requireBearer(s.opts.Token, http.HandlerFunc(s.handleWebUISession)))
		// Logout: clears the browser's cookie. No credential required — clearing a credential
		// is not a privilege, and the reason it is called is that the credential the browser
		// holds is no longer valid. The origin policy still applies.
		mux.Handle("DELETE /v1/webui/session", http.HandlerFunc(s.handleWebUILogout))
	}
	return mux
}

// page registers one page file at one exact pattern.
func (s *Server) page(mux *http.ServeMux, pattern, name string) {
	mux.HandleFunc(pattern, func(w http.ResponseWriter, r *http.Request) {
		body, ctype, err := webui.Content(name)
		if err != nil {
			// The name comes from this file, never from the request, so an error here means the
			// binary and the router disagree. A 404 is the honest answer to that.
			http.NotFound(w, r)
			return
		}
		w.Header().Set(headerContentType, ctype)
		// No caching. The page is part of the binary, so a client holding yesterday's copy
		// after an upgrade is running code that no longer exists - and a stale page calling an
		// API that moved is a failure nobody can explain.
		w.Header().Set("Cache-Control", "no-store")
		_, _ = w.Write(body)
	})
}

// handleWebUISession exchanges the token for the browser's cookie.
//
// The token arrives as a bearer header, which the page's script took from the URL FRAGMENT: a
// fragment is never sent to the server and never appears in a log or a Referer, which is the
// only way to put a secret in a URL without it travelling. From here on the browser holds a
// DERIVED value, not the token (see cookieValue). It is NOT harmless: it authorises the whole
// API from this browser. What it cannot do is serve as the bearer token or mint another cookie,
// and a state-changing request it authorises must also pass crossSiteRefusal.
func (s *Server) handleWebUISession(w http.ResponseWriter, r *http.Request) {
	// Long-lived because it is derived, not stored: it stays valid until the token rotates,
	// and rotating the token invalidates it with nothing to clean up.
	setSessionCookie(w, r, cookieValue(s.opts.Token), 30*24*3600)
	w.WriteHeader(http.StatusNoContent)
}

// setSessionCookie sends the browser's session cookie with every protection the connection allows.
//
//   - HttpOnly: a script cannot read it, so an injected script cannot exfiltrate it.
//   - SameSite=Strict: another SITE never sends it. Another port of this same host is the same site,
//     though, and cookies are not isolated by port: that page's browser DOES attach it, which is what
//     crossSiteRefusal (the X-Motita header, the Origin and the JSON content type) is for. This
//     server sends no CORS header, so no other origin can read a response.
//   - Secure: whenever the browser reached the gateway over TLS, directly or through a proxy that
//     terminates it (X-Forwarded-Proto). Over plain http it is NOT set, deliberately: the gateway
//     plain listener has no TLS (gateway.tls is a second one that does), and a Secure cookie is one
//     a browser refuses to send over http, so setting it there would silently break the interface -
//     the browser would never stay connected, with nothing in any log to say why. That case is
//     written as the same header by hand, without the Secure attribute, for that reason alone.
//
// A negative maxAge expires the cookie now.
func setSessionCookie(w http.ResponseWriter, r *http.Request, value string, maxAge int) {
	if r.TLS != nil || strings.EqualFold(r.Header.Get("X-Forwarded-Proto"), "https") {
		http.SetCookie(w, &http.Cookie{
			Name: webuiCookie, Value: value, Path: "/", MaxAge: maxAge,
			HttpOnly: true, SameSite: http.SameSiteStrictMode, Secure: true,
		})
		return
	}
	if maxAge < 0 {
		maxAge = 0
	}
	w.Header().Add("Set-Cookie",
		fmt.Sprintf("%s=%s; Path=/; Max-Age=%d; HttpOnly; SameSite=Strict", webuiCookie, value, maxAge))
}

// handleWebUILogout clears the browser's cookie so the blocking auth modal
// reappears. It is the complement to handleWebUISession: one mints a cookie,
// the other removes it. No credential is required because the caller is, by
// definition, a browser that holds a credential that no longer works.
func (s *Server) handleWebUILogout(w http.ResponseWriter, r *http.Request) {
	setSessionCookie(w, r, "", -1) // delete immediately
	w.WriteHeader(http.StatusNoContent)
}

// originPolicy refuses requests from an origin the configured rules do not allow.
//
// It reads the client's address from the CONNECTION, never from a header. That is the whole
// security property of this middleware: X-Forwarded-For, X-Real-IP and friends are attacker-supplied
// strings, so honouring one would let anybody reach a gateway that had been restricted to one office
// address by simply claiming to be it. A header-based check is how an allow list turns into a
// decoration. (A deployment behind a real reverse proxy would need a setting naming the proxies it
// trusts; that setting does not exist, and this comment is why it must be a deliberate addition
// rather than an accident.)
//
// A NIL policy is the open one: it is what a caller that never configured a rule set passes, and it
// is the documented default rather than an error.
func (s *Server) originPolicy(next http.Handler) http.Handler {
	if s.opts.Allow == nil {
		return next
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// RemoteAddr is "ip:port" as reported by the socket. An address that cannot be parsed is
		// NOT treated as allowed: a request whose origin cannot be established is exactly the one
		// to refuse, and net/rpc-style "the transport guarantees it" reasoning does not hold across
		// every listener Go supports.
		addrPort, err := netip.ParseAddrPort(r.RemoteAddr)
		if err != nil {
			writeError(w, http.StatusForbidden,
				"this gateway could not determine the origin of the request, so it will not serve it")
			return
		}
		if !s.opts.Allow.Allows(addrPort.Addr()) {
			// The message names the SETTING, so an operator reading this from the other machine
			// can tell "the rules do not cover me" from "something is broken". The rules
			// themselves go to the log only: describing them to a client that is not allowed in
			// would hand a stranger the map of who is.
			if s.opts.Log != nil {
				s.opts.Log.Warn("refused a request from an origin outside gateway.allow",
					"origin", addrPort.Addr().String(), "allow", s.opts.Allow.Describe())
			}
			writeError(w, http.StatusForbidden, fmt.Sprintf(
				"this gateway does not serve requests from %s: its gateway.allow rules do not include this address",
				addrPort.Addr()))
			return
		}
		next.ServeHTTP(w, r)
	})
}

// pageCSP is the content policy of everything this server answers. The page and every script it
// loads are served from here and nothing is inline, so scripts need no 'unsafe-inline' and no
// 'unsafe-eval'. Styles do need 'unsafe-inline': mermaid and KaTeX lay their output out with style
// attributes and <style> elements. Images are this server's, or data:/blob: (the previews arrive
// inline), so an answer cannot make the browser fetch an address of the model's choosing. Media is
// the same: the gateway's voice arrives as a blob the page plays. 'wasm-unsafe-eval' lets the page
// compile WebAssembly (the local voices run as WebAssembly), and nothing else: JavaScript eval stays
// refused, and the only WebAssembly there is to fetch is this server's, checked against the
// catalog's hashes.
const pageCSP = "default-src 'self'; script-src 'self' 'wasm-unsafe-eval'; style-src 'self' 'unsafe-inline'; " +
	"img-src 'self' data: blob:; media-src 'self' blob:; font-src 'self' data:; connect-src 'self'; worker-src 'self'; " +
	"object-src 'none'; base-uri 'none'; form-action 'self'; frame-ancestors 'none'"

// securityHeaders sets the headers every response carries: the page must not be framed by another
// site (a frame is how a fake approval dialog gets laid over a real button), the browser must not
// guess a type, and no URL of this gateway leaks to another site through a Referer. A handler that
// needs a different policy for its own content (an artifact) replaces the CSP itself.
func securityHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h := w.Header()
		h.Set("Content-Security-Policy", pageCSP)
		h.Set("X-Content-Type-Options", "nosniff")
		h.Set("Referrer-Policy", "no-referrer")
		next.ServeHTTP(w, r)
	})
}

// osHostname is this machine's name, a variable so a test can make it fail.
var osHostname = os.Hostname

// knownHosts is the set of host NAMES this gateway answers to besides an IP address and
// localhost: the host in the listen address, this machine's own name (bare and as its mDNS
// .local name), and the names the operator listed in gateway.hosts.
func knownHosts(listen string, extra []string) map[string]bool {
	set := map[string]bool{}
	add := func(h string) {
		if h = normalHost(h); h != "" {
			set[h] = true
		}
	}
	if h, _, err := net.SplitHostPort(strings.TrimSpace(listen)); err == nil {
		add(h)
	}
	if name, err := osHostname(); err == nil && name != "" {
		add(name)
		add(strings.SplitN(name, ".", 2)[0] + ".local")
	}
	for _, h := range extra {
		add(h)
	}
	return set
}

// normalHost lower-cases a host name and drops IPv6 brackets and a trailing root dot, so the
// spellings a browser may send compare equal.
func normalHost(h string) string {
	return strings.TrimSuffix(strings.ToLower(strings.Trim(strings.TrimSpace(h), "[]")), ".")
}

// hostPolicy refuses a request whose Host header names a host this gateway does not know.
//
// It is the defence against DNS rebinding: a page on attacker.example re-points its own name at
// 127.0.0.1, and the browser then treats this gateway as that page's origin and sends
// "Host: attacker.example". Every name such a page can use is a NAME, so an IP address in the
// Host header is always accepted (that is how a LAN client and a tunnel reach the gateway), as
// are localhost and the names knownHosts collects. A request with no Host at all (HTTP/1.0)
// cannot come from a browser and is left alone.
func (s *Server) hostPolicy(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		host := r.Host
		if h, _, err := net.SplitHostPort(host); err == nil {
			host = h
		}
		host = normalHost(host)
		if !isLocalHostName(host) && !s.knowsHost(host) {
			writeError(w, http.StatusMisdirectedRequest, fmt.Sprintf(
				"this gateway does not answer to the host name %q: add it to gateway.hosts if it is yours", host))
			return
		}
		next.ServeHTTP(w, r)
	})
}

// isLoopback reports whether host names this machine only.
func isLoopback(host string) bool {
	if host == "" {
		return false
	}
	if strings.EqualFold(host, "localhost") {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

func (s *Server) handleHealth(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "version": s.opts.Version})
}

// handleUpdateCheck asks GitHub whether a newer release exists and returns
// the comparison to the frontend. A cached result younger than updateCacheTTL
// is returned immediately; an older one is checked again, because releases can
// land several times an hour and an hour-old answer names a version that is
// already behind. If that fresh check fails, the last good answer is kept.
func (s *Server) handleUpdateCheck(w http.ResponseWriter, r *http.Request) {
	if s.updater == nil {
		writeJSON(w, http.StatusOK, updater.CheckResult{
			CurrentVersion: s.opts.Version,
			Error:          "self-update is not available: no executable path was configured",
		})
		return
	}
	s.lastCheckMu.RLock()
	cached, at := s.lastCheck, s.lastCheckAt
	s.lastCheckMu.RUnlock()
	if cached.LatestVersion != "" && time.Since(at) < updateCacheTTL {
		writeJSON(w, http.StatusOK, cached)
		return
	}
	result := s.updater.Check(r.Context())
	if result.Error != "" && cached.LatestVersion != "" {
		writeJSON(w, http.StatusOK, cached)
		return
	}
	s.storeCheck(result)
	writeJSON(w, http.StatusOK, result)
}

// storeCheck caches an update check result together with the time it was taken.
func (s *Server) storeCheck(result updater.CheckResult) {
	s.lastCheckMu.Lock()
	s.lastCheck = result
	s.lastCheckAt = time.Now()
	s.lastCheckMu.Unlock()
}

// handleUpdateRun streams the upgrade as SSE events so the frontend can show
// a progress bar. After the binary is replaced, the handler signals the
// gateway to restart itself.
func (s *Server) handleUpdateRun(w http.ResponseWriter, r *http.Request) {
	if s.updater == nil {
		writeError(w, http.StatusBadRequest, "self-update is not available: no executable path was configured")
		return
	}

	w.Header().Set(headerContentType, "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")

	flusher, ok := w.(http.Flusher)
	if !ok {
		writeError(w, http.StatusInternalServerError, "streaming is not supported by this connection")
		return
	}

	// First, fetch the release info.
	sse := func(evt updater.ProgressEvent) {
		data, _ := json.Marshal(evt)
		fmt.Fprintf(w, "event: progress\ndata: %s\n\n", data)
		flusher.Flush()
	}

	sse(updater.ProgressEvent{Stage: "checking", Percent: 0, Message: "Checking latest release"})

	result := s.updater.Check(r.Context())
	if result.Error != "" {
		sse(updater.ProgressEvent{Stage: "error", Message: result.Error})
		return
	}
	s.storeCheck(result)
	if !result.UpdateAvailable {
		sse(updater.ProgressEvent{Stage: "done", Message: "Already up to date", Version: s.opts.Version})
		return
	}

	// Fetch the full release (the check only returned the summary).
	release, err := s.updater.LatestRelease(r.Context())
	if err != nil {
		sse(updater.ProgressEvent{Stage: "error", Message: err.Error()})
		return
	}

	if err := s.updater.DownloadAndInstall(r.Context(), release, sse); err != nil {
		sse(updater.ProgressEvent{Stage: "error", Message: err.Error()})
		return
	}

	// A process that cannot replace itself has done all it can: the binary is in place, and the
	// next start runs it. It stays up, because stopping it would leave nothing serving.
	if s.opts.Restart == nil {
		sse(updater.ProgressEvent{Stage: "done", Percent: 100, Version: release.TagName,
			Message: "Upgrade installed. This gateway does not restart itself: start motita again to run " + release.TagName})
		return
	}

	// Signal the frontend that the restart is coming.
	sse(updater.ProgressEvent{
		Stage: "restarting", Percent: 100, Message: "Restarting the gateway", Version: release.TagName,
	})

	// Persist every session — including ones that are mid-run — so the new
	// process can resume them. This is the whole point of the running/lastTask
	// fields in sessionRecord: a session that was working when the gateway
	// shut down for the upgrade picks up where it left off.
	s.saveAllSessions()

	// Restart in a goroutine so the SSE response can flush: the process stops, and whoever
	// supplied Restart brings the new binary up in its place.
	go func() {
		// Give the SSE response time to flush before the process exits.
		time.Sleep(500 * time.Millisecond)
		s.opts.Restart()
	}()

	// Send a final done event.
	sse(updater.ProgressEvent{
		Stage: "done", Percent: 100, Message: "Upgrade complete. The gateway is restarting.", Version: release.TagName,
	})
}

func (s *Server) handleConfig(w http.ResponseWriter, r *http.Request) {
	// viewOf, never the configuration itself: see view.go.
	writeJSON(w, http.StatusOK, viewOf(convOf(r).svc.Config()))
}

// isLocalHostName reports whether host needs no listing in gateway.hosts: no name at all, an IP
// address, or localhost and its subdomains.
func isLocalHostName(host string) bool {
	_, ipErr := netip.ParseAddr(host)
	return host == "" || ipErr == nil || host == "localhost" || strings.HasSuffix(host, ".localhost")
}

// writeJSON is the one place a response body is produced, so the Content-Type is set in the one
// place it could be forgotten.
func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set(headerContentType, "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

// writeError is the refusal shape, so a client parses one thing and not five.
func writeError(w http.ResponseWriter, status int, msg string) {
	writeJSON(w, status, map[string]string{"error": msg})
}

// decodeBody reads a bounded JSON body and reports a failure the client can act on. It returns
// false when it has already written the refusal, and the caller then returns.
//
// A request the browser cookie authorised must also SAY it is JSON. A cross-origin form or a
// text/plain fetch is a "simple" request a browser sends without asking; application/json is not,
// so requiring it is a second wall behind the X-Motita header (see crossSiteRefusal). A bearer
// client is not held to it: no page can attach the token.
func (s *Server) decodeBody(w http.ResponseWriter, r *http.Request, dst any) bool {
	if !bearerMatches(s.opts.Token, r.Header.Get("Authorization")) && !s.peerGranted(r) {
		if mt, _, _ := mime.ParseMediaType(r.Header.Get(headerContentType)); mt != "application/json" {
			writeError(w, http.StatusUnsupportedMediaType, "the request body must be sent as application/json")
			return false
		}
	}
	r.Body = http.MaxBytesReader(w, r.Body, int64(s.opts.MaxBodyKB)<<10)
	if err := json.NewDecoder(r.Body).Decode(dst); err != nil {
		// MaxBytesReader makes the read fail, so an oversized body lands here too and is
		// reported as the malformed request it is. One shape for both: the client's fix is to
		// send less, and the message says which.
		writeError(w, http.StatusBadRequest, fmt.Sprintf("the request body is not the JSON this endpoint expects: %v", err))
		return false
	}
	return true
}
