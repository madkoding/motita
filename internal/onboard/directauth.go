package onboard

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/madkoding/motita/internal/config"
	"github.com/madkoding/motita/internal/oauth"
)

// runDirectAuth dispatches to the provider-specific login. It shows the user a
// URL (and a code), waits for the authorisation, and STORES the resulting
// credential (see oauth.SaveCredential) so the LLM client can renew it.
//
// It returns an empty key: a login is not a key, and writing a token that
// expires within the hour into a file the user sources once is what used to
// leave every login broken by the next day.
func runDirectAuth(ctx context.Context, out io.Writer, in *bufio.Reader, p Provider) (string, error) {
	var (
		cred oauth.Credential
		err  error
	)
	switch strings.ToLower(p.ID) {
	case "copilot":
		cred, err = copilotDirectAuth(ctx, out)
	case "codex":
		cred, err = codexDirectAuth(ctx, out, in)
	case "qwen":
		cred, err = qwenDirectAuth(ctx, out)
	case "gemini":
		cred, err = geminiDirectAuth(ctx, out, in)
	default:
		return "", fmt.Errorf("direct login is not supported for %s", p.ID)
	}
	if err != nil {
		return "", err
	}
	dir := authDir()
	if err := oauth.SaveCredential(dir, cred); err != nil {
		return "", fmt.Errorf("the login worked but could not be saved: %w", err)
	}
	printInfo(out, "The login is stored in %s and renewed automatically.", oauth.CredentialPath(dir, cred.Provider))
	return "", nil
}

// authDir is where logins are stored. A variable so tests use a temporary one.
var authDir = config.AuthDir

// authHTTP is the HTTP client of the login flows; nil means oauth's default.
var authHTTP oauth.HTTPClient

// copilotDirectAuth runs the GitHub device flow + Copilot token exchange.
func copilotDirectAuth(ctx context.Context, out io.Writer) (oauth.Credential, error) {
	printSection(out, "Connect to GitHub Copilot")
	printInfo(out, "You'll open a link in your browser and enter a code.")
	printInfo(out, "An active Copilot subscription is required.")
	fmt.Fprintln(out)

	result, err := oauth.CopilotFullFlow(ctx, authHTTP, oauth.CopilotConfig{ClientID: oauth.CopilotClientID},
		func(dc oauth.DeviceCode) { printDeviceInfo(out, dc) },
		nil,
	)
	if err != nil {
		return oauth.Credential{}, fmt.Errorf("copilot authentication failed: %w", err)
	}
	if result.CopilotToken.Token == "" {
		return oauth.Credential{}, errors.New("GitHub returned no Copilot token: the account has no active Copilot subscription")
	}
	printSuccess(out, "Connected to GitHub Copilot!")
	return result.Credential(), nil
}

// codexDirectAuth runs "Sign in with ChatGPT".
func codexDirectAuth(ctx context.Context, out io.Writer, in *bufio.Reader) (oauth.Credential, error) {
	printSection(out, "Sign in with ChatGPT (Codex)")
	printInfo(out, "A ChatGPT plan that includes Codex (Plus, Pro, Business, Edu, Enterprise) is required.")
	fmt.Fprintln(out)

	pkce, err := oauth.NewPKCE()
	if err != nil {
		return oauth.Credential{}, err
	}
	lb, lerr := startLoopback(oauth.CodexListenAddr, "localhost", "/auth/callback")
	if lerr == nil {
		defer lb.Close()
	}
	printInfo(out, "Open this URL in your browser:")
	printLink(out, oauth.CodexAuthorizeURL(pkce, oauth.CodexRedirectURI))
	fmt.Fprintln(out)
	printPasteHint(out, lerr)

	code, err := waitForCode(ctx, out, in, lb, pkce.State)
	if err != nil {
		return oauth.Credential{}, err
	}
	cred, err := oauth.CodexExchangeCode(ctx, authHTTP, code, oauth.CodexRedirectURI, pkce)
	if err != nil {
		return oauth.Credential{}, fmt.Errorf("could not complete the ChatGPT login: %w", err)
	}
	printSuccess(out, "Signed in with ChatGPT!")
	return cred, nil
}

// qwenDirectAuth runs the qwen.ai device login.
func qwenDirectAuth(ctx context.Context, out io.Writer) (oauth.Credential, error) {
	printSection(out, "Connect to Qwen (qwen.ai account)")
	printInfo(out, "You'll open a link in your browser and approve this device.")
	fmt.Fprintln(out)

	pkce, err := oauth.NewPKCE()
	if err != nil {
		return oauth.Credential{}, err
	}
	dc, err := oauth.QwenRequestDeviceCode(ctx, authHTTP, pkce)
	if err != nil {
		return oauth.Credential{}, fmt.Errorf("could not start the Qwen login: %w", err)
	}
	printDeviceInfo(out, dc)
	cred, err := oauth.PollDevice(ctx, dc.Interval, sleepFor, func() (oauth.Credential, error) {
		return oauth.QwenPollToken(ctx, authHTTP, dc, pkce)
	})
	if err != nil {
		return oauth.Credential{}, fmt.Errorf("qwen authentication failed: %w", err)
	}
	printSuccess(out, "Connected to Qwen!")
	return cred, nil
}

// geminiDirectAuth logs in to Google with a client the user owns: their own
// OAuth client (MOTITA_GEMINI_CLIENT_ID/SECRET), or gcloud's Application Default
// Credentials.
func geminiDirectAuth(ctx context.Context, out io.Writer, in *bufio.Reader) (oauth.Credential, error) {
	printSection(out, "Connect to Google Gemini")

	project, err := promptLine(ctx, out, in, "Google Cloud project that pays for the calls (Enter = the one gcloud set):")
	if err != nil {
		return oauth.Credential{}, err
	}

	client, haveClient := oauth.GeminiClientFromEnv()
	if !haveClient {
		path := oauth.ADCPath(config.HomeDir())
		cred, err := oauth.GeminiFromADC(path, project)
		if err != nil {
			printInfo(out, "Gemini needs an OAuth client of your own. Either:")
			printInfo(out, "  a) run: gcloud auth application-default login --client-id-file=client_secret.json \\")
			printInfo(out, "       --scopes='%s'", strings.ReplaceAll(oauth.GeminiScopes, " ", ","))
			printInfo(out, "  b) or export MOTITA_GEMINI_CLIENT_ID and MOTITA_GEMINI_CLIENT_SECRET (a Desktop OAuth client)")
			printInfo(out, "and run the wizard again. Or paste an API key from https://aistudio.google.com/apikey instead.")
			return oauth.Credential{}, err
		}
		if cred.ProjectID == "" {
			return oauth.Credential{}, errors.New("no Google Cloud project is known: type one, or run `gcloud auth application-default set-quota-project <project>`")
		}
		// Renew once now, so a credential that cannot reach Gemini fails here and
		// not on the first task.
		cred, err = oauth.GeminiRefresh(ctx, authHTTP, cred)
		if err != nil {
			return oauth.Credential{}, fmt.Errorf("the gcloud credentials could not be renewed: %w", err)
		}
		printSuccess(out, "Connected to Google Gemini with your gcloud login!")
		return cred, nil
	}
	if project == "" {
		return oauth.Credential{}, errors.New("a Google Cloud project is required with your own OAuth client")
	}

	pkce, err := oauth.NewPKCE()
	if err != nil {
		return oauth.Credential{}, err
	}
	lb, lerr := startLoopback("127.0.0.1:0", "127.0.0.1", "/")
	if lerr != nil {
		return oauth.Credential{}, lerr
	}
	defer lb.Close()
	printInfo(out, "Open this URL in your browser:")
	printLink(out, oauth.GeminiAuthorizeURL(client, pkce, lb.RedirectURI))
	fmt.Fprintln(out)
	printPasteHint(out, nil)

	code, err := waitForCode(ctx, out, in, lb, pkce.State)
	if err != nil {
		return oauth.Credential{}, err
	}
	cred, err := oauth.GeminiExchangeCode(ctx, authHTTP, client, code, lb.RedirectURI, pkce, project)
	if err != nil {
		return oauth.Credential{}, fmt.Errorf("could not complete the Google login: %w", err)
	}
	printSuccess(out, "Connected to Google Gemini!")
	return cred, nil
}

// startLoopback is indirected so tests can run without binding a port.
var startLoopback = oauth.StartLoopback

func printPasteHint(out io.Writer, listenErr error) {
	if listenErr != nil {
		printInfo(out, "(%v)", listenErr)
		printInfo(out, "After approving, the browser lands on a page that does not load: copy its URL and paste it here.")
		return
	}
	printInfo(out, "Waiting for the browser... If it runs on another machine, paste here the URL it ends on.")
}

// waitForCode waits for the browser callback or a pasted URL/code. When the
// browser wins, the reader started for the paste is still waiting for a line, so
// the user is asked for one Enter: a reader left behind would swallow the answer
// to whatever is asked next.
func waitForCode(ctx context.Context, out io.Writer, in *bufio.Reader, lb *oauth.Loopback, state string) (string, error) {
	lines := make(chan string, 1)
	go func() {
		line, err := in.ReadString('\n')
		if err != nil && line == "" {
			close(lines)
			return
		}
		lines <- line
	}()
	code, pasted, err := lb.Wait(ctx, state, lines)
	if err == nil && !pasted {
		fmt.Fprintf(out, "%sLogged in in the browser. Press Enter to continue.%s ", colCyan, colReset)
		select {
		case <-lines:
		case <-ctx.Done():
		}
		fmt.Fprintln(out)
	}
	return code, err
}

// promptLine asks one question inside a login flow.
func promptLine(ctx context.Context, out io.Writer, in *bufio.Reader, prompt string) (string, error) {
	fmt.Fprintf(out, "%s%s%s ", colCyan, prompt, colReset)
	line, err := readLine(ctx, in)
	if err != nil && !(errors.Is(err, io.EOF) && line != "") {
		if errors.Is(err, io.EOF) {
			return "", nil
		}
		return "", err
	}
	return strings.TrimSpace(line), nil
}

// sleepFor is the pause used between poll attempts. It is a variable so tests
// can replace it with a no-op; the real flow sleeps for the interval the server
// specifies.
var sleepFor = time.After

// printDeviceInfo shows the device code information to the user.
func printDeviceInfo(out io.Writer, dc oauth.DeviceCode) {
	fmt.Fprintf(out, "  %sOpen this URL:%s %s%s%s\n", colBold, colReset, colCyan, dc.VerificationURL, colReset)
	fmt.Fprintf(out, "  %sEnter this code:%s %s%s%s\n", colBold, colReset, colYellow, dc.UserCode, colReset)
	fmt.Fprintln(out)
	fmt.Fprintf(out, "  %sWaiting for you to authorise...%s\n", colDim, colReset)
	fmt.Fprintln(out)
}

// readLine reads a single line from the input, respecting context cancellation.
func readLine(ctx context.Context, in *bufio.Reader) (string, error) {
	ch := make(chan struct {
		line string
		err  error
	}, 1)
	go func() {
		line, err := in.ReadString('\n')
		ch <- struct {
			line string
			err  error
		}{line, err}
	}()
	select {
	case r := <-ch:
		return r.line, r.err
	case <-ctx.Done():
		return "", ctx.Err()
	}
}
