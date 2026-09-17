package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// --- Remaining tool and agent paths -----------------------------------------

// TestPaintHonoursTheGlobalSetting: with colours disabled the text must come back
// untouched, so redirected output stays clean.
func TestPaintHonoursTheGlobalSetting(t *testing.T) {
	saved := colorEnabled
	defer func() { colorEnabled = saved }()

	colorEnabled = false
	if got := paint(colorRed, "text"); got != "text" {
		t.Errorf("with colours off it must return the text: %q", got)
	}
	colorEnabled = true
	if got := paint(colorRed, "text"); got != colorRed+"text"+colorReset {
		t.Errorf("with colours on it must wrap the text: %q", got)
	}
	if got := paint("", "text"); got != "text" {
		t.Errorf("with no colour it must return the text: %q", got)
	}
}

// TestDecodeArgsRejectsBrokenJSON: a string that claims to contain JSON but does
// not must be reported instead of being passed on as text.
func TestDecodeArgsRejectsBrokenJSON(t *testing.T) {
	var dst readFileArgs
	// A quoted string whose contents are not JSON.
	if err := decodeArgs(json.RawMessage(`"not json at all"`), &dst); err == nil {
		t.Error("a string with broken JSON must be an error")
	}
	// An object with the wrong shape for the destination.
	if err := decodeArgs(json.RawMessage(`{"path": 42}`), &dst); err == nil {
		t.Error("a value of the wrong type must be an error")
	}
}

// TestDecodeArgsWithAnEmptyQuotedString: `""` means "no arguments", it is not an
// error (several gateways send it that way).
func TestDecodeArgsWithAnEmptyQuotedString(t *testing.T) {
	dst := readFileArgs{Path: "untouched"}
	if err := decodeArgs(json.RawMessage(`"   "`), &dst); err != nil {
		t.Fatalf("an empty string must be accepted: %v", err)
	}
	if dst.Path != "untouched" {
		t.Errorf("the value must not change: %q", dst.Path)
	}
}

// TestReadFileUnreadable: a file that exists but cannot be opened (no permission)
// must be reported, because the model needs to know why it failed.
func TestReadFileUnreadable(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("as root almost everything is readable")
	}
	path := filepath.Join(t.TempDir(), "secret.txt")
	if err := os.WriteFile(path, []byte("classified"), 0o000); err != nil {
		t.Fatal(err)
	}
	got := toolReadFile(args(t, readFileArgs{Path: path}))
	if !strings.Contains(got, "Error opening the file") {
		t.Errorf("an unreadable file must be reported: %q", got)
	}
}

// TestReadFileWithABrokenSymlink: a symlink pointing nowhere must be reported as
// an access error, not as a crash.
func TestReadFileWithABrokenSymlink(t *testing.T) {
	dir := t.TempDir()
	link := filepath.Join(dir, "dangling")
	if err := os.Symlink(filepath.Join(dir, "nowhere"), link); err != nil {
		t.Skip("symlinks are not available here")
	}
	got := toolReadFile(args(t, readFileArgs{Path: link}))
	if !strings.Contains(got, "Error") {
		t.Errorf("a broken symlink must be reported: %q", got)
	}
}

// TestReadFileEmpty: an empty file is not an error, but the model must be told
// there is nothing in it.
func TestReadFileEmpty(t *testing.T) {
	path := filepath.Join(t.TempDir(), "empty.txt")
	if err := os.WriteFile(path, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	if got := toolReadFile(args(t, readFileArgs{Path: path})); !strings.Contains(got, "empty") {
		t.Errorf("an empty file must be reported as such: %q", got)
	}
}

// TestLimitedBufferStopsAtTheLimit: the buffer must drop the surplus and flag it,
// without failing the command (which is what keeps a huge output from filling
// RAM on a 32-bit machine).
func TestLimitedBufferStopsAtTheLimit(t *testing.T) {
	buf := &limitedBuffer{max: 10}

	if n, err := buf.Write([]byte("12345")); err != nil || n != 5 {
		t.Fatalf("first write: n=%d err=%v", n, err)
	}
	if buf.truncated {
		t.Error("nothing was cut yet")
	}

	// Exactly filling the buffer is not truncation.
	if n, err := buf.Write([]byte("67890")); err != nil || n != 5 {
		t.Fatalf("second write: n=%d err=%v", n, err)
	}
	if buf.truncated {
		t.Error("exactly filling the buffer is not a truncation")
	}

	// Anything beyond the limit is dropped and flagged.
	if n, err := buf.Write([]byte("more")); err != nil || n != 4 {
		t.Fatalf("third write: n=%d err=%v", n, err)
	}
	if !buf.truncated {
		t.Error("the surplus must be flagged as truncated")
	}
	if buf.buf.String() != "1234567890" {
		t.Errorf("content = %q", buf.buf.String())
	}

	// A write that only partly fits must keep the part that fits.
	partial := &limitedBuffer{max: 4}
	if n, err := partial.Write([]byte("abcdef")); err != nil || n != 6 {
		t.Fatalf("partial write: n=%d err=%v", n, err)
	}
	if partial.buf.String() != "abcd" || !partial.truncated {
		t.Errorf("content = %q truncated=%v", partial.buf.String(), partial.truncated)
	}
}

// TestRunCommandTruncatesLongOutput: the tool must tell the model the output was
// cut, so it does not reason as if it had seen everything.
func TestRunCommandTruncatesLongOutput(t *testing.T) {
	// A command that produces clearly more than the limit (the trailing notice is
	// appended after the cut, so the total must exceed it comfortably).
	count := maxToolOutputBytes*2 + 4096
	got := toolRunCommand(args(t, runCommandArgs{
		Cmd: fmt.Sprintf("head -c %d /dev/zero | tr '\\0' 'x'", count),
	}))
	if !strings.Contains(got, "output truncated") {
		t.Errorf("the truncation must be announced (len=%d): %q", len(got), got[max(0, len(got)-140):])
	}
	if len(got) > maxToolOutputBytes+200 {
		t.Errorf("the output must stay bounded, got %d bytes", len(got))
	}
}

// TestCompleteRejectsAnEmptyChoicesListWithAnErrorObject: some providers answer
// 200 with an error object instead of choices.
func TestCompleteRejectsAnErrorObjectWithStatusOK(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, `{"error":{"message":"the key has expired"}}`)
	}))
	defer srv.Close()

	a := newAgent(config{apiKey: "k", baseURL: srv.URL, model: "test", maxLoops: 1, timeout: 5 * time.Second})
	_, err := a.complete("")
	if err == nil || !strings.Contains(err.Error(), "the key has expired") {
		t.Errorf("err = %v", err)
	}
}

// TestCompleteWithAnUnreachableEndpoint: a network failure must be reported as
// such, naming the cause.
func TestCompleteWithAnUnreachableEndpoint(t *testing.T) {
	a := newAgent(config{apiKey: "k", baseURL: "http://127.0.0.1:1", model: "test", maxLoops: 1, timeout: 2 * time.Second})
	_, err := a.complete("")
	if err == nil || !strings.Contains(err.Error(), "network error") {
		t.Errorf("err = %v", err)
	}
}

// TestTrimHistoryWithoutOrphanedToolResults: a history that is over the limit and
// starts with tool results must be trimmed so the first kept message is not a
// tool result (the API rejects that).
func TestTrimHistoryWithoutOrphanedToolResults(t *testing.T) {
	a := newAgent(config{})
	a.hist = nil
	for i := 0; i < maxHistory+20; i++ {
		role := "user"
		if i%3 == 0 {
			role = "tool"
		}
		a.hist = append(a.hist, message{Role: role, Content: fmt.Sprintf("m%d", i)})
	}
	a.trimHistory()
	if len(a.hist) > maxHistory {
		t.Fatalf("history not trimmed: %d", len(a.hist))
	}
	if a.hist[1].Role == "tool" {
		t.Error("the history must not start with an orphaned tool result")
	}
}

// TestRunToolDispatchesEachTool: the dispatcher must route both tools and report
// an unknown one.
func TestRunToolDispatchesEachTool(t *testing.T) {
	a := newAgent(config{})
	dir := t.TempDir()
	path := filepath.Join(dir, "note.txt")
	os.WriteFile(path, []byte("contents here"), 0o644)

	if got := a.runTool(toolCall{Function: functionCall{Name: "read_file", Arguments: args(t, readFileArgs{Path: path})}}); !strings.Contains(got, "contents here") {
		t.Errorf("read_file = %q", got)
	}
	if got := a.runTool(toolCall{Function: functionCall{Name: "run_command", Arguments: args(t, runCommandArgs{Cmd: "echo dispatched"})}}); !strings.Contains(got, "dispatched") {
		t.Errorf("run_command = %q", got)
	}
	if got := a.runTool(toolCall{Function: functionCall{Name: "no_such_tool"}}); !strings.Contains(got, "unknown tool") {
		t.Errorf("an unknown tool must be reported: %q", got)
	}
}

// TestTurnWithABlankFinalAnswer: a model that answers with whitespace must not
// leave the user staring at an empty line.
func TestTurnWithABlankFinalAnswer(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, `{"choices":[{"finish_reason":"stop","message":{"role":"assistant","content":"   "}}]}`)
	}))
	defer srv.Close()

	a := newAgent(config{apiKey: "k", baseURL: srv.URL, model: "test", maxLoops: 1, timeout: 5 * time.Second})
	if err := a.turn("anything"); err != nil {
		t.Fatalf("a blank answer is not a failure: %v", err)
	}
}

// TestTurnWithoutToolsAndWithoutContent: a model that stops with neither text nor
// tool calls must still produce a readable line.
func TestTurnWithoutToolsAndWithoutContent(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, `{"choices":[{"finish_reason":"stop","message":{"role":"assistant"}}]}`)
	}))
	defer srv.Close()

	a := newAgent(config{apiKey: "k", baseURL: srv.URL, model: "test", maxLoops: 1, timeout: 5 * time.Second})
	if err := a.turn("anything"); err != nil {
		t.Fatalf("an empty answer is not a failure: %v", err)
	}
}

// TestKillGroupWithoutAStartedProcess: with no process there is nothing to kill
// and it must say so instead of panicking.
func TestKillGroupWithoutAStartedProcess(t *testing.T) {
	cmd := exec.Command("/bin/true")
	if err := killGroup(cmd); err == nil {
		t.Error("with no process started it must return an error")
	}
}

// --- The interactive REPL, driven through a pipe --------------------------

// TestMainInteractiveReplCommands drives the REPL for real: the commands are fed
// through stdin and the transcript is read on stderr. It is the only way to cover
// the loop that a person actually uses.
func TestMainInteractiveReplCommands(t *testing.T) {
	binary, err := os.Executable()
	if err != nil {
		t.Skip("could not locate the test binary")
	}

	cmd := exec.Command(binary)
	cmd.Env = append(os.Environ(),
		"STARLIGHT_TEST_MAIN=1",
		"NO_COLOR=1",
		"OPENAI_API_KEY=test",
		"OPENAI_BASE_URL=http://127.0.0.1:1/v1",
		"OPENAI_MODEL=mock",
	)
	// The commands: help, reset, a blank line, and exit.
	cmd.Stdin = strings.NewReader("/help\n/reset\n\nexit\n")

	var out, errs bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &errs

	if err := cmd.Run(); err != nil {
		t.Fatalf("the REPL must end cleanly: %v (%s)", err, errs.String())
	}

	text := errs.String()
	for _, part := range []string{"REPL commands", "Conversation forgotten", "Agent finished"} {
		if !strings.Contains(text, part) {
			t.Errorf("the transcript must include %q: %q", part, text)
		}
	}
}

// TestMainReplTreatsAnUnknownCommandAsAnInstruction: a line that is not a command
// is sent to the model, so the run fails on the unreachable endpoint (which proves
// the instruction was taken rather than ignored).
func TestMainReplTreatsAnUnknownCommandAsAnInstruction(t *testing.T) {
	binary, err := os.Executable()
	if err != nil {
		t.Skip("could not locate the test binary")
	}

	cmd := exec.Command(binary, "-timeout", "1s")
	cmd.Env = append(os.Environ(),
		"STARLIGHT_TEST_MAIN=1",
		"NO_COLOR=1",
		"OPENAI_API_KEY=test",
		"OPENAI_BASE_URL=http://127.0.0.1:1/v1",
	)
	cmd.Stdin = strings.NewReader("what is the weather\nexit\n")

	var out, errs bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &errs

	_ = cmd.Run()
	if !strings.Contains(errs.String(), "❌") {
		t.Errorf("the failed instruction must be reported: %q", errs.String())
	}
}

// TestMainReplEndsOnEOF: closing stdin (Ctrl+D) must end the REPL with the closing
// line, without hanging.
func TestMainReplEndsOnEOF(t *testing.T) {
	binary, err := os.Executable()
	if err != nil {
		t.Skip("could not locate the test binary")
	}

	cmd := exec.Command(binary)
	cmd.Env = append(os.Environ(), "STARLIGHT_TEST_MAIN=1", "NO_COLOR=1", "OPENAI_API_KEY=test")
	cmd.Stdin = strings.NewReader("") // immediate EOF

	done := make(chan error, 1)
	var errs bytes.Buffer
	cmd.Stderr = &errs
	cmd.Stdout = &bytes.Buffer{}
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	go func() { done <- cmd.Wait() }()

	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("EOF must end the REPL cleanly: %v (%s)", err, errs.String())
		}
	case <-time.After(10 * time.Second):
		cmd.Process.Kill()
		t.Fatal("the REPL hung on EOF")
	}
	if !strings.Contains(errs.String(), "Agent finished") {
		t.Errorf("the closing line must be shown: %q", errs.String())
	}
}

// TestMainRejectsAnUnknownFlag: an unrecognised flag must be refused by the flag
// package, with the usage text.
func TestMainRejectsAnUnknownFlag(t *testing.T) {
	_, errs, code := runMain(t, nil, "-no-such-flag")
	if code == 0 {
		t.Error("an unknown flag must not exit successfully")
	}
	if !strings.Contains(errs, "flag provided but not defined") {
		t.Errorf("the flag error must be shown: %q", errs)
	}
}

// --- The last guarded branches ----------------------------------------------

// TestDecodeArgsWithBrokenEmbeddedJSON: a quoted string whose body is not JSON at
// all must be an error, not silently ignored.
func TestDecodeArgsWithBrokenEmbeddedJSON(t *testing.T) {
	var dst readFileArgs
	if err := decodeArgs(json.RawMessage(`"this is not json"`), &dst); err == nil {
		t.Error("a quoted string with broken JSON must be an error")
	}
}

// TestReadFileWithAnEmptyArgumentObject: `{}` means no path was given, and the
// model must be told which parameter is missing.
func TestReadFileWithAnEmptyArgumentObject(t *testing.T) {
	if got := toolReadFile(json.RawMessage(`{}`)); !strings.Contains(got, "'path' parameter is missing") {
		t.Errorf("got = %q", got)
	}
}

// TestRunCommandWithBrokenArguments: broken arguments must be reported instead of
// being treated as an empty command.
func TestRunCommandWithBrokenArguments(t *testing.T) {
	if got := toolRunCommand(json.RawMessage(`"not json"`)); !strings.Contains(got, "Error parsing the arguments") {
		t.Errorf("got = %q", got)
	}
}

// TestCompleteWithABrokenResponseBody: a 200 whose body cannot be read must be
// reported as a read failure.
func TestCompleteWithABrokenResponseBody(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Announcing more bytes than are sent makes the read fail.
		w.Header().Set("Content-Length", "500")
		fmt.Fprint(w, "short")
	}))
	defer srv.Close()

	a := newAgent(config{apiKey: "k", baseURL: srv.URL, model: "test", maxLoops: 1, timeout: 5 * time.Second})
	if _, err := a.complete(""); err == nil {
		t.Error("a truncated body must be an error")
	}
}

// TestCompleteWithAStatusThatCarriesNoJSONError: a non-2xx whose body is not the
// documented error envelope must still report the status and the raw body.
func TestCompleteWithAStatusThatCarriesNoJSONError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusServiceUnavailable)
		fmt.Fprint(w, "gateway is down")
	}))
	defer srv.Close()

	a := newAgent(config{apiKey: "k", baseURL: srv.URL, model: "test", maxLoops: 1, timeout: 5 * time.Second})
	_, err := a.complete("")
	if err == nil {
		t.Fatal("a 503 must be an error")
	}
	if !strings.Contains(err.Error(), "503") || !strings.Contains(err.Error(), "gateway is down") {
		t.Errorf("the error must carry the status and the body: %v", err)
	}
}

// TestTurnReportsWhenForcingTheFinalAnswerFails: if the forced final call fails,
// the turn must be reported as failed and the history rolled back.
func TestTurnReportsWhenForcingTheFinalAnswerFails(t *testing.T) {
	var calls int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		if calls == 1 {
			// A tool call, so the loop continues.
			fmt.Fprint(w, `{"choices":[{"finish_reason":"tool_calls","message":{"role":"assistant",
				"tool_calls":[{"id":"c1","type":"function","function":{"name":"run_command","arguments":"{\"cmd\":\"true\"}"}}]}}]}`)
			return
		}
		// The forced final answer fails.
		w.WriteHeader(http.StatusInternalServerError)
		fmt.Fprint(w, `{"error":{"message":"the engine gave up"}}`)
	}))
	defer srv.Close()

	a := newAgent(config{apiKey: "k", baseURL: srv.URL, model: "test", maxLoops: 1, timeout: 5 * time.Second})
	before := len(a.hist)
	if err := a.turn("do something"); err == nil {
		t.Fatal("a failing forced answer must be reported")
	}
	if len(a.hist) != before {
		t.Errorf("the history must be rolled back: %d (before %d)", len(a.hist), before)
	}
}

// TestKillGroupOnAFinishedProcess: killing a group whose process already exited
// must report the condition instead of panicking.
func TestKillGroupOnAFinishedProcess(t *testing.T) {
	cmd := exec.Command("/bin/true")
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	if err := cmd.Wait(); err != nil {
		t.Fatal(err)
	}
	if err := killGroup(cmd); err == nil {
		t.Error("killing the group of a finished process must report the condition")
	}
}

// TestMainWithAModelAndURLFlag: the -model and -url flags must override the
// environment (they are how a user switches provider without exporting anything).
func TestMainWithAModelAndURLFlag(t *testing.T) {
	_, errs, code := runMain(t,
		[]string{"OPENAI_API_KEY=test", "OPENAI_BASE_URL=http://127.0.0.1:9/v1", "OPENAI_MODEL=from-env"},
		"-p", "hello", "-model", "from-flag", "-url", "http://127.0.0.1:1/v1", "-timeout", "1s")
	if code != 1 {
		t.Fatalf("code = %d (%q)", code, errs)
	}
	if !strings.Contains(errs, "❌") {
		t.Errorf("the failure must be reported: %q", errs)
	}
}

// --- The final guarded branches ---------------------------------------------

// TestTrimHistoryWhenTheTailIsAllToolResults: a long run of tool results at the
// end of the history must be trimmed to just the system prompt, because sending a
// tool result without the call it answers is rejected by the provider.
func TestTrimHistoryWhenTheTailIsAllToolResults(t *testing.T) {
	a := newAgent(config{})
	for i := 0; i < maxHistory+5; i++ {
		a.hist = append(a.hist, message{Role: "tool", Content: fmt.Sprintf("t%d", i), ToolCallID: fmt.Sprintf("c%d", i)})
	}
	a.trimHistory()

	if len(a.hist) == 0 {
		t.Fatal("the system prompt must always stay")
	}
	if a.hist[0].Role != "system" {
		t.Errorf("the first message must be the system prompt, got %q", a.hist[0].Role)
	}
	// Nothing usable survived, so only the system prompt is left.
	if len(a.hist) != 1 {
		t.Errorf("history = %d messages, expected only the system prompt", len(a.hist))
	}
}

// TestCompletePostsToTheConfiguredBaseURLTrimmingSlashes: a base URL written with
// a trailing slash must not produce a doubled one.
func TestCompleteTrimsTheTrailingSlash(t *testing.T) {
	var seenPath string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seenPath = r.URL.Path
		fmt.Fprint(w, `{"choices":[{"message":{"role":"assistant","content":"ok"}}]}`)
	}))
	defer srv.Close()

	// The trailing slash is added on purpose.
	a := newAgent(config{apiKey: "k", baseURL: srv.URL + "/", model: "test", maxLoops: 1, timeout: 5 * time.Second})
	if _, err := a.complete(""); err != nil {
		t.Fatal(err)
	}
	if seenPath != "/chat/completions" {
		t.Errorf("path = %q, the slash must not be doubled", seenPath)
	}
}

// TestCompleteSendsTheAuthorisationHeader: the key must travel as a bearer token,
// which is the only thing that makes the call authenticated.
func TestCompleteSendsTheAuthorisationHeader(t *testing.T) {
	var header string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		header = r.Header.Get("Authorization")
		fmt.Fprint(w, `{"choices":[{"message":{"role":"assistant","content":"ok"}}]}`)
	}))
	defer srv.Close()

	a := newAgent(config{apiKey: "secret-key", baseURL: srv.URL, model: "test", maxLoops: 1, timeout: 5 * time.Second})
	if _, err := a.complete(""); err != nil {
		t.Fatal(err)
	}
	if header != "Bearer secret-key" {
		t.Errorf("header = %q", header)
	}
}

// TestCompleteDeclaresTheTools: the request must carry both tool definitions,
// because that is what lets the model ask for them at all.
func TestCompleteDeclaresTheTools(t *testing.T) {
	var tools []struct {
		Function struct {
			Name string `json:"name"`
		} `json:"function"`
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewDecoder(r.Body).Decode(&struct {
			Tools *[]struct {
				Function struct {
					Name string `json:"name"`
				} `json:"function"`
			} `json:"tools"`
		}{Tools: &tools})
		fmt.Fprint(w, `{"choices":[{"message":{"role":"assistant","content":"ok"}}]}`)
	}))
	defer srv.Close()

	a := newAgent(config{apiKey: "k", baseURL: srv.URL, model: "test", maxLoops: 1, timeout: 5 * time.Second})
	if _, err := a.complete(""); err != nil {
		t.Fatal(err)
	}
	if len(tools) != 2 || tools[0].Function.Name != "read_file" || tools[1].Function.Name != "run_command" {
		t.Errorf("tools = %+v", tools)
	}
}

// TestMainWithAWirelessEndpointAndALowLoopLimit: the -max-loops flag must be
// honoured, and the forced final answer must be attempted before giving up.
func TestMainWithAWirelessEndpointAndALowLoopLimit(t *testing.T) {
	_, errs, code := runMain(t,
		[]string{"OPENAI_API_KEY=test", "OPENAI_BASE_URL=http://127.0.0.1:1/v1"},
		"-p", "hello", "-max-loops", "1", "-timeout", "1s")
	if code != 1 {
		t.Fatalf("code = %d (%q)", code, errs)
	}
}

// --- The last four blocks ---------------------------------------------------

// TestDecodeArgsWithAMalformedQuotedString: a string starting with a quote whose
// JSON is broken must be reported (the quote is the tell that a gateway wrapped
// the object).
func TestDecodeArgsWithAMalformedQuotedString(t *testing.T) {
	var dst runCommandArgs
	// Opens with a quote but never closes it.
	if err := decodeArgs(json.RawMessage(`"{\"cmd\":`), &dst); err == nil {
		t.Error("a malformed quoted string must be an error")
	}
}

// TestReadFileWhenTheLimitReaderFails: reading a file that cannot be read to the
// end must be reported. A directory is caught earlier, so the failure is
// reproduced with a file whose read fails midway.
func TestReadFileReportsTheReadFailure(t *testing.T) {
	// /proc/self/mem is a file that exists, reports a size and fails on read.
	path := "/proc/self/mem"
	if _, err := os.Stat(path); err != nil {
		t.Skip("no /proc in this environment")
	}
	got := toolReadFile(args(t, readFileArgs{Path: path}))
	if !strings.Contains(got, "Error") {
		t.Errorf("a failing read must be reported: %q", got)
	}
}

// TestCompleteWhenTheRequestCannotBeBuilt: a base URL that http.NewRequest
// rejects must be reported as an invalid request.
func TestCompleteWhenTheRequestCannotBeBuilt(t *testing.T) {
	a := newAgent(config{apiKey: "k", baseURL: "http://[::1]:namedport", model: "test", maxLoops: 1, timeout: time.Second})
	if _, err := a.complete(""); err == nil {
		t.Error("an invalid URL must be an error")
	}
}

// TestMainReplIgnoresBlankLines: pressing Enter on its own must not send anything
// to the model, and the REPL must carry on until told to exit.
func TestMainReplIgnoresBlankLines(t *testing.T) {
	binary, err := os.Executable()
	if err != nil {
		t.Skip("could not locate the test binary")
	}

	cmd := exec.Command(binary)
	cmd.Env = append(os.Environ(), "STARLIGHT_TEST_MAIN=1", "NO_COLOR=1", "OPENAI_API_KEY=test")
	// Two blank lines, then exit: nothing must be sent.
	cmd.Stdin = strings.NewReader("\n\n\nexit\n")

	var out, errs bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &errs
	if err := cmd.Run(); err != nil {
		t.Fatalf("blank lines must be ignored: %v (%s)", err, errs.String())
	}
	if strings.Contains(errs.String(), "❌") {
		t.Errorf("no request must be made: %q", errs.String())
	}
	if !strings.Contains(errs.String(), "Agent finished") {
		t.Errorf("the REPL must end on exit: %q", errs.String())
	}
}

// TestMainWithNoArgumentsAndNoStdin: with no instruction and no input, the REPL
// must start and end immediately (the EOF path), not hang.
func TestMainWithNoArgumentsAndNoStdin(t *testing.T) {
	binary, err := os.Executable()
	if err != nil {
		t.Skip("could not locate the test binary")
	}
	cmd := exec.Command(binary)
	cmd.Env = append(os.Environ(), "STARLIGHT_TEST_MAIN=1", "NO_COLOR=1", "OPENAI_API_KEY=test")
	cmd.Stdin = strings.NewReader("")
	cmd.Stdout = &bytes.Buffer{}
	var errs bytes.Buffer
	cmd.Stderr = &errs

	done := make(chan error, 1)
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	go func() { done <- cmd.Wait() }()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("the REPL must end on EOF: %v", err)
		}
	case <-time.After(10 * time.Second):
		cmd.Process.Kill()
		t.Fatal("the REPL hung with no input")
	}
}

// --- The last four blocks ---------------------------------------------------

// TestReadFileWithAnUnreadableFile: a file that cannot be read to the end must be
// reported, because the model needs to know the contents are incomplete.
func TestReadFileWithAnUnreadableFile(t *testing.T) {
	path := "/proc/self/mem"
	if _, err := os.Stat(path); err != nil {
		t.Skip("no /proc in this environment")
	}
	got := toolReadFile(args(t, readFileArgs{Path: path}))
	if !strings.Contains(got, "Error") {
		t.Errorf("a failing read must be reported: %q", got)
	}
}

// TestMainReplCarriesOnAfterAFailedTurn: a turn that fails must be reported and
// the REPL must keep running (a person can simply try again or exit).
func TestMainReplCarriesOnAfterAFailedTurn(t *testing.T) {
	binary, err := os.Executable()
	if err != nil {
		t.Skip("could not locate the test binary")
	}

	cmd := exec.Command(binary, "-timeout", "1s")
	cmd.Env = append(os.Environ(),
		"STARLIGHT_TEST_MAIN=1",
		"NO_COLOR=1",
		"OPENAI_API_KEY=test",
		"OPENAI_BASE_URL=http://127.0.0.1:1/v1",
	)
	// One instruction that fails (the endpoint is unreachable), then exit.
	cmd.Stdin = strings.NewReader("do something impossible\nexit\n")

	var out, errs bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &errs

	if err := cmd.Run(); err != nil {
		t.Fatalf("the REPL must survive a failed turn: %v (%s)", err, errs.String())
	}
	if !strings.Contains(errs.String(), "❌") {
		t.Errorf("the failure must be reported: %q", errs.String())
	}
	if !strings.Contains(errs.String(), "Agent finished") {
		t.Errorf("the REPL must still accept the exit command: %q", errs.String())
	}
}

// --- The final four blocks (real CLI behaviour) -----------------------------

// TestMainReplEndsWhenTheStreamClosesMidLine: if the input stream ends without a
// newline (a pipe closing, Ctrl+D after typing), the line is still processed and
// the REPL finishes cleanly.
func TestMainReplEndsWhenTheStreamClosesMidLine(t *testing.T) {
	binary, err := os.Executable()
	if err != nil {
		t.Skip("could not locate the test binary")
	}

	cmd := exec.Command(binary, "-timeout", "1s")
	cmd.Env = append(os.Environ(),
		"STARLIGHT_TEST_MAIN=1", "NO_COLOR=1",
		"OPENAI_API_KEY=test", "OPENAI_BASE_URL=http://127.0.0.1:1/v1",
	)
	// No trailing newline: ReadString returns the text together with io.EOF.
	cmd.Stdin = strings.NewReader("an instruction with no newline")

	var out, errs bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &errs

	if err := cmd.Run(); err != nil {
		t.Fatalf("the REPL must end cleanly: %v (%s)", err, errs.String())
	}
	text := errs.String()
	if !strings.Contains(text, "❌") {
		t.Errorf("the instruction must have been attempted: %q", text)
	}
	if !strings.Contains(text, "Agent finished") {
		t.Errorf("the closing line must be shown: %q", text)
	}
}

// TestMainNonInteractiveReturnsAfterOneInstruction: the one-shot mode must exit
// instead of falling into the REPL (that is what makes it usable from a script).
func TestMainNonInteractiveReturnsAfterOneInstruction(t *testing.T) {
	binary, err := os.Executable()
	if err != nil {
		t.Skip("could not locate the test binary")
	}

	cmd := exec.Command(binary, "-p", "hello", "-timeout", "1s")
	cmd.Env = append(os.Environ(),
		"STARLIGHT_TEST_MAIN=1", "NO_COLOR=1",
		"OPENAI_API_KEY=test", "OPENAI_BASE_URL=http://127.0.0.1:1/v1",
	)
	// If it fell into the REPL, it would wait for this stdin and print the banner.
	cmd.Stdin = strings.NewReader("")

	var out, errs bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &errs

	done := make(chan error, 1)
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	go func() { done <- cmd.Wait() }()

	select {
	case <-done:
	case <-time.After(15 * time.Second):
		cmd.Process.Kill()
		t.Fatal("the one-shot mode hung")
	}
	if strings.Contains(errs.String(), "Starlight ready") {
		t.Errorf("the one-shot mode must not enter the REPL: %q", errs.String())
	}
}

// TestToolReadFileWithAnUnreadableFile: a path that exists but cannot be read must
// be reported so the model knows the contents are missing.
func TestToolReadFileWithAnUnreadableFile(t *testing.T) {
	path := "/proc/self/mem"
	if _, err := os.Stat(path); err != nil {
		t.Skip("no /proc in this environment")
	}
	if got := toolReadFile(args(t, readFileArgs{Path: path})); !strings.Contains(got, "Error") {
		t.Errorf("a failing read must be reported: %q", got)
	}
}

// TestTurnWhenTheForcedAnswerFailsRollsBack: if the forced final call fails, the
// turn is reported and the history is rolled back so the next turn is consistent.
func TestTurnWhenTheForcedAnswerFailsRollsBack(t *testing.T) {
	calls := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		if calls == 1 {
			fmt.Fprint(w, `{"choices":[{"finish_reason":"tool_calls","message":{"role":"assistant",
				"tool_calls":[{"id":"c1","type":"function","function":{"name":"run_command","arguments":"{\"cmd\":\"true\"}"}}]}}]}`)
			return
		}
		w.WriteHeader(http.StatusInternalServerError)
		fmt.Fprint(w, `{"error":{"message":"the engine gave up"}}`)
	}))
	defer srv.Close()

	a := newAgent(config{apiKey: "k", baseURL: srv.URL, model: "test", maxLoops: 1, timeout: 5 * time.Second})
	before := len(a.hist)
	if err := a.turn("do it"); err == nil {
		t.Fatal("a failing forced answer must be reported")
	}
	if len(a.hist) != before {
		t.Errorf("the history must be rolled back: %d (before %d)", len(a.hist), before)
	}
}

// TestToolReadFileWithBrokenArguments: broken arguments must come back as a
// readable message, because the model reads it and corrects the call.
func TestToolReadFileWithBrokenArguments(t *testing.T) {
	got := toolReadFile(json.RawMessage(`"not json at all"`))
	if !strings.Contains(got, "Error parsing the arguments") {
		t.Errorf("got = %q", got)
	}
}

// TestOneShotExitCodes: the one-shot mode's exit code is the contract a script
// relies on: zero when the instruction succeeded, one when it failed.
func TestOneShotExitCodes(t *testing.T) {
	// A failure: the endpoint is unreachable.
	out, errs, code := runMain(t,
		[]string{"OPENAI_API_KEY=test", "OPENAI_BASE_URL=http://127.0.0.1:1/v1"},
		"-p", "hello", "-timeout", "1s")
	if code != 1 {
		t.Fatalf("a failed instruction must exit 1, got %d (%q / %q)", code, out, errs)
	}
	if !strings.Contains(errs, "❌") {
		t.Errorf("the failure must be reported: %q", errs)
	}

	// A success: a server that answers with a final message.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, `{"choices":[{"finish_reason":"stop","message":{"role":"assistant","content":"all done"}}]}`)
	}))
	defer srv.Close()

	out, errs, code = runMain(t,
		[]string{"OPENAI_API_KEY=test", "OPENAI_BASE_URL=" + srv.URL, "OPENAI_MODEL=mock"},
		"-p", "hello", "-timeout", "5s")
	if code != 0 {
		t.Fatalf("a successful instruction must exit 0, got %d (%q / %q)", code, out, errs)
	}
	if !strings.Contains(out, "all done") {
		t.Errorf("the answer must go to stdout: %q", out)
	}
	if strings.Contains(errs, "Starlight ready") {
		t.Errorf("the one-shot mode must not enter the REPL: %q", errs)
	}
}
