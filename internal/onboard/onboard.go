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

	"github.com/madkoding/motita/internal/llm"
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
	Provider        Provider
	Model           string
}

// Run interviews the user, writes a working configuration and returns what it
// did. It reads answers from in and writes the conversation to out, so the whole
// flow can be tested without a terminal. Nothing is written if the user cancels:
// the file appears only once every answer is known (see writeFileAtomic).
// modelLister is the function the wizard uses to fetch models from hosts that
// publish them. It is a package-level variable so tests can replace it with a
// stub that does not hit the network.
var modelLister = listOllamaModels

// listOllamaModels is the real implementation.
func listOllamaModels(ctx context.Context, baseURL, apiKey string) ([]string, error) {
	return llm.ListOllamaModels(ctx, baseURL, apiKey)
}

// isPresetEmpty reports whether the preset has no answers set at all, which is
// the interactive case where the banner should be shown.
func isPresetEmpty(a Answers) bool {
	return a.Provider == "" && a.Model == "" && a.BaseURL == "" &&
		a.AnchorCommand == "" && len(a.AnchorArgs) == 0 && a.APIKey == ""
}

func Run(ctx context.Context, in io.Reader, out io.Writer, configPath string, preset Answers, now time.Time) (Result, error) {
	r := bufio.NewReader(in)
	w := &session{in: r, out: out}

	// The banner is shown only when the wizard is interactive (no preset). A
	// preset means the answers come from a script or a test, and the colour art
	// would be noise. Answers carries a slice, so the comparison is field by
	// field rather than with ==.
	if isPresetEmpty(preset) {
		printBanner(out)
	}

	provider, err := w.chooseProvider(ctx, preset.Provider)
	if err != nil {
		return Result{}, err
	}

	// Providers that expose their own catalogue (Ollama Cloud) need the key
	// before we can ask for the model, and their base URL is fixed.
	key := preset.APIKey
	if provider.FetchModels {
		if key == "" {
			key, err = w.askAPIKey(ctx, provider)
			if err != nil {
				return Result{}, err
			}
		}
	}

	var baseURL string
	var listURL string
	if provider.FetchModels {
		baseURL = provider.DefaultBaseURL
		if preset.BaseURL != "" {
			baseURL = preset.BaseURL
		}
		listURL = baseURL
	}

	model, err := w.chooseModel(ctx, provider, preset.Model, listURL, key)
	if err != nil {
		return Result{}, err
	}

	anchorCommand, anchorArgs, err := w.chooseAnchor(ctx, preset.AnchorCommand, preset.AnchorArgs)
	if err != nil {
		return Result{}, err
	}

	if !provider.FetchModels && !provider.NoKey {
		baseURL, err = w.chooseBaseURL(ctx, provider, preset.BaseURL)
		if err != nil {
			return Result{}, err
		}
	}

	if key == "" && !provider.NoKey {
		key, err = w.askAPIKey(ctx, provider)
		if err != nil {
			return Result{}, err
		}
	}

	config := renderConfig(configValues{
		provider:      provider.ID,
		model:         model,
		baseURL:       baseURL,
		anchorCommand: anchorCommand,
		anchorArgs:    anchorArgs,
		generated:     now,
	})

	if err := writeFileAtomicFn(configPath, config); err != nil {
		return Result{}, err
	}

	res := Result{ConfigPath: configPath, Provider: provider, Model: model}

	if key != "" {
		// The key goes to its own file with 0600 permissions: the configuration
		// stays shareable, and the secret is never in it.
		credPath := credentialsPathFor(configPath)
		if err := writeFileAtomicFn(credPath, []byte(renderCredentials(provider.EnvKey, key))); err != nil {
			return Result{}, err
		}
		res.CredentialsPath = credPath
	}

	w.summary(res)
	return res, nil
}

// session holds the conversation state.
type session struct {
	in  *bufio.Reader
	out io.Writer
}

func (s *session) say(format string, args ...any) {
	fmt.Fprintf(s.out, format+"\n", args...)
}

// sayRaw writes a string without a trailing newline, for prompts that read a
// line from the user.
func (s *session) sayRaw(format string, args ...any) {
	fmt.Fprintf(s.out, format, args...)
}

// ask reads one line. EOF and a lone "q" cancel the wizard; so does a cancelled
// context, which is how Ctrl+C during -init is handled.
func (s *session) ask(ctx context.Context, prompt string) (string, error) {
	s.say("")
	fmt.Fprintf(s.out, "%s ", prompt)
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

func (s *session) chooseProvider(ctx context.Context, preset string) (Provider, error) {
	providers := Providers()

	if preset != "" {
		p, ok := Lookup(preset)
		if !ok {
			return Provider{}, fmt.Errorf("unknown provider %q (use %s)", preset, Names())
		}
		return p, nil
	}

	printSection(s.out, "Choose your LLM provider")
	printCancelHint(s.out)
	fmt.Fprintln(s.out)
	for i, p := range providers {
		printProvider(s.out, i+1, p)
	}

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
			s.say("  There is no option %d.", n)
			continue
		}
		if p, ok := Lookup(answer); ok {
			return p, nil
		}
		s.say("  I do not know %q. Use a number, or one of: %s.", answer, Names())
	}
	return Provider{}, fmt.Errorf("no valid provider after three attempts")
}

// chooseModel offers the models of the chosen provider and accepts any other name
// typed by hand: the catalogue is a convenience, not a limitation, and models are
// released faster than any list can follow. For providers that publish their own
// catalogue (Ollama Cloud), the list is fetched live from /api/tags.
func (s *session) chooseModel(ctx context.Context, p Provider, preset, listURL, apiKey string) (string, error) {
	if preset != "" {
		return preset, nil
	}

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
			s.say("  Could not read the live catalogue from %s: %v", url, err)
			s.say("  Showing the built-in list instead; any model id can be typed by hand.")
		case len(fetched) == 0:
			s.say("  The catalogue at %s is empty; showing the built-in list.", url)
		default:
			models = make([]Model, 0, len(fetched))
			for _, id := range fetched {
				models = append(models, Model{ID: id, Label: id})
			}
		}
	}

	s.say("")
	printSection(s.out, fmt.Sprintf("Choose a model from %s", p.Name))
	if len(models) == 0 {
		printInfo(s.out, "no models were offered; type the model id you want")
	}
	for i, m := range models {
		printModel(s.out, i+1, m)
	}

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
				s.say("  You must type a model id.")
				continue
			}
			return models[0].ID, nil
		}
		if n, err := strconv.Atoi(answer); err == nil {
			if n >= 1 && n <= len(models) {
				return models[n-1].ID, nil
			}
			s.say("  There is no option %d.", n)
			continue
		}
		return answer, nil // a model id typed by hand
	}
	return "", fmt.Errorf("no valid model after three attempts")
}

// chooseAnchor asks what decides PASS. This is the question that makes the agent
// what it is: without a validator it refuses to run, so the wizard either takes a
// real command or records the explicit "always pass" escape.
func (s *session) chooseAnchor(ctx context.Context, preset string, presetArgs []string) (string, []string, error) {
	if preset != "" {
		return preset, presetArgs, nil
	}

	s.say("")
	printSection(s.out, "What decides that a task is done?")
	s.say("  %s1.%s A command that must succeed (for example: make test)", colYellow, colReset)
	s.say("  %s2.%s Always pass, while I try the agent out", colYellow, colReset)
	s.say("")
	printInfo(s.out, "The agent never trusts the model: only this check can declare PASS.")

	for attempt := 0; attempt < 3; attempt++ {
		answer, err := s.ask(ctx, "Check [1]:")
		if err != nil {
			return "", nil, err
		}
		switch answer {
		case "", "1":
			cmd, err := s.ask(ctx, "Command to run as the check [make]:")
			if err != nil {
				return "", nil, err
			}
			if cmd == "" {
				cmd = "make"
				return cmd, []string{"test"}, nil
			}
			parts := strings.Fields(cmd)
			return parts[0], parts[1:], nil
		case "2":
			return "true", nil, nil
		default:
			s.say("  Choose 1 or 2.")
		}
	}
	return "", nil, fmt.Errorf("no valid check after three attempts")
}

func (s *session) askAPIKey(ctx context.Context, p Provider) (string, error) {
	s.say("")
	printSection(s.out, "Authentication")

	// When the provider supports direct auth (OAuth/device flow), offer it as
	// the first option: the user opens a URL, enters a code, and no key is
	// pasted. This is the path the user asked for — "connect with a link and a
	// one-time code" — and it is the better UX for the providers that offer it.
	if p.SupportsDirectAuth {
		s.say("  %s1.%s Connect directly (open a link, enter a code)", colYellow, colReset)
		s.say("  %s2.%s Paste an API key", colYellow, colReset)
		s.say("")
		for attempt := 0; attempt < 3; attempt++ {
			answer, err := s.ask(ctx, "How do you want to authenticate? [1]:")
			if err != nil {
				return "", err
			}
			switch answer {
			case "", "1":
				return s.directAuth(ctx, p)
			case "2":
				return s.askForAPIKey(ctx, p)
			default:
				s.say("  Choose 1 or 2.")
			}
		}
		return "", fmt.Errorf("no valid authentication choice after three attempts")
	}

	return s.askForAPIKey(ctx, p)
}

// askForAPIKey is the traditional paste-the-key path, used when the provider
// does not support direct auth or the user chose option 2.
func (s *session) askForAPIKey(ctx context.Context, p Provider) (string, error) {
	s.say("")
	printSection(s.out, "API key")
	// Name only the variables that really work for this provider: telling an
	// Ollama user to export OPENAI_API_KEY would send them to a variable the
	// loader does not consult for it.
	if p.EnvKey == "MOTITA_LLM_API_KEY" {
		printInfo(s.out, "The key is read from %s.", p.EnvKey)
	} else {
		printInfo(s.out, "The key is read from %s, or from MOTITA_LLM_API_KEY.", p.EnvKey)
	}
	printInfo(s.out, "You can get one at:")
	printLink(s.out, p.ConsoleURL)
	key, err := s.ask(ctx, "Paste the key, or press Enter to set it later:")
	if err != nil {
		return "", err
	}
	return key, nil
}

// directAuth runs the provider-specific OAuth/device-code flow. It shows the
// user a URL and a code, waits for them to authorise, and returns the resulting
// token as the "API key" — the rest of the wizard treats it the same way.
//
// The flow is injected via the directAuthRunner package variable so tests can
// replace it without a network.
var directAuthRunner = runDirectAuth

func (s *session) directAuth(ctx context.Context, p Provider) (string, error) {
	return directAuthRunner(ctx, s.out, s.in, p)
}

// chooseBaseURL asks for the API endpoint. OpenAI-compatible providers need this
// because the same protocol is spoken by many hosts (OpenAI, Ollama Cloud, Groq,
// OpenRouter, DeepSeek, etc.).
func (s *session) chooseBaseURL(ctx context.Context, p Provider, preset string) (string, error) {
	if preset != "" {
		return preset, nil
	}

	s.say("")
	printSection(s.out, "API endpoint")
	printInfo(s.out, "Examples of OpenAI-compatible URLs:")
	s.say("  %shttps://api.openai.com/v1%s", colDim, colReset)
	s.say("  %shttps://ollama.com/v1%s", colDim, colReset)
	s.say("  %shttps://api.groq.com/openai/v1%s", colDim, colReset)
	s.say("  %shttps://openrouter.ai/api/v1%s", colDim, colReset)

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
		s.say("  A URL must start with http:// or https://.")
	}
	return "", fmt.Errorf("no valid API base URL after three attempts")
}

func (s *session) summary(res Result) {
	printSummary(s.out, res)
}

// credentialsPathFor returns the credentials file that goes next to a
// configuration: same directory, same base name, .env extension.
func credentialsPathFor(configPath string) string {
	base := strings.TrimSuffix(configPath, filepath.Ext(configPath))
	return base + ".env"
}

// renderCredentials writes a file the user can source. It is a shell script so
// that sourcing it is the whole setup.
func renderCredentials(envKey, key string) string {
	return fmt.Sprintf(
		"# Generated by motita -init. This file holds a secret: keep it\n"+
			"# out of the repository and out of any backup you share.\n"+
			"# Load it in the current shell with:  source %s\n"+
			"export %s=%s\n",
		filepath.Base(envKey), envKey, shellQuote(key))
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
