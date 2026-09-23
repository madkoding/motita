package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/madkoding/starlight/internal/config"
	taskpkg "github.com/madkoding/starlight/internal/task"
)

// taskOf builds a task with only its description, which is all the conversation keys on.
func taskOf(description string) taskpkg.Task {
	return taskpkg.Task{Description: description, Origin: "test"}
}

// A message that is not a task is answered, not executed.
//
// The agent used to have exactly one response to everything: analyse it as work, plan it, run
// it, validate it. A greeting produced a task plan, and a question about what it had just done
// produced another one. The user asked to be talked to.

// chatServer answers the analysis phase with a conversational classification.
func chatServer(t *testing.T, reply string) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		var req struct {
			Messages []struct {
				Content string `json:"content"`
			} `json:"messages"`
		}
		_ = json.Unmarshal(body, &req)
		var text string
		for _, m := range req.Messages {
			text += m.Content
		}
		// Anything after the analysis phase is a bug for a chat turn, and answering it with
		// something distinctive is what makes the bug visible instead of silent.
		if strings.Contains(text, "## ANALYSIS OF THE TASK") {
			fmt.Fprintf(w, `{"choices":[{"message":{"content":%q}}]}`,
				`{"kind":"chat","understandable":true,"reply":"`+reply+`"}`)
			return
		}
		fmt.Fprint(w, `{"choices":[{"message":{"content":"{\"plan\":[]}"}}]}`)
	}))
}

func chatFixture(t *testing.T, reply string) *fixture {
	t.Helper()
	srv := chatServer(t, reply)
	t.Cleanup(srv.Close)
	return mount(t, srv, config.Anchor{Kind: "command", Command: "true", Timeout: 5 * time.Second}, nil)
}

// TestAGreetingIsAnsweredNotExecuted: the whole point. "hello" must come back as a reply, with
// no plan, no action and no validation.
func TestAGreetingIsAnsweredNotExecuted(t *testing.T) {
	e := chatFixture(t, "Hi! What do you need?")

	var result *TaskResult
	e.agent.Observer = func(r TaskResult) { result = &r }

	if err := e.agent.Run(context.Background()); err != nil {
		t.Fatalf("a conversational turn must not be an error: %v", err)
	}
	if result == nil {
		t.Fatal("no result was reported")
	}
	if result.Kind != KindChat {
		t.Errorf("kind = %q, want %q", result.Kind, KindChat)
	}
	if result.Reply != "Hi! What do you need?" {
		t.Errorf("reply = %q", result.Reply)
	}
	// Nothing ran, and nothing claims to have run.
	if result.FinalAction != "" {
		t.Errorf("a chat turn must not run a final action, got %q", result.FinalAction)
	}
	if result.Validation != nil {
		t.Errorf("a chat turn must not be validated, got %+v", result.Validation)
	}
	if result.Attempts != 0 {
		t.Errorf("a chat turn must not run the loop, got %d attempts", result.Attempts)
	}
}

// TestAChatTurnIsNotAFailure: it is an answer. Reporting it as failed would tell the user
// something went wrong when the agent did exactly the right thing.
func TestAChatTurnIsNotAFailure(t *testing.T) {
	e := chatFixture(t, "I am ready.")

	var result *TaskResult
	e.agent.Observer = func(r TaskResult) { result = &r }
	_ = e.agent.Run(context.Background())

	if !result.Pass {
		t.Errorf("a chat turn did what was asked and must pass: %+v", result)
	}
	if result.NeedsInput {
		t.Error("a chat turn is not waiting for anything")
	}
}

// TestTheReplySurvivesAnEmptyOneFromTheModel: the model may classify a message as chat and
// write nothing. Falling through to the task loop would be worse — the classification is
// still its judgement, and a task plan is the answer the user did not ask for.
func TestTheReplySurvivesAnEmptyOneFromTheModel(t *testing.T) {
	e := chatFixture(t, "")

	var result *TaskResult
	e.agent.Observer = func(r TaskResult) { result = &r }
	_ = e.agent.Run(context.Background())

	if result.Kind != KindChat {
		t.Fatalf("kind = %q, want chat", result.Kind)
	}
	if strings.TrimSpace(result.Reply) == "" {
		t.Error("an empty reply must still say something rather than nothing")
	}
}

// TestAnUnknownKindIsTreatedAsATask: the default has to preserve the old behaviour. A prompt
// written before this field existed, or a model that does not answer it, must not turn every
// request into a chat.
func TestAnUnknownKindIsTreatedAsATask(t *testing.T) {
	for _, kind := range []string{"", "TASK", " work ", "nonsense"} {
		got := Analysis{Kind: kind}.resolveKind()
		if got != KindTask {
			t.Errorf("kind %q resolved to %q, want %q", kind, got, KindTask)
		}
	}
	for _, kind := range []string{"chat", "CHAT", " Chat "} {
		if got := (Analysis{Kind: kind}).resolveKind(); got != KindChat {
			t.Errorf("kind %q resolved to %q, want chat", kind, got)
		}
	}
	for _, kind := range []string{"ask", "ASK"} {
		if got := (Analysis{Kind: kind}).resolveKind(); got != KindAsk {
			t.Errorf("kind %q resolved to %q, want ask", kind, got)
		}
	}
}

// TestAskingIsReportedAsAsking: "ask" takes the same path as an unreadable request, so the
// question reaches the user even when the model left "understandable" true.
func TestAskingIsReportedAsAsking(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		if strings.Contains(string(body), "## ANALYSIS OF THE TASK") {
			fmt.Fprint(w, `{"choices":[{"message":{"content":"{\"kind\":\"ask\",\"understandable\":true,\"question\":\"About which folder?\",\"assumption\":\"reviso la actual\"}"}}]}`)
			return
		}
		fmt.Fprint(w, `{"choices":[{"message":{"content":"{\"plan\":[]}"}}]}`)
	}))
	defer srv.Close()

	e := mount(t, srv, config.Anchor{Kind: "command", Command: "true", Timeout: 5 * time.Second}, nil)
	e.agent.Interactive = true

	var result *TaskResult
	e.agent.Observer = func(r TaskResult) { result = &r }
	_ = e.agent.Run(context.Background())

	if result == nil || !result.NeedsInput {
		t.Fatalf("an ask must reach the user as a question: %+v", result)
	}
	if !strings.Contains(result.Question, "folder") {
		t.Errorf("question = %q", result.Question)
	}
}

// --- the conversation ----------------------------------------------------

// TestTheAssistantKnowsWhatItAlreadySaid: the reply from one turn is what the next turn's
// analysis reads. Without this a clarifying question is worse than useless — the agent asks,
// the user answers, and the agent has no idea what the answer refers to.
func TestTheAssistantKnowsWhatItAlreadySaid(t *testing.T) {
	e := chatFixture(t, "Which folder do you want to review?")

	var turn1 *TaskResult
	e.agent.Observer = func(r TaskResult) { turn1 = &r }
	_ = e.agent.Run(context.Background())
	if turn1 == nil || turn1.Kind != KindChat {
		t.Fatalf("first turn did not answer as chat: %+v", turn1)
	}

	turns := e.agent.Transcript()
	if len(turns) != 1 {
		t.Fatalf("the conversation must record the turn, got %d", len(turns))
	}
	if turns[0].Agent != "Which folder do you want to review?" {
		t.Errorf("the answer must be recorded: %+v", turns[0])
	}
	if turns[0].User == "" {
		t.Error("the user's message must be recorded too: the pair is the meaning")
	}
}

// TestThePromptCarriesTheConversation: the transcript must actually reach the model, not just
// be stored. This is the difference between remembering and looking like it remembers.
func TestThePromptCarriesTheConversation(t *testing.T) {
	e := chatFixture(t, "ok")
	e.agent.SetTranscript([]DialogueTurn{
		{User: "review the directory", Agent: "which directory?", Kind: KindAsk},
		{User: "el actual", Agent: "voy", Kind: KindChat},
	})

	got := e.agent.dialogue()
	if !strings.Contains(got, "review the directory") {
		t.Errorf("the prompt must carry what the user said: %q", got)
	}
	if !strings.Contains(got, "which directory?") {
		t.Errorf("the prompt must carry what the agent asked: %q", got)
	}
	if !strings.Contains(got, "el actual") {
		t.Errorf("the prompt must carry the answer: %q", got)
	}
}

// TestAnEmptyConversationSaysSoPlainly: on the first message there is nothing to carry, and
// the prompt says that instead of leaving a blank where a transcript should be — a model
// reading an empty section may think the context was lost.
func TestAnEmptyConversationSaysSoPlainly(t *testing.T) {
	e := chatFixture(t, "ok")
	got := e.agent.dialogue()
	if !strings.Contains(strings.ToLower(got), "first message") {
		t.Errorf("an empty transcript must say it is the first message: %q", got)
	}
}

// TestTheConversationIsBounded: it is fed to the analysis on every turn, so an unbounded one
// would grow until it crowded out the request being made now.
func TestTheConversationIsBounded(t *testing.T) {
	e := chatFixture(t, "ok")
	var turns []DialogueTurn
	for i := 0; i < 40; i++ {
		turns = append(turns, DialogueTurn{User: "message number " + string(rune('A'+i%26)), Agent: "reply", Kind: KindChat})
	}
	e.agent.SetTranscript(turns)

	got := e.agent.dialogue()
	lines := strings.Count(got, "\n")
	if lines > 30 {
		t.Errorf("the transcript must be bounded, got %d lines", lines)
	}
	// The tail is what a short answer refers to, so the MOST RECENT turn must survive.
	if !strings.Contains(got, "reply") {
		t.Errorf("the recent turns must be kept: %q", got)
	}
	// ...and it must say that it was truncated, so the model does not believe it has the whole
	// history.
	if !strings.Contains(got, "omitted") {
		t.Errorf("a truncated transcript must say so: %q", got)
	}
}

// TestARecordedTaskIsPartOfTheConversation: "do the same for the other one" only means
// something if the next turn can see what the previous one did.
func TestARecordedTaskIsPartOfTheConversation(t *testing.T) {
	e := chatFixture(t, "ok")
	e.agent.note(taskOf("count the files in the current directory"), "72 files", KindTask)

	got := e.agent.dialogue()
	if !strings.Contains(got, "count the files") {
		t.Errorf("the task must be in the conversation: %q", got)
	}
	if !strings.Contains(got, "72 files") {
		t.Errorf("what was done must be in the conversation: %q", got)
	}
}

// TestAMultiLineAnswerStaysOneLine: a reply with paragraphs would otherwise turn into a dozen
// transcript lines and eat the budget for no added meaning.
func TestAMultiLineAnswerStaysOneLine(t *testing.T) {
	e := chatFixture(t, "ok")
	e.agent.SetTranscript([]DialogueTurn{
		{User: "line one\nline two\nline three", Agent: "a\n\nb", Kind: KindChat},
	})
	got := e.agent.dialogue()
	if strings.Count(got, "\n") > 3 {
		t.Errorf("each turn must be one line, got: %q", got)
	}
}

// TestSetTranscriptReplaces: seeding must not append to whatever was there, or a caller that
// hands over the full history twice would double it every turn.
func TestSetTranscriptReplaces(t *testing.T) {
	e := chatFixture(t, "ok")
	e.agent.SetTranscript([]DialogueTurn{{User: "one", Kind: KindChat}})
	e.agent.SetTranscript([]DialogueTurn{{User: "two", Kind: KindChat}})

	turns := e.agent.Transcript()
	if len(turns) != 1 || turns[0].User != "two" {
		t.Errorf("SetTranscript must replace, got %+v", turns)
	}
}

// TestTranscriptIsCopiedOut: the returned slice must not alias the internal one, or a caller
// holding it would see it change under them as the turn appends.
func TestTranscriptIsCopiedOut(t *testing.T) {
	e := chatFixture(t, "ok")
	e.agent.SetTranscript([]DialogueTurn{{User: "one", Kind: KindChat}})

	out := e.agent.Transcript()
	out[0].User = "mutado"

	if e.agent.Transcript()[0].User != "one" {
		t.Error("the transcript must be copied out, not aliased")
	}
}

// TestAFinishedTaskEntersTheConversation is the regression test for a bug the manual test found
// and no unit test could have: the conversation a client READS was empty after a task.
//
// `note` was only ever called with KindAsk, and `converse` only on the chat branch, so KindTask was
// rendered and never written. Every unit test passed because they called `note` DIRECTLY - they
// proved that the renderer works, not that anything calls it on the path a task takes. The gateway
// endpoint that returns the transcript was pinned against a hand-written fake of the service, so it
// agreed with its own fake.
//
// The consequence was the exact failure the whole feature exists to prevent: attach to a session
// after a task ran, and the conversation reads back as `{"messages":[]}` - the blank screen /attach
// was built to avoid. The task leaves no trace for the NEXT turn either, so "and now do the same for
// the other one" had nothing to refer to, which is the amnesia the transcript exists to fix.
//
// It drives the REAL path - a task that runs and passes - because driving `note` is what left the
// hole in the first place.
func TestAFinishedTaskEntersTheConversation(t *testing.T) {
	fake := &fakeLLMServer{
		actionsPerAttempt: [][]string{{"echo hello > result.txt"}},
	}
	srv := httptest.NewServer(fake.handler(t))
	defer srv.Close()

	e := mount(t, srv, config.Anchor{
		Kind:         "command",
		Command:      "sh",
		Args:         []string{"-c", "test -s result.txt && echo READY"},
		Timeout:      10 * time.Second,
		ExpectOutput: "READY",
	}, nil)

	var result *TaskResult
	e.agent.Observer = func(r TaskResult) { result = &r }
	if err := e.agent.Run(context.Background()); err != nil {
		t.Fatalf("the agent returned an error: %v", err)
	}
	if result == nil || !result.Pass {
		t.Fatalf("the task was expected to pass, result: %+v", result)
	}

	turns := e.agent.Transcript()
	if len(turns) == 0 {
		t.Fatal("a task that ran left no turn behind, so a client attaching to this session " +
			"reads an empty conversation - which is the blank screen this feature exists to avoid")
	}
	var found bool
	for _, turn := range turns {
		if turn.Kind == KindTask && turn.User != "" && turn.Agent != "" {
			found = true
		}
	}
	if !found {
		t.Errorf("the task is not in the conversation as a task turn: %+v", turns)
	}

	// And the next turn can see it, which is what makes a follow-up question mean anything. The
	// assertion is on the SHAPE of the line and not on the mock's wording: the mock's summary is
	// its own invention, and pinning that would make this test fail for the wrong reason.
	got := e.agent.dialogue()
	if !strings.Contains(got, "user: do the test task") {
		t.Errorf("the next prompt does not carry what was asked: %q", got)
	}
	if !strings.Contains(got, "you (did it):") {
		t.Errorf("the next prompt does not carry the task as WORK THAT RAN: %q", got)
	}
}

// TestATaskOutcomeAlwaysSaysSomething: a turn recorded with an empty line would read back to a
// client as a message the user never sent, and an empty "you (did it):" is worse than a plain
// statement that it finished.
func TestATaskOutcomeAlwaysSaysSomething(t *testing.T) {
	cases := []struct {
		name string
		in   TaskResult
		want string
	}{
		// What the agent said in its own words wins: it is the answer the user read.
		{"summary", TaskResult{Summary: "there are twelve files", Reason: "1 check passed"}, "there are twelve files"},
		// A conversational reply, for a turn that was classified as work but answered in words.
		{"reply", TaskResult{Reply: "nothing to do", Reason: "no actions"}, "nothing to do"},
		// The verdict, when there is no prose.
		{"reason", TaskResult{Reason: "1 check(s) passed", Pass: true}, "1 check(s) passed"},
		// A blank summary must not win over a reason that says something.
		{"blank summary falls through", TaskResult{Summary: "   ", Reason: "the real reason"}, "the real reason"},
		// Nothing to say at all: the outcome is still stated, in the direction it went.
		{"nothing at all, passed", TaskResult{Pass: true}, "the task completed"},
		{"nothing at all, failed", TaskResult{}, "the task did not complete"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := taskOutcome(c.in); got != c.want {
				t.Errorf("taskOutcome(%+v) = %q, want %q", c.in, got, c.want)
			}
		})
	}
}

// TestASubtaskIsNotRecordedAsATurnOfItsOwn: a task that split into five subtasks reaches
// processTask six times. Recording each would read back to the user as six things they asked for,
// when they asked for one - and the conversation would grow by the agent's own bookkeeping.
func TestASubtaskIsNotRecordedAsATurnOfItsOwn(t *testing.T) {
	fake := &fakeLLMServer{
		actionsPerAttempt: [][]string{{"echo hello > result.txt"}},
	}
	srv := httptest.NewServer(fake.handler(t))
	defer srv.Close()

	e := mount(t, srv, config.Anchor{
		Kind:         "command",
		Command:      "sh",
		Args:         []string{"-c", "test -s result.txt && echo READY"},
		Timeout:      10 * time.Second,
		ExpectOutput: "READY",
	}, nil)

	// A depth above zero is how a subtask arrives.
	e.agent.processTask(context.Background(), taskOf("a subtask the user never typed"), 1)

	for _, turn := range e.agent.Transcript() {
		if strings.Contains(turn.User, "a subtask the user never typed") {
			t.Errorf("a subtask was recorded as a turn of its own: %+v", turn)
		}
	}
}
