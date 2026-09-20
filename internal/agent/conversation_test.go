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

// TestAGreetingIsAnsweredNotExecuted: the whole point. "hola" must come back as a reply, with
// no plan, no action and no validation.
func TestAGreetingIsAnsweredNotExecuted(t *testing.T) {
	e := chatFixture(t, "¡Hola! ¿Qué necesitas?")

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
	if result.Reply != "¡Hola! ¿Qué necesitas?" {
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
	e := chatFixture(t, "Estoy listo.")

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
			fmt.Fprint(w, `{"choices":[{"message":{"content":"{\"kind\":\"ask\",\"understandable\":true,\"question\":\"¿Sobre qué carpeta?\",\"assumption\":\"reviso la actual\"}"}}]}`)
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
	if !strings.Contains(result.Question, "carpeta") {
		t.Errorf("question = %q", result.Question)
	}
}

// --- the conversation ----------------------------------------------------

// TestTheAssistantKnowsWhatItAlreadySaid: the reply from one turn is what the next turn's
// analysis reads. Without this a clarifying question is worse than useless — the agent asks,
// the user answers, and the agent has no idea what the answer refers to.
func TestTheAssistantKnowsWhatItAlreadySaid(t *testing.T) {
	e := chatFixture(t, "¿Qué carpeta quieres revisar?")

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
	if turns[0].Agent != "¿Qué carpeta quieres revisar?" {
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
		{User: "revisa el directorio", Agent: "¿qué directorio?", Kind: KindAsk},
		{User: "el actual", Agent: "voy", Kind: KindChat},
	})

	got := e.agent.dialogue()
	if !strings.Contains(got, "revisa el directorio") {
		t.Errorf("the prompt must carry what the user said: %q", got)
	}
	if !strings.Contains(got, "¿qué directorio?") {
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
		turns = append(turns, DialogueTurn{User: "mensaje numero " + string(rune('A'+i%26)), Agent: "respuesta", Kind: KindChat})
	}
	e.agent.SetTranscript(turns)

	got := e.agent.dialogue()
	lines := strings.Count(got, "\n")
	if lines > 30 {
		t.Errorf("the transcript must be bounded, got %d lines", lines)
	}
	// The tail is what a short answer refers to, so the MOST RECENT turn must survive.
	if !strings.Contains(got, "respuesta") {
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
	e.agent.note(taskOf("cuenta los archivos del directorio actual"), "72 files", KindTask)

	got := e.agent.dialogue()
	if !strings.Contains(got, "cuenta los archivos") {
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
	e.agent.SetTranscript([]DialogueTurn{{User: "uno", Kind: KindChat}})
	e.agent.SetTranscript([]DialogueTurn{{User: "dos", Kind: KindChat}})

	turns := e.agent.Transcript()
	if len(turns) != 1 || turns[0].User != "dos" {
		t.Errorf("SetTranscript must replace, got %+v", turns)
	}
}

// TestTranscriptIsCopiedOut: the returned slice must not alias the internal one, or a caller
// holding it would see it change under them as the turn appends.
func TestTranscriptIsCopiedOut(t *testing.T) {
	e := chatFixture(t, "ok")
	e.agent.SetTranscript([]DialogueTurn{{User: "uno", Kind: KindChat}})

	out := e.agent.Transcript()
	out[0].User = "mutado"

	if e.agent.Transcript()[0].User != "uno" {
		t.Error("the transcript must be copied out, not aliased")
	}
}
