package onboard

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/madkoding/motita/internal/config"
	"github.com/madkoding/motita/internal/llm"
	"github.com/madkoding/motita/internal/oauth"
)

// ErrCancelled means the user stopped the wizard: nothing was written.
var ErrCancelled = errors.New("cancelled: nothing was written")

// Answers is what the wizard collected. The zero value means "ask".
type Answers struct {
	Provider string
	Model    string
	BaseURL  string
	// AnchorCommand is the check that decides PASS. Empty means "always pass",
	// which is written explicitly.
	AnchorCommand string
	AnchorArgs    []string
	// APIKey is optional. When given it is written to a separate file with 0600
	// permissions, never into the configuration.
	APIKey string
}

// Result reports what the wizard did.
type Result struct {
	ConfigPath      string
	CredentialsPath string // empty when no key was given
	// BackupPath is where the configuration the wizard replaced was kept, and empty when there
	// was none to replace.
	BackupPath string
	Provider   Provider
	Model      string
	// LoggedIn is true when a login (OAuth, device code) was stored instead of a key.
	LoggedIn bool
	// Keyless is true for a provider that takes no key here (a self-hosted Ollama).
	Keyless bool
}

// modelLister is the function the wizard uses to fetch models from hosts that
// publish them. It is a package-level variable so tests can replace it with a
// stub that does not hit the network.
var modelLister = listOllamaModels

// listOllamaModels is the real implementation.
func listOllamaModels(ctx context.Context, baseURL, apiKey string) ([]string, error) {
	return llm.ListOllamaModels(ctx, baseURL, apiKey)
}

// isPresetEmpty reports whether the preset has no answers set at all, which is
// the interactive case where the banner and the review are shown.
func isPresetEmpty(a Answers) bool {
	return a.Provider == "" && a.Model == "" && a.BaseURL == "" &&
		a.AnchorCommand == "" && len(a.AnchorArgs) == 0 && a.APIKey == ""
}

// setup is everything one pass through the wizard decided, before anything is written.
//
// It is a value of its own so the review can show it and the user can throw it away: the
// wizard used to write as it went, and the only way to correct a wrong answer was to finish,
// read the file and run the whole thing again.
type setup struct {
	provider Provider
	baseURL  string
	keyless  bool
	key      string
	// keptKey records that the key is the one already saved, so the review says so instead of
	// presenting it as new.
	keptKey  bool
	loggedIn bool
	model    string
	anchor   anchorChoice
}

// Run interviews the user, writes a working configuration and returns what it
// did. It reads answers from in and writes the conversation to out, so the whole
// flow can be tested without a terminal. Nothing is written if the user cancels:
// the file appears only once every answer is known and, when the wizard is
// interactive, once the user has reviewed and accepted them.
//
// The questions follow the order a newcomer thinks in: which service, how to reach
// it (endpoint and sign-in), which model, what proves a task is done, and a review.
// The sign-in comes BEFORE the model so a provider that publishes its catalogue can
// be asked for it with the key the user just gave.
func Run(ctx context.Context, in io.Reader, out io.Writer, configPath string, preset Answers, now time.Time) (Result, error) {
	r := bufio.NewReader(in)
	w := &session{in: r, out: out, configPath: configPath}

	// The banner and the review are shown only when the wizard is interactive (no preset). A
	// preset means the answers come from a script or a test, and both would be noise - or, for
	// the review, a question a script cannot answer.
	interactive := isPresetEmpty(preset)
	if interactive {
		printBanner(out)
	}

	for {
		s, err := w.collect(ctx, preset)
		if err != nil {
			return Result{}, err
		}
		if interactive {
			ok, err := w.review(ctx, s)
			if err != nil {
				return Result{}, err
			}
			if !ok {
				w.say("")
				printInfo(out, "No problem: let's go through it again. Nothing has been written.")
				continue
			}
		}
		return w.save(s, now)
	}
}

// collect asks every question of one pass, skipping the ones the preset answers.
func (s *session) collect(ctx context.Context, preset Answers) (setup, error) {
	var st setup
	var err error
	s.connectShown = false

	if st.provider, err = s.chooseProvider(ctx, preset.Provider); err != nil {
		return setup{}, err
	}
	if st.baseURL, err = s.chooseEndpoint(ctx, st.provider, preset.BaseURL); err != nil {
		return setup{}, err
	}
	st.keyless = config.IsSelfHostedOllama(config.LLM{Provider: st.provider.ID, BaseURL: st.baseURL})
	if err = s.connect(ctx, &st, preset.APIKey); err != nil {
		return setup{}, err
	}

	// The host chosen above is where the live catalogue is read from.
	var listURL string
	if st.provider.FetchModels {
		listURL = st.baseURL
	}
	if st.model, err = s.chooseModel(ctx, st.provider, preset.Model, listURL, st.key); err != nil {
		return setup{}, err
	}
	if st.anchor, err = s.chooseAnchor(ctx, preset.AnchorCommand, preset.AnchorArgs); err != nil {
		return setup{}, err
	}
	return st, nil
}

// save writes the configuration (and the key, apart from it) and reports what it wrote.
func (s *session) save(st setup, now time.Time) (Result, error) {
	content := renderConfig(configValues{
		provider:      st.provider.ID,
		model:         st.model,
		baseURL:       st.baseURL,
		anchorCommand: st.anchor.command,
		anchorArgs:    st.anchor.args,
		anchorAuto:    st.anchor.auto,
		generated:     now,
	})

	res := Result{ConfigPath: s.configPath, Provider: st.provider, Model: st.model, LoggedIn: st.loggedIn, Keyless: st.keyless}

	// The configuration being replaced is kept beside it. Running the wizard again is how a user
	// changes the model, and a hand-tuned file (a sandbox limit, a schedule) lost to that would be
	// a punishment for using the tool the way it invites.
	if old, err := os.ReadFile(s.configPath); err == nil {
		backup := s.configPath + ".bak"
		if err := writeFileAtomicFn(backup, old); err != nil {
			return Result{}, fmt.Errorf("could not keep a copy of the current configuration: %w", err)
		}
		res.BackupPath = backup
	}

	if err := writeFileAtomicFn(s.configPath, content); err != nil {
		return Result{}, err
	}

	credPath := config.CredentialsPath(s.configPath)
	if st.key != "" {
		// The key goes to its own file with 0600 permissions: the configuration
		// stays shareable, and the secret is never in it.
		if err := writeFileAtomicFn(credPath, []byte(renderCredentials(st.provider.EnvKey, st.key))); err != nil {
			return Result{}, err
		}
		res.CredentialsPath = credPath
	} else if err := removeFile(credPath); err != nil && !os.IsNotExist(err) {
		// A key saved for the PREVIOUS setup would otherwise be read for this one: the loader
		// takes the generic variable for whichever provider the file names, and a key sent to
		// the wrong vendor is a leak. The user was offered to keep it and did not.
		printWarning(s.out, "The key of the previous setup could not be removed from %s: %v", credPath, err)
	}

	printSummary(s.out, res)
	return res, nil
}

// removeFile deletes the stale credentials file. A variable so the failure is testable.
var removeFile = os.Remove

// review shows every answer of the pass and asks whether to save it. It returns false when the
// user wants to go through the questions again.
func (s *session) review(ctx context.Context, st setup) (bool, error) {
	printStep(s.out, stepSave, "Review and save")
	s.say("")
	printField(s.out, "Provider", st.provider.Short)
	if st.baseURL != "" {
		printField(s.out, "Endpoint", st.baseURL)
	}
	printField(s.out, "Sign-in", signInDescription(st))
	printField(s.out, "Model", st.model)
	printField(s.out, "Check", st.anchor.describe())
	file := s.configPath
	if _, err := os.Stat(s.configPath); err == nil {
		file += colDim + "  (replaces the current one; a copy is kept as .bak)" + colReset
	}
	printField(s.out, "File", file)

	for attempt := 0; attempt < 3; attempt++ {
		answer, err := s.ask(ctx, "Save this setup? [Y/n]:")
		if err != nil {
			return false, err
		}
		switch strings.ToLower(answer) {
		case "", "y", "yes":
			return true, nil
		case "n", "no":
			return false, nil
		default:
			s.say("    Answer y to save, or n to change something.")
		}
	}
	return false, fmt.Errorf("no answer to the review after three attempts")
}

// signInDescription says how the setup reaches the provider, for the review.
func signInDescription(st setup) string {
	switch {
	case st.provider.Login != "":
		return "your own login (" + st.provider.Login + ")"
	case st.loggedIn:
		return "your account (stored login, renewed automatically)"
	case st.keyless:
		return "none needed"
	case st.keptKey:
		return "API key " + maskKey(st.key) + " (the one already saved)"
	case st.key != "":
		return "API key " + maskKey(st.key)
	default:
		return colYellow + "no key yet: add it later" + colReset
	}
}

// session holds the conversation state.
type session struct {
	in         *bufio.Reader
	out        io.Writer
	configPath string
	// connectShown records that the "Connect" step's header was written in this pass, so the
	// endpoint and the sign-in questions share one header instead of each opening a step.
	connectShown bool
}

func (s *session) say(format string, args ...any) {
	fmt.Fprintf(s.out, format+"\n", args...)
}

// ask reads one line. EOF and a lone "q" cancel the wizard; so does a cancelled
// context, which is how Ctrl+C during -init is handled.
func (s *session) ask(ctx context.Context, prompt string) (string, error) {
	s.say("")
	printPrompt(s.out, prompt)
	line, err := s.readLine(ctx)
	if err != nil {
		if errors.Is(err, context.Canceled) {
			return "", ErrCancelled
		}
		if errors.Is(err, io.EOF) {
			return "", ErrCancelled
		}
		return "", err
	}
	text := strings.TrimSpace(line)
	if strings.EqualFold(text, "q") || strings.EqualFold(text, "quit") {
		return "", ErrCancelled
	}
	return text, nil
}

// readLine reads a single line from the input. It returns when a line is
// available, the context is cancelled, or the input reaches EOF.
func (s *session) readLine(ctx context.Context) (string, error) {
	ch := make(chan lineResult, 1)
	go func() {
		l, err := s.in.ReadString('\n')
		ch <- lineResult{line: l, err: err}
	}()
	select {
	case r := <-ch:
		return r.line, r.err
	case <-ctx.Done():
		return "", ctx.Err()
	}
}

type lineResult struct {
	line string
	err  error
}

// pick reads a choice from a numbered menu of n options: Enter takes the first and a number takes
// that option. It gives up after three answers it cannot use, saying what it was asking for.
func (s *session) pick(ctx context.Context, prompt string, n int, what string) (int, error) {
	for attempt := 0; attempt < 3; attempt++ {
		answer, err := s.ask(ctx, prompt)
		if err != nil {
			return 0, err
		}
		if answer == "" {
			return 0, nil
		}
		if v, err := strconv.Atoi(answer); err == nil && v >= 1 && v <= n {
			return v - 1, nil
		}
		s.say("    Choose a number from 1 to %d.", n)
	}
	return 0, fmt.Errorf("no valid %s after three attempts", what)
}

func (s *session) chooseProvider(ctx context.Context, preset string) (Provider, error) {
	providers := Providers()

	if preset != "" {
		p, ok := Lookup(preset)
		if !ok {
			return Provider{}, fmt.Errorf("unknown provider %q (use %s)", preset, Names())
		}
		return p, nil
	}

	printStep(s.out, stepProvider, "Which AI provider do you want to use?")
	printInfo(s.out, "Pick the one you already have an account with. You can change it later.")
	s.say("")
	printProviders(s.out, providers)

	for attempt := 0; attempt < 3; attempt++ {
		answer, err := s.ask(ctx, "Provider [1]:")
		if err != nil {
			return Provider{}, err
		}
		if answer == "" {
			return providers[0], nil
		}
		// A number, or the name itself.
		if n, err := strconv.Atoi(answer); err == nil {
			if n >= 1 && n <= len(providers) {
				return providers[n-1], nil
			}
			s.say("    There is no option %d.", n)
			continue
		}
		if p, ok := Lookup(answer); ok {
			return p, nil
		}
		s.say("    I do not know %q. Use a number, or one of: %s.", answer, Names())
	}
	return Provider{}, fmt.Errorf("no valid provider after three attempts")
}

// connectHeader opens the "Connect" step once per pass, whichever of its questions comes first.
func (s *session) connectHeader(p Provider) {
	if s.connectShown {
		return
	}
	s.connectShown = true
	printStep(s.out, stepConnect, "Connect to "+p.Short)
}

// chooseEndpoint decides the API endpoint: asked where the protocol is spoken by many hosts
// (OpenAI-compatible) or where the server may be the user's own (Ollama), and the provider's own
// endpoint otherwise.
func (s *session) chooseEndpoint(ctx context.Context, p Provider, preset string) (string, error) {
	switch {
	case preset != "":
		return strings.TrimRight(preset, "/"), nil
	case strings.EqualFold(p.ID, "ollama"):
		// Ollama is either the user's own server (no key) or Ollama Cloud (a key): the host
		// decides everything that follows, so it is asked first.
		return s.chooseOllamaHost(ctx, p)
	case p.Login != "":
		// A login that names its own endpoint (the claude CLI): there is nothing to configure.
		return "", nil
	case p.AskBaseURL:
		return s.chooseBaseURL(ctx, p)
	default:
		return p.DefaultBaseURL, nil
	}
}

// connect settles how the setup authenticates: a login of the provider's own tool, nothing (a
// server of the user's own), a stored login, or a key.
func (s *session) connect(ctx context.Context, st *setup, presetKey string) error {
	p := st.provider
	switch {
	case presetKey != "":
		st.key = presetKey
		return nil
	case p.Login != "":
		s.connectHeader(p)
		printInfo(s.out, "No key is needed: motita uses your own %s login.", p.Short)
		printInfo(s.out, "If you have not logged in yet, run this after the setup:")
		printCommand(s.out, p.Login)
		return nil
	case st.keyless:
		printInfo(s.out, "No key is needed for an Ollama server of your own.")
		return nil
	}
	key, loggedIn, kept, err := s.askAPIKey(ctx, p)
	st.key, st.loggedIn, st.keptKey = key, loggedIn, kept
	return err
}

// storedKey is the key the credentials file beside the configuration already holds for this
// provider, so a user who runs the wizard again to change the model is not asked to paste it
// again.
//
// The generic variable is only offered back to the provider that wrote it (OpenAI and Codex share
// it, being the same account): it would otherwise offer one vendor's key to another.
func (s *session) storedKey(p Provider) string {
	if p.EnvKey == "" {
		return ""
	}
	key := config.StoredCredentials(s.configPath)[p.EnvKey]
	if key == "" || p.EnvKey != "MOTITA_LLM_API_KEY" {
		return key
	}
	old, err := config.LoadWithoutKey(s.configPath)
	if err != nil {
		return ""
	}
	switch strings.ToLower(old.LLM.Provider) {
	case "openai", "codex":
		return key
	}
	return ""
}

// authOption is one way to sign in that the wizard can offer.
type authOption struct {
	label, note string
	// signIn marks the account login: its failure is something to try again (a closed browser,
	// an expired code), while a failure of the others is a failure of the terminal itself.
	signIn bool
	run    func() (key string, loggedIn, kept bool, err error)
}

// askAPIKey asks how to authenticate and returns the key (empty for a login), whether a login
// was stored, and whether the key is the one already saved.
//
// The options are built from what is possible right now: keeping a login or a key that is already
// there comes first, because re-running the wizard to change one thing should not mean signing in
// again; then the account login, for the providers that have one; then pasting a key.
func (s *session) askAPIKey(ctx context.Context, p Provider) (string, bool, bool, error) {
	s.connectHeader(p)
	var opts []authOption
	if p.SupportsDirectAuth && oauth.HasCredential(authDir(), p.ID) {
		opts = append(opts, authOption{label: "Keep your current sign-in", note: "already signed in on this computer",
			run: func() (string, bool, bool, error) { return "", true, false, nil }})
	}
	if stored := s.storedKey(p); stored != "" {
		opts = append(opts, authOption{label: "Keep the saved API key", note: maskKey(stored),
			run: func() (string, bool, bool, error) { return stored, false, true, nil }})
	}
	if p.SupportsDirectAuth {
		opts = append(opts, authOption{label: "Sign in with your account", note: "opens a link in your browser", signIn: true,
			run: func() (string, bool, bool, error) {
				key, err := directAuthRunner(ctx, s.out, s.in, p)
				return key, err == nil && key == "", false, err
			}})
	}
	opts = append(opts, authOption{label: "Paste an API key", note: "from " + p.ConsoleURL,
		run: func() (string, bool, bool, error) {
			key, err := s.askForAPIKey(ctx, p)
			return key, false, false, err
		}})

	// One way in is not a choice: the key is asked for directly.
	if len(opts) == 1 {
		return opts[0].run()
	}

	s.say("")
	s.say("%sHow do you want to sign in?", indent)
	labels := make([]string, len(opts))
	for i, o := range opts {
		labels[i] = o.label
	}
	w := labelWidth(labels)
	for i, o := range opts {
		printOption(s.out, i+1, o.label, w, o.note)
	}

	// A failed sign-in is not the end of the setup: the browser may have been closed, the code
	// may have expired. The user is told what happened and asked again, with the other ways in
	// still on the menu.
	for attempt := 0; attempt < 3; attempt++ {
		i, err := s.pick(ctx, "Sign-in [1]:", len(opts), "authentication choice")
		if err != nil {
			return "", false, false, err
		}
		key, loggedIn, kept, err := opts[i].run()
		if err == nil {
			return key, loggedIn, kept, nil
		}
		if ctx.Err() != nil {
			return "", false, false, ErrCancelled
		}
		if !opts[i].signIn {
			return "", false, false, err
		}
		s.say("")
		printWarning(s.out, "The sign-in did not finish: %v", err)
		printInfo(s.out, "Try again, or choose another option.")
	}
	return "", false, false, fmt.Errorf("no sign-in after three attempts")
}

// askForAPIKey is the paste-the-key path.
func (s *session) askForAPIKey(ctx context.Context, p Provider) (string, error) {
	s.say("")
	s.say("%sYour API key. Get one at:", indent)
	printLink(s.out, p.ConsoleURL)
	printInfo(s.out, "It is saved apart from the settings, in a private file (%s).", credentialsProtection())
	return s.ask(ctx, "Paste the key, or press Enter to add it later:")
}

// directAuthRunner runs the provider-specific OAuth/device-code flow. It shows the
// user a URL and a code, waits for them to authorise, and stores the login.
//
// It is a package variable so tests can replace it without a network.
var directAuthRunner = runDirectAuth

// chooseModel offers the models of the chosen provider and accepts any other name
// typed by hand: the catalogue is a convenience, not a limitation, and models are
// released faster than any list can follow. For providers that publish their own
// catalogue (Ollama), the list is fetched live.
func (s *session) chooseModel(ctx context.Context, p Provider, preset, listURL, apiKey string) (string, error) {
	if preset != "" {
		return preset, nil
	}

	printStep(s.out, stepModel, "Which model should motita use?")

	models := p.Models
	if p.FetchModels {
		url := listURL
		if url == "" {
			url = p.DefaultBaseURL
		}
		fetched, err := modelLister(ctx, url, apiKey)
		switch {
		case err != nil:
			// Never leave the user staring at an empty menu: fall back to the
			// known catalogue and say plainly that it is a fallback.
			printWarning(s.out, "Could not read the models available at %s: %s", url, unreachable(err))
			printInfo(s.out, "Showing the built-in list instead; any model id can be typed by hand.")
		case len(fetched) == 0:
			printWarning(s.out, "The catalogue at %s is empty; showing the built-in list.", url)
		default:
			printInfo(s.out, "These are the models available at %s.", url)
			models = make([]Model, 0, len(fetched))
			for _, id := range fetched {
				models = append(models, Model{ID: id, Label: id})
			}
		}
	}

	s.say("")
	if len(models) == 0 {
		printInfo(s.out, "no models were offered; type the model id you want")
	}
	printModels(s.out, models)

	for attempt := 0; attempt < 3; attempt++ {
		prompt := "Model [1, or type any model id]:"
		if len(models) == 0 {
			prompt = "Model id:"
		}
		answer, err := s.ask(ctx, prompt)
		if err != nil {
			return "", err
		}
		if answer == "" {
			if len(models) == 0 {
				s.say("    You must type a model id.")
				continue
			}
			return models[0].ID, nil
		}
		if n, err := strconv.Atoi(answer); err == nil {
			if n >= 1 && n <= len(models) {
				return models[n-1].ID, nil
			}
			s.say("    There is no option %d.", n)
			continue
		}
		return answer, nil // a model id typed by hand
	}
	return "", fmt.Errorf("no valid model after three attempts")
}

// unreachable says why a catalogue could not be read in the words a user acts on. A server that
// refuses the connection is, nearly always, an Ollama that has not been started yet: the raw
// "dial tcp 127.0.0.1:11434: connect: connection refused" is accurate and says neither.
func unreachable(err error) string {
	if strings.Contains(err.Error(), "connection refused") {
		return "nothing is answering there yet (for Ollama on this computer, start it with `ollama serve`)"
	}
	return err.Error()
}

// chooseAnchor asks what decides PASS. This is the question that makes the agent
// what it is: without a validator it refuses to run, so the wizard either takes a
// real command or records the explicit "no check yet".
func (s *session) chooseAnchor(ctx context.Context, preset string, presetArgs []string) (anchorChoice, error) {
	if preset != "" {
		return anchorChoice{command: preset, args: presetArgs}, nil
	}

	printStep(s.out, stepCheck, "How should motita check that a task is really done?")
	printInfo(s.out, "motita never takes the model's word for it: only this check can declare a task done.")
	s.say("")
	labels := []string{"Detect it from the project", "A command I choose", "No check for now"}
	w := labelWidth(labels)
	printOption(s.out, 1, labels[0], w, "recommended: make test, go test, npm test, cargo test...")
	printOption(s.out, 2, labels[1], w, "for example: make test")
	printOption(s.out, 3, labels[2], w, "just trying it out: tasks are reported as unverified")

	for attempt := 0; attempt < 3; attempt++ {
		answer, err := s.ask(ctx, "Check [1]:")
		if err != nil {
			return anchorChoice{}, err
		}
		switch answer {
		case "", "1":
			// The detected gate: nothing is hardcoded, because the gate comes from
			// the project at run time. A project that wants to be explicit writes
			// .motita/anchor.
			return anchorChoice{auto: true}, nil
		case "2":
			cmd, err := s.ask(ctx, "Command to run as the check [make test]:")
			if err != nil {
				return anchorChoice{}, err
			}
			if cmd == "" {
				return anchorChoice{command: "make", args: []string{"test"}}, nil
			}
			parts := strings.Fields(cmd)
			return anchorChoice{command: parts[0], args: parts[1:]}, nil
		case "3":
			// "Always pass" is recorded as NO anchor, not as a command that cannot fail.
			// The agent then refuses to declare PASS and says why, which is the honest
			// shape of "I am just trying this out" — see render.go for the measurement
			// that made this a defect rather than a preference.
			return anchorChoice{}, nil
		default:
			s.say("    Choose 1, 2 or 3.")
		}
	}
	return anchorChoice{}, fmt.Errorf("no valid check after three attempts")
}

// anchorChoice is what the wizard decided about the validator.
//
// Three outcomes, and they are not variations of one: a gate DISCOVERED from the
// project, a gate NAMED by the user, and NO gate at all. Modelling them as one makes
// "no gate" and "a gate I have not decided yet" the same value, which is exactly the
// confusion that let `command: "true"` be written as an anchor.
type anchorChoice struct {
	auto    bool
	command string
	args    []string
}

// describe says what the check is, for the review.
func (a anchorChoice) describe() string {
	switch {
	case a.auto:
		return "detected from each project"
	case a.command != "":
		return strings.Join(append([]string{a.command}, a.args...), " ")
	default:
		return colYellow + "none yet: tasks are reported as unverified" + colReset
	}
}

// chooseOllamaHost asks whether Ollama runs on the user's own machine or is
// Ollama Cloud. Only the cloud needs a key: a local server takes none, and the
// wizard used to demand one anyway, so a local Ollama could not be configured.
func (s *session) chooseOllamaHost(ctx context.Context, p Provider) (string, error) {
	s.connectHeader(p)
	s.say("%sWhere does Ollama run?", indent)
	labels := []string{"On this computer or my network", "Ollama Cloud", "Another address"}
	w := labelWidth(labels)
	printOption(s.out, 1, labels[0], w, "http://localhost:11434 · free, no key")
	printOption(s.out, 2, labels[1], w, "https://ollama.com · needs a key")
	printOption(s.out, 3, labels[2], w, "a URL ending in /v1")
	for attempt := 0; attempt < 3; attempt++ {
		answer, err := s.ask(ctx, "Ollama [1]:")
		if err != nil {
			return "", err
		}
		switch answer {
		case "", "1":
			return "http://localhost:11434/v1", nil
		case "2":
			return "https://ollama.com/v1", nil
		case "3":
			u, err := s.ask(ctx, "Ollama base URL (ending in /v1):")
			if err != nil {
				return "", err
			}
			if strings.HasPrefix(u, "http://") || strings.HasPrefix(u, "https://") {
				return strings.TrimRight(u, "/"), nil
			}
			s.say("    A URL must start with http:// or https://.")
		default:
			s.say("    Choose 1, 2 or 3.")
		}
	}
	return "", fmt.Errorf("no valid Ollama host after three attempts")
}

// chooseBaseURL asks for the API endpoint. OpenAI-compatible providers need this
// because the same protocol is spoken by many hosts (OpenAI, Groq, OpenRouter,
// DeepSeek, LM Studio, etc.).
func (s *session) chooseBaseURL(ctx context.Context, p Provider) (string, error) {
	s.connectHeader(p)
	s.say("%sPress Enter to use %s itself, or paste the address of any compatible service:", indent, p.Short)
	for _, u := range []string{"https://api.groq.com/openai/v1", "https://openrouter.ai/api/v1", "https://api.deepseek.com/v1"} {
		printInfo(s.out, "  %s", u)
	}

	defaultURL := p.DefaultBaseURL
	for attempt := 0; attempt < 3; attempt++ {
		answer, err := s.ask(ctx, fmt.Sprintf("API base URL [%s]:", defaultURL))
		if err != nil {
			return "", err
		}
		if answer == "" {
			return defaultURL, nil
		}
		u := strings.ToLower(answer)
		if strings.HasPrefix(u, "http://") || strings.HasPrefix(u, "https://") {
			return strings.TrimRight(answer, "/"), nil
		}
		s.say("    A URL must start with http:// or https://.")
	}
	return "", fmt.Errorf("no valid API base URL after three attempts")
}

// credentialsPathFor returns the credentials file that goes next to a
// configuration: same directory, same base name, .env extension. It is the
// loader's own definition, so the file the wizard writes is the one it reads.
func credentialsPathFor(configPath string) string {
	return config.CredentialsPath(configPath)
}

// renderCredentials writes a file the user can source. It is a shell script so
// that sourcing it is the whole setup elsewhere; motita itself reads it on its own.
func renderCredentials(envKey, key string) string {
	return fmt.Sprintf(
		"# Generated by motita -init. This file holds a secret: keep it\n"+
			"# out of the repository and out of any backup you share.\n"+
			"# motita reads it automatically. Other tools can load it with:  source %s\n"+
			"export %s=%s\n",
		"<this file>", envKey, shellQuote(key))
}

// shellQuote makes a value safe to put in an export line, so a key containing a
// space or a quote cannot break the file.
func shellQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}

// The filesystem operations are indirected so their failure branches can be
// tested: filling a disk or winning a race inside a unit test is not reasonable,
// and an untested error path is where a half-written configuration would hide.
// Production uses the real functions.
var (
	createTemp = os.CreateTemp
	closeFile  = func(f *os.File) error { return f.Close() }
	chmodFile  = os.Chmod
	renameFile = os.Rename
)

// writeFileAtomicFn is the function used to write files atomically. It is a
// variable so tests can inject a failure to cover the error paths in Run.
var writeFileAtomicFn = writeFileAtomic

// writeFileAtomic writes to a temporary file in the same directory and renames it,
// so the destination is never half-written, and creates the directory if needed.
func writeFileAtomic(path string, content []byte) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("could not create %s: %w", dir, err)
	}
	tmp, err := createTemp(dir, ".motita-*.tmp")
	if err != nil {
		return fmt.Errorf("could not write in %s: %w", dir, err)
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName) // no-op after a successful rename

	if _, err := tmp.Write(content); err != nil {
		tmp.Close()
		return fmt.Errorf("could not write %s: %w", tmpName, err)
	}
	if err := closeFile(tmp); err != nil {
		return fmt.Errorf("could not close %s: %w", tmpName, err)
	}
	if err := chmodFile(tmpName, 0o600); err != nil {
		return fmt.Errorf("could not set the permissions of %s: %w", tmpName, err)
	}
	if err := renameFile(tmpName, path); err != nil {
		return fmt.Errorf("could not move %s into place: %w", tmpName, err)
	}
	return nil
}
