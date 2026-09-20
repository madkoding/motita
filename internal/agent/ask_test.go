package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/madkoding/starlight/internal/config"
)

// The analysis can report one question or a list of them, and both have to reach the interface
// in the same shape. These are the rules that fold the two into one.

func TestBuildQuestionsPrefersTheList(t *testing.T) {
	got := buildQuestions(Analysis{
		Question: "la única",
		Questions: []AskItem{
			{Text: "primera"},
			{Text: "segunda"},
		},
	})
	if len(got) != 2 {
		t.Fatalf("the list should win, got %d items", len(got))
	}
	if got[0].Text != "primera" || got[1].Text != "segunda" {
		t.Fatalf("the order should be the model's, got %+v", got)
	}
}

// A prompt that predates the list fills Question, and it must keep working: that is the whole
// reason the single field is still read.
func TestBuildQuestionsFallsBackToTheSingleQuestion(t *testing.T) {
	got := buildQuestions(Analysis{Question: "¿qué carpeta?", Assumption: "la actual"})
	if len(got) != 1 {
		t.Fatalf("the single question should become a list of one, got %d", len(got))
	}
	if got[0].Text != "¿qué carpeta?" || got[0].Assumption != "la actual" {
		t.Fatalf("got %+v", got[0])
	}
}

// Both filled with the same gap means the model said the same thing twice. The list is the one
// taken, so the user is not asked twice.
func TestBuildQuestionsDoesNotAskTheSameGapTwice(t *testing.T) {
	got := buildQuestions(Analysis{
		Question:  "¿qué carpeta?",
		Questions: []AskItem{{Text: "¿qué carpeta?"}},
	})
	if len(got) != 1 {
		t.Fatalf("the same gap should be asked once, got %d", len(got))
	}
}

// Nothing to ask is an empty result, NOT a list with an empty question: the caller treats an
// empty list as "this turn is not an ask at all".
func TestBuildQuestionsWithNothingIsEmpty(t *testing.T) {
	for _, a := range []Analysis{
		{},
		{Question: "   "},
		{Questions: []AskItem{{Text: ""}, {Text: "  "}}},
	} {
		if got := buildQuestions(a); len(got) != 0 {
			t.Fatalf("nothing to ask should be empty, got %+v", got)
		}
	}
}

// Blank entries inside the list are dropped rather than drawn as empty rows, and the surviving
// questions keep the model's order.
func TestBuildQuestionsSkipsBlanks(t *testing.T) {
	got := buildQuestions(Analysis{Questions: []AskItem{
		{Text: "primera"},
		{Text: "  "},
		{Text: "segunda"},
	}})
	if len(got) != 2 {
		t.Fatalf("blanks should be dropped, got %d", len(got))
	}
	if got[0].Text != "primera" || got[1].Text != "segunda" {
		t.Fatalf("order and content should hold, got %+v", got)
	}
}

// A list longer than the cap is cut. Past a few, the user is filling in a form rather than
// clarifying a request, and the request should have been read more generously first.
func TestBuildQuestionsIsCapped(t *testing.T) {
	long := make([]AskItem, 0, maxQuestions+3)
	for i := 0; i < maxQuestions+3; i++ {
		long = append(long, AskItem{Text: string(rune('a' + i))})
	}
	got := buildQuestions(Analysis{Questions: long})
	if len(got) != maxQuestions {
		t.Fatalf("the list should be capped at %d, got %d", maxQuestions, len(got))
	}
}

// An option is a one-line answer the user picks, so an option that cannot be picked is dropped:
// empty, multi-line, absurdly long, or a duplicate of one already offered.
func TestCleanOptions(t *testing.T) {
	cases := []struct {
		name string
		in   []string
		want int
	}{
		{"empty input", nil, 0},
		{"all blank", []string{"", "   ", "\t"}, 0},
		{"duplicates", []string{"sí", "sí", "no"}, 2},
		{"multi-line collapses to one row", []string{"sí\nno"}, 1},
		{"too long", []string{strings.Repeat("x", optionMaxRunes+1)}, 0},
		{"exactly the limit", []string{strings.Repeat("x", optionMaxRunes)}, 1},
		{"keeps the order", []string{"uno", "dos", "tres"}, 3},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := cleanOptions(c.in); len(got) != c.want {
				t.Fatalf("cleanOptions(%q) = %v, want %d", c.in, got, c.want)
			}
		})
	}
}

// The cap applies to what is kept, so a long list of duplicates or junk still yields at most a
// handful of choices.
func TestCleanOptionsIsCapped(t *testing.T) {
	in := make([]string, 0, maxOptions+4)
	for i := 0; i < maxOptions+4; i++ {
		in = append(in, strings.Repeat("o", i+1))
	}
	if got := cleanOptions(in); len(got) != maxOptions {
		t.Fatalf("cleanOptions should cap at %d, got %d", maxOptions, len(got))
	}
}

// Whitespace is collapsed so an option is one line, and trimming means " yes " and "yes" are the
// same choice rather than two identical-looking rows.
func TestCleanOptionsCollapsesAndTrims(t *testing.T) {
	got := cleanOptions([]string{"  la   carpeta   actual  "})
	if len(got) != 1 || got[0] != "la carpeta actual" {
		t.Fatalf("got %q", got)
	}
	dup := cleanOptions([]string{"sí", "  sí  "})
	if len(dup) != 1 {
		t.Fatalf("the trimmed forms are the same option, got %q", dup)
	}
}

// The options reach the result on the asking path, so the interface can draw them.
func TestOptionsReachTheResult(t *testing.T) {
	got := buildQuestions(Analysis{
		Question: "¿qué carpeta?",
		Options:  []string{"la actual", "/tmp"},
	})
	if len(got) != 1 || len(got[0].Options) != 2 {
		t.Fatalf("the options should survive into the question, got %+v", got)
	}
}

// The model tends to open its assumption with the same sentence the interface adds in front of
// it, and the two together read as a stutter. Found by running the real binary, not by reading
// the code: the window printed "Si no me dices otra cosa, asumiré: Si no me dices otra cosa,
// reviso el contenido de ./workspace".
func TestAssumptionLeadIsNotRepeated(t *testing.T) {
	cases := []struct{ in, want string }{
		{"Si no me dices otra cosa, reviso ./workspace", "reviso ./workspace"},
		{"si no me dices otra cosa, asumiré: usar la actual", "usar la actual"},
		{"Si no me dices lo contrario, lo borro", "lo borro"},
		{"Usaré la carpeta actual", "Usaré la carpeta actual"},
	}
	for _, c := range cases {
		if got := cleanAssumption(c.in); got != c.want {
			t.Errorf("cleanAssumption(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

// Only the LEAD is dropped: an assumption that mentions the phrase later is saying something,
// and rewriting the middle of a sentence would be guessing at its meaning.
func TestAssumptionLeadOnlyAtTheStart(t *testing.T) {
	const in = "reviso el directorio y, si no me dices otra cosa, lo dejo como está"
	if got := cleanAssumption(in); got != in {
		t.Fatalf("a mention in the middle must be left alone, got %q", got)
	}
}

// The cleaned assumption is the one that reaches the question, so the interface never has to
// think about the stutter again.
func TestAssumptionIsCleanedInTheQuestion(t *testing.T) {
	got := buildQuestions(Analysis{
		Question:   "¿qué carpeta?",
		Assumption: "Si no me dices otra cosa, asumiré: la actual",
	})
	if len(got) != 1 {
		t.Fatalf("one question expected, got %d", len(got))
	}
	if got[0].Assumption != "la actual" {
		t.Fatalf("Assumption = %q, want it without the repeated lead", got[0].Assumption)
	}
}

// The RESULT's assumption is the one summarise prints its own sentence in front of, so it has to
// be cleaned too. Cleaning only the questions left the stutter in the chat while the window was
// clean — which is how the fix was verified as incomplete on the i386 laptop: the window read
// "si no respondes: reviso el proyecto" and the conversation above it still read "Si no me dices
// otra cosa, asumiré: Si no me dices otra cosa, reviso el proyecto".
func TestResultAssumptionIsCleanedToo(t *testing.T) {
	const raw = "Si no me dices otra cosa, asumiré: reviso el proyecto"
	if got := cleanAssumption(raw); got != "reviso el proyecto" {
		t.Fatalf("cleanAssumption(%q) = %q", raw, got)
	}
	// The list and the single question must agree, or the window and the chat disagree about
	// the same assumption.
	a := Analysis{Question: "¿reviso qué?", Assumption: raw}
	items := buildQuestions(a)
	if len(items) != 1 || items[0].Assumption != "reviso el proyecto" {
		t.Fatalf("the question list should carry the cleaned assumption, got %+v", items)
	}
	if cleanAssumption(a.Assumption) != items[0].Assumption {
		t.Fatal("both paths must produce the same assumption")
	}
}

// Recognised by SHAPE, not by a list of phrasings. The first attempt listed the sentences and
// the very next run on the hardware produced one that was not on it ("Si no me aclaras nada,
// ordenaré los archivos..."), which is what settled the approach: a list would always be one
// phrasing behind.
func TestAssumptionConditionalIsRecognisedByShape(t *testing.T) {
	cases := []struct{ in, want string }{
		{"Si no me dices otra cosa, reviso ./workspace", "reviso ./workspace"},
		{"Si no me aclaras nada, ordenaré los archivos de ./workspace", "ordenaré los archivos de ./workspace"},
		{"si no me dices lo contrario, lo borro", "lo borro"},
		{"Si quieres, lo dejo como está", "lo dejo como está"},
		{"asumiré: usar la actual", "usar la actual"},
		{"Reviso el directorio actual", "Reviso el directorio actual"},
	}
	for _, c := range cases {
		if got := cleanAssumption(c.in); got != c.want {
			t.Errorf("cleanAssumption(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

// An assumption that is ENTIRELY a conditional is kept whole: dropping the clause would leave
// nothing to show, and an empty assumption is worse than a wordy one.
func TestAssumptionThatIsOnlyAConditionalIsKept(t *testing.T) {
	const in = "Si no me dices otra cosa,"
	if got := cleanAssumption(in); got != in {
		t.Fatalf("a bare conditional must be kept, got %q", got)
	}
	const noComma = "Si no me dices otra cosa"
	if got := cleanAssumption(noComma); got != noComma {
		t.Fatalf("a conditional with nothing after it must be kept, got %q", got)
	}
}

// Empty stays empty, and an empty assumption is normal: it means the agent has nothing to fall
// back on, and the interface then offers no "if you do not answer" line at all.
func TestCleanAssumptionHandlesEmpty(t *testing.T) {
	for _, in := range []string{"", "   ", "\n\t"} {
		if got := cleanAssumption(in); got != "" {
			t.Fatalf("cleanAssumption(%q) = %q, want empty", in, got)
		}
	}
}

// A turn that reports its gap ONLY as a list still has a question to ask, and must not be failed
// as a dead end. Found on the hardware: the run printed "task not understandable:" and then
// "error: 1 of 1 tasks failed" for a request the agent had two questions ready to ask about,
// because the single fields were empty and the dead-end check read only those.
func TestQuestionsListIsNotADeadEnd(t *testing.T) {
	// The shape the model produces for a list: no "question", no "assumption", only "questions".
	a := Analysis{
		Kind:           KindAsk,
		Understandable: false,
		Questions: []AskItem{
			{Text: "¿a dónde lo mando?", Assumption: "lo dejo listo sin enviar"},
			{Text: "¿qué orden uso?", Options: []string{"por fecha", "por nombre"}},
		},
	}
	items := buildQuestions(a)
	if len(items) != 2 {
		t.Fatalf("the list should reach the interface, got %d", len(items))
	}
	// The single fields are what the rest of the asking path reads, so they are derived here.
	question, assumption := a.Question, cleanAssumption(a.Assumption)
	if question == "" && len(items) > 0 {
		question = items[0].Text
	}
	if assumption == "" && len(items) > 0 {
		assumption = items[0].Assumption
	}
	if question == "" {
		t.Fatal("a list-only turn must still yield a question; otherwise the turn fails as a dead end")
	}
	if assumption != "lo dejo listo sin enviar" {
		t.Fatalf("the first question's assumption should stand in, got %q", assumption)
	}
}

// The end-to-end version of the bug found on the hardware: the model reports its gap ONLY as a
// list, the single fields stay empty, and the turn must NOT be failed as a dead end. Before the
// fix this printed "task not understandable:" and returned a failure for a request the agent had
// two questions about.
func TestAListOnlyAskIsNotFailed(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			Messages []struct {
				Content string `json:"content"`
			} `json:"messages"`
		}
		json.NewDecoder(r.Body).Decode(&req)
		var text string
		for _, m := range req.Messages {
			text += m.Content
		}
		if strings.Contains(text, "## ANALYSIS OF THE TASK") {
			// Exactly the shape a list-only answer takes: no "question", no "assumption".
			fmt.Fprint(w, `{"choices":[{"message":{"content":"{\"kind\":\"ask\",\"understandable\":false,\"questions\":[{\"text\":\"¿a dónde lo mando?\",\"assumption\":\"lo dejo listo sin enviar\"},{\"text\":\"¿qué orden uso?\",\"options\":[\"por fecha\",\"por nombre\"]}]}"}}]}`)
			return
		}
		fmt.Fprint(w, `{"choices":[{"message":{"content":"{}"}}]}`)
	}))
	defer srv.Close()

	// Interactive, because that is the case where the turn ends by ASKING. Without a user the
	// agent is designed to proceed on its assumption, so the run would go on to the planner and
	// this test would be measuring the wrong path.
	e := mount(t, srv, config.Anchor{Kind: "command", Command: "true", Timeout: 5 * time.Second}, nil)
	e.agent.Interactive = true
	var got TaskResult
	e.agent.SetObserver(func(tr TaskResult) { got = tr })
	if err := e.agent.Run(context.Background()); err != nil {
		t.Fatalf("a list-only ask must not fail the run: %v", err)
	}
	if !got.NeedsInput {
		t.Fatal("the turn should be asking")
	}
	if got.Question == "" {
		t.Fatal("the question must be derived from the list, or the interface has nothing to show")
	}
	if len(got.Questions) != 2 {
		t.Fatalf("both questions should reach the interface, got %d", len(got.Questions))
	}
	if got.Assumption == "" {
		t.Fatal("the first question's assumption should stand in for the empty field")
	}
}
