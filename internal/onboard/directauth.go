package onboard

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/madkoding/motita/internal/oauth"
)

// runDirectAuth dispatches to the provider-specific OAuth/device-code flow. It
// shows the user a URL and a code, waits for authorisation, and returns the
// token string that the wizard writes as the "API key".
//
// For Copilot, the token stored is the GitHub OAuth token (gho_...); the Copilot
// session token is refreshed at runtime by the LLM client. For Gemini, the
// access token is stored. For Anthropic, the PKCE access token is stored.
func runDirectAuth(ctx context.Context, out io.Writer, in *bufio.Reader, p Provider) (string, error) {
	switch strings.ToLower(p.ID) {
	case "gemini":
		return geminiDirectAuth(ctx, out, in)
	case "copilot":
		return copilotDirectAuth(ctx, out, in)
	case "anthropic":
		return anthropicDirectAuth(ctx, out, in)
	default:
		return "", fmt.Errorf("direct auth is not supported for %s", p.ID)
	}
}

// geminiDirectAuth runs the Google OAuth2 device flow.
func geminiDirectAuth(ctx context.Context, out io.Writer, in *bufio.Reader) (string, error) {
	cfg := oauth.GeminiConfig{
		ClientID:     oauth.GeminiDefaultClientID(),
		ClientSecret: oauth.GeminiDefaultClientSecret(),
		Scope:        oauth.GeminiDefaultScope,
	}

	printSection(out, "Connect to Google Gemini")
	printInfo(out, "You'll open a link in your browser and enter a code.")
	fmt.Fprintln(out)

	dc, err := oauth.GeminiRequestDeviceCode(ctx, nil, cfg)
	if err != nil {
		return "", fmt.Errorf("could not start the device flow: %w", err)
	}

	printDeviceInfo(out, dc)

	interval := dc.Interval
	if interval <= 0 {
		interval = 5
	}

	for {
		tok, err := oauth.GeminiPollToken(ctx, nil, cfg, dc)
		if err == nil {
			printSuccess(out, "Connected to Google Gemini!")
			return tok.AccessToken, nil
		}
		if err == oauth.ErrSlowDown {
			interval += 5
			select {
			case <-ctx.Done():
				return "", ctx.Err()
			case <-sleepFor(time.Duration(interval) * time.Second):
			}
			continue
		}
		if err == oauth.ErrAuthorizationPending {
			select {
			case <-ctx.Done():
				return "", ctx.Err()
			case <-sleepFor(time.Duration(interval) * time.Second):
			}
			continue
		}
		return "", err
	}
}

// copilotDirectAuth runs the GitHub device flow + Copilot token exchange.
func copilotDirectAuth(ctx context.Context, out io.Writer, in *bufio.Reader) (string, error) {
	cfg := oauth.CopilotConfig{ClientID: oauth.CopilotClientID}

	printSection(out, "Connect to GitHub Copilot")
	printInfo(out, "You'll open a link in your browser and enter a code.")
	printInfo(out, "An active Copilot subscription is required.")
	fmt.Fprintln(out)

	result, err := oauth.CopilotFullFlow(ctx, nil, cfg,
		func(dc oauth.DeviceCode) {
			printDeviceInfo(out, dc)
		},
		nil,
	)
	if err != nil {
		return "", fmt.Errorf("copilot authentication failed: %w", err)
	}

	printSuccess(out, "Connected to GitHub Copilot!")
	printInfo(out, "Your Copilot subscription is now linked.")
	return result.GitHubToken, nil
}

// anthropicDirectAuth runs the Anthropic PKCE flow.
func anthropicDirectAuth(ctx context.Context, out io.Writer, in *bufio.Reader) (string, error) {
	cfg := oauth.AnthropicConfig{ClientID: oauth.AnthropicClientID}

	printSection(out, "Connect to Anthropic (Claude)")
	printInfo(out, "You'll open a link in your browser, log in to Anthropic,")
	printInfo(out, "and copy back the code that appears after you approve.")
	fmt.Fprintln(out)

	authURL, pkce, err := oauth.AnthropicAuthorizeURL(cfg)
	if err != nil {
		return "", fmt.Errorf("could not start the auth flow: %w", err)
	}

	printInfo(out, "Open this URL in your browser:")
	printLink(out, authURL)
	fmt.Fprintln(out)

	// The user pastes the code they get after approving in the browser.
	fmt.Fprintf(out, "%sPaste the code from the browser here:%s ", colCyan, colReset)
	code, err := readLine(ctx, in)
	if err != nil {
		return "", err
	}
	code = strings.TrimSpace(code)
	if code == "" {
		return "", fmt.Errorf("no code was given")
	}

	result, err := oauth.AnthropicExchangeCode(ctx, nil, cfg, code, pkce)
	if err != nil {
		return "", fmt.Errorf("could not exchange the code: %w", err)
	}

	printSuccess(out, "Connected to Anthropic!")
	return result.AccessToken, nil
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
