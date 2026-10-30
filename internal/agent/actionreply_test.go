package agent

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
)

// TestActionSchemaDescribesAction: the schema a provider enforces must name the fields Action
// reads, or an enforced reply would decode into an empty round.
func TestActionSchemaDescribesAction(t *testing.T) {
	var schema struct {
		Properties map[string]json.RawMessage `json:"properties"`
		Required   []string                   `json:"required"`
	}
	if err := json.Unmarshal(actionSchema, &schema); err != nil {
		t.Fatalf("the schema is not JSON: %v", err)
	}
	for _, field := range []string{"reasoning", "actions", "final_action", "notes", "done"} {
		if _, ok := schema.Properties[field]; !ok {
			t.Errorf("the schema does not name %q", field)
		}
	}
	if strings.Join(schema.Required, ",") != "reasoning,actions,done" {
		t.Errorf("required = %q", schema.Required)
	}
}

func TestDecodeActionReadsTheObject(t *testing.T) {
	a, err := decodeAction(`{"reasoning":"r","actions":[{"kind":"command","command":"ls"}],"done":true}`)
	if err != nil || !a.isDone() || len(a.Actions) != 1 || a.Actions[0].Command != "ls" {
		t.Fatalf("action = %+v, %v", a, err)
	}
}

// TestDecodeActionSalvagesWhatARealRunSent replays the shapes that cost a real session whole
// rounds: a bare list of commands, and commands written as pseudo tool calls.
func TestDecodeActionSalvagesWhatARealRunSent(t *testing.T) {
	a, err := decodeAction(`[{"kind":"command","description":"Add the env var","command":"go test ./internal/config/"}]`)
	if err != nil || a.isDone() || len(a.Actions) != 1 || a.Actions[0].Command != "go test ./internal/config/" {
		t.Fatalf("a list of commands: action = %+v, %v", a, err)
	}
	// Nothing before the list: no reasoning, and never the old marker shown as one.
	if a.Reasoning != "" || a.Salvaged != shapeList {
		t.Errorf("reasoning = %q, shape = %q", a.Reasoning, a.Salvaged)
	}
	a, _ = decodeAction("I will run the tests.\n  Then commit.\n[{\"command\":\"go test ./...\"}]")
	if a.Reasoning != "I will run the tests. Then commit." {
		t.Errorf("prose before a list: reasoning = %q", a.Reasoning)
	}

	a, err = decodeAction(
		"Reading the helper first.\n<invoke name=\"bash\">\n<parameter name=\"command\">cd /w && grep -n 'func newFakeTUI' " +
			"internal/tui/*_test.go</parameter>\n" +
			"<parameter name=\"description\">Locate the helper</parameter>\n</invoke>\n<invoke name=\"bash\"><parameter " +
			"name=\"command\">git status</parameter></invoke>",
	)
	if err != nil || a.isDone() || len(a.Actions) != 2 {
		t.Fatalf("pseudo tool calls: action = %+v, %v", a, err)
	}
	if a.Reasoning != "Reading the helper first." || a.Salvaged != shapeToolCalls {
		t.Errorf("reasoning = %q, shape = %q", a.Reasoning, a.Salvaged)
	}
	if a.Actions[0].Kind != "command" || a.Actions[0].Description != "Locate the helper" ||
		a.Actions[0].Command != "cd /w && grep -n 'func newFakeTUI' internal/tui/*_test.go" ||
		a.Actions[1].Command != "git status" {
		t.Errorf("commands = %+v", a.Actions)
	}
	// Each salvaged round has its own "done", so one cannot change another.
	b, _ := decodeAction(`[{"command":"ls"}]`)
	*b.Done = true
	if c, _ := decodeAction(`[{"command":"ls"}]`); c.isDone() {
		t.Error("salvaged rounds must not share their done flag")
	}
}

// TestProseBeforeIsOnlyProse: code before the commands is not shown as the model's thinking.
func TestProseBeforeIsOnlyProse(t *testing.T) {
	for _, tc := range []struct {
		text string
		at   int
		want string
	}{
		{"abc", -1, ""},
		{"abc", 0, ""},
		{"run `ls` first\nX", 14, ""},
		{"see {a} X", 8, ""},
		{"  two\n words X", 13, "two words"},
	} {
		if got := proseBefore(tc.text, tc.at); got != tc.want {
			t.Errorf("proseBefore(%q, %d) = %q, want %q", tc.text, tc.at, got, tc.want)
		}
	}
}

// TestDecodeActionStillRefusesWhatItCannotRead: nothing to run is the error it always was.
func TestDecodeActionStillRefusesWhatItCannotRead(t *testing.T) {
	for _, text := range []string{
		`<invoke name="x">` + "\n</invoke>",                // an empty pseudo call
		`[{"kind":"command","command":""}]`,                // a list with nothing to run
		`[1, 2]`,                                           // a list that is not commands
		"I will now read the file.",                        // prose
		`{"actions":"not a list"}`,                         // an object that does not fit
		`<invoke name="bash"><parameter name="command">ls`, // cut off
	} {
		if a, err := decodeAction(text); err == nil {
			t.Errorf("%q: want an error, got %+v", text, a)
		}
	}
}

// TestActionPhaseReadsASalvagedRound: a list of commands is a round of work, and its shape goes to
// the log, not to the reasoning the chat shows.
func TestActionPhaseReadsASalvagedRound(t *testing.T) {
	a := synthesisAgent(t, `[{"kind":"command","command":"ls"}]`)
	r := &taskRun{a: a, ctx: context.Background(), t: synthesisTask(t), mem: &workMemory{}}
	action, err := r.actionPhase()
	if err != nil || action.Salvaged != shapeList || action.Reasoning != "" || len(action.Actions) != 1 {
		t.Fatalf("action = %+v, %v", action, err)
	}
}
