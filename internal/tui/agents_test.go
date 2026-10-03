package tui

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/madkoding/motita/internal/agent"
	"github.com/madkoding/motita/internal/llm"
)

// agentsRunner is a runner whose task reports a list of agents before it answers, the way a run
// that started background agents does.
type agentsRunner struct {
	*fakeRunner
	lines []string
}

func (r *agentsRunner) RunTask(ctx context.Context, task string, progress func(string, ...any)) (string, error) {
	for _, l := range r.lines {
		progress("%s", l)
	}
	return r.fakeRunner.RunTask(ctx, task, progress)
}

var agentsT0 = time.Date(2026, 10, 3, 15, 0, 0, 0, time.UTC)

func sampleAgents() []agent.AgentInfo {
	return []agent.AgentInfo{
		{ID: "main", Purpose: "add the counter", State: agent.AgentRunning, Started: agentsT0,
			Tokens: llm.Usage{Input: 30000, Output: 2000}, Round: 7, Activity: "running: go test ./..."},
		{ID: "a1", Parent: "main", Purpose: "write the web tests", State: agent.AgentRunning,
			Started: agentsT0.Add(30 * time.Second), Tokens: llm.Usage{Input: 12000, Output: 4200}, Round: 3},
		{ID: "a2", Parent: "main", Purpose: "update the docs", State: agent.AgentPassed, Started: agentsT0,
			Finished: agentsT0.Add(83 * time.Second), Tokens: llm.Usage{Input: 900}, Branch: "motita/sub/a2",
			Summary: "README and REFERENCE updated\nsecond line"},
		{ID: "a3", Parent: "main", Purpose: "try the old API", State: agent.AgentFailed, Started: agentsT0,
			Finished: agentsT0.Add(time.Second)},
		{ID: "a4", Parent: "main", Purpose: "abandoned idea", State: agent.AgentCancelled, Started: agentsT0,
			ElapsedMS: 5000},
	}
}

func agentsTUI(agents []agent.AgentInfo) *TUI {
	tui := newFakeTUI("", &fakeRunner{})
	tui.agents = agents
	tui.clock = func() time.Time { return agentsT0.Add(2 * time.Minute) }
	return tui
}

func TestHumanTokens(t *testing.T) {
	for n, want := range map[int64]string{0: "0", 950: "950", 48210: "48.2k", 1_300_000: "1.3M"} {
		if got := humanTokens(n); got != want {
			t.Errorf("humanTokens(%d) = %q, want %q", n, got, want)
		}
	}
}

func TestElapsedText(t *testing.T) {
	now := agentsT0.Add(2 * time.Minute)
	cases := []struct {
		a    agent.AgentInfo
		want string
	}{
		{agent.AgentInfo{State: agent.AgentRunning, Started: agentsT0}, "2m00s"},                                          // live clock
		{agent.AgentInfo{State: agent.AgentPassed, Started: agentsT0, Finished: agentsT0.Add(83 * time.Second)}, "1m23s"}, // fixed at its end
		{agent.AgentInfo{State: agent.AgentCancelled, ElapsedMS: 5400}, "5s"},                                             // only the snapshot's figure
		{agent.AgentInfo{State: agent.AgentRunning, Started: now.Add(time.Minute)}, "0s"},                                 // a clock behind the agent's
		{agent.AgentInfo{State: agent.AgentPassed, Started: agentsT0, Finished: agentsT0.Add(2*time.Hour + 5*time.Minute)}, "2h05m"},
	}
	for _, c := range cases {
		if got := elapsedText(c.a, now); got != c.want {
			t.Errorf("elapsedText(%+v) = %q, want %q", c.a, got, c.want)
		}
	}
}

func TestTheWallClockIsTheDefault(t *testing.T) {
	tui := &TUI{}
	if d := time.Since(tui.timeNow()); d < 0 || d > time.Minute {
		t.Fatalf("timeNow is %v away from the wall clock", d)
	}
}

func TestAgentsLabel(t *testing.T) {
	if got := agentsTUI(nil).agentsLabel(); got != "" {
		t.Errorf("no agents: %q, want nothing", got)
	}
	if got := agentsTUI([]agent.AgentInfo{{ID: "main"}}).agentsLabel(); got != "" {
		t.Errorf("a main agent that spent nothing: %q, want nothing", got)
	}
	alone := agentsTUI([]agent.AgentInfo{{ID: "main", Tokens: llm.Usage{Input: 1500}}}).agentsLabel()
	if alone != "1.5k tok" {
		t.Errorf("main alone = %q", alone)
	}
	if got := agentsTUI(sampleAgents()).agentsLabel(); got != glyphAgents+" 1 running "+glyphMid+" 49.1k tok" {
		t.Errorf("with background agents = %q", got)
	}
}

func TestAgentsPanelDrawsEveryStateAndFitsTheWidth(t *testing.T) {
	tui := agentsTUI(sampleAgents())
	lines := tui.agentsLines(tui.bodyWidth())
	text := strings.Join(lines, "\n")
	for _, want := range []string{
		"Agents", "main " + glyphMid + " add the counter", "2m00s", "32.0k tok", "r7", "running: go test",
		"write the web tests", "1m30s", "16.2k tok", "r3",
		glyphPassed + " update the docs", "1m23s", "motita/sub/a2 " + glyphMid + " README and REFERENCE updated",
		glyphFailed + " try the old API", glyphCancelled + " abandoned idea", "5s",
	} {
		if !strings.Contains(text, want) {
			t.Errorf("the panel lacks %q:\n%s", want, text)
		}
	}
	if strings.Contains(text, "second line") {
		t.Errorf("only the first line of a summary belongs in the panel:\n%s", text)
	}
	for _, narrow := range []int{80, 30, 12} {
		for _, l := range tui.agentsLines(narrow) {
			if visibleLen(l) > narrow+leftMargin {
				t.Errorf("width %d: a row overflows (%d): %q", narrow, visibleLen(l), l)
			}
		}
	}
}

func TestAgentsPanelWithoutAgentsSaysSo(t *testing.T) {
	tui := agentsTUI(nil)
	if text := strings.Join(tui.agentsLines(60), "\n"); !strings.Contains(text, "none yet") {
		t.Fatalf("an empty panel must say so:\n%s", text)
	}
}

func TestAgentsPanelCappedKeepsTheHeaderAndTheNewest(t *testing.T) {
	tui := agentsTUI(sampleAgents())
	all := tui.agentsLinesCapped(0)
	if len(all) != tui.agentsRows() {
		t.Fatalf("uncapped = %d rows, agentsRows = %d", len(all), tui.agentsRows())
	}
	capped := tui.agentsLinesCapped(3)
	if len(capped) != 3 || !strings.Contains(capped[0], "Agents") || capped[2] != all[len(all)-1] {
		t.Fatalf("capped:\n%s", strings.Join(capped, "\n"))
	}
}

func TestAgentGlyphsInColour(t *testing.T) {
	tui := agentsTUI(nil)
	tui.NoColor = false
	for _, st := range []string{agent.AgentPassed, agent.AgentFailed, agent.AgentCancelled, agent.AgentRunning} {
		if g := tui.agentGlyph(agent.AgentInfo{State: st}); !strings.Contains(g, "\x1b[") {
			t.Errorf("%s: %q has no colour", st, g)
		}
	}
}

func TestATaskReportingAgentsShowsThemInTheFooterNotInTheChat(t *testing.T) {
	runner := &agentsRunner{fakeRunner: &fakeRunner{}, lines: []string{agent.AgentsLine(sampleAgents())}}
	tui := newFakeTUI("my task\n\n", runner)
	tui.Run(context.Background())
	for _, m := range tui.messages {
		if strings.Contains(m.Text, agent.AgentsPrefix) {
			t.Fatalf("the snapshot reached the conversation: %q", m.Text)
		}
	}
	if len(tui.agents) != 5 {
		t.Fatalf("agents = %d, want 5", len(tui.agents))
	}
	if !strings.Contains(outputOf(tui), glyphAgents+" 1 running") {
		t.Errorf("the footer does not count the running agents:\n%s", outputOf(tui))
	}
}

func TestCtrlGTogglesThePanelAndKeepsTheDraft(t *testing.T) {
	tui := agentsTUI(sampleAgents())
	tui.draft = "half a sentence"
	if _, dispatch, handled := tui.handleLiveKey(keyAgents); dispatch || !handled {
		t.Fatalf("Ctrl+G: dispatch=%v handled=%v", dispatch, handled)
	}
	if !tui.agentsOpen || tui.draft != "half a sentence" {
		t.Fatalf("open=%v draft=%q", tui.agentsOpen, tui.draft)
	}
	if tui.popupRows() != tui.agentsRows() {
		t.Fatalf("popupRows = %d, want the panel's %d", tui.popupRows(), tui.agentsRows())
	}
	frame, _ := tui.layout(80, 40)
	if !strings.Contains(strings.Join(frame, "\n"), "write the web tests") {
		t.Fatalf("the open panel is not drawn:\n%s", strings.Join(frame, "\n"))
	}
	// Typing a command takes the slot back: the popup is what that keystroke is about.
	tui.draft = "/ag"
	if tui.showingAgents() || tui.popupRows() != len(completions("/ag")) {
		t.Fatalf("the completion popup must win over the panel")
	}
	tui.draft = ""
	tui.handleLiveKey(keyAgents)
	if tui.agentsOpen {
		t.Fatal("a second Ctrl+G must close the panel")
	}
}

func TestTheAgentsCommandOnAnEmptyRunExplains(t *testing.T) {
	tui := agentsTUI(nil)
	commandActions["/agents"](tui, context.Background(), "")
	if !tui.agentsOpen {
		t.Fatal("/agents must open the panel")
	}
	last := tui.messages[len(tui.messages)-1].Text
	if !strings.Contains(last, "No agents yet") {
		t.Fatalf("an empty panel opened by hand must explain itself, got %q", last)
	}
}

func TestPlanAndFollowedRunsTakeTheSnapshotToo(t *testing.T) {
	tui := agentsTUI(nil)
	tui.addMessage(AuthorAgent, "")
	stream := &planStream{tui: tui, pendingIdx: 0}
	stream.handle(agent.AgentsLine(sampleAgents()[:2]))
	if len(tui.agents) != 2 || stream.text != "" {
		t.Fatalf("plan: agents=%d text=%q", len(tui.agents), stream.text)
	}
	before := len(tui.messages)
	tui.addProgress(agent.AgentsLine(sampleAgents()[:1]))
	if len(tui.agents) != 1 || len(tui.messages) != before {
		t.Fatalf("followed run: agents=%d messages %d -> %d", len(tui.agents), before, len(tui.messages))
	}
}
