package tui

import (
	"context"
	"strings"
	"testing"
)

// steeringRunner is a liveRunner whose task in flight takes messages.
type steeringRunner struct {
	*liveRunner
	taken []string
}

func (r *steeringRunner) Steer(text string) bool {
	if text == "refused" {
		return false
	}
	r.taken = append(r.taken, text)
	return true
}

// A message typed while a task runs goes to the task, shown as the person's, and the turn goes
// on; a message the task does not take is queued as before.
func TestAMessageTypedWhileATaskRunsSteersIt(t *testing.T) {
	r := &steeringRunner{liveRunner: newLiveRunner()}
	tu, keys := newLiveTUI(r)
	tu.charMode = true
	ctx := context.Background()

	tu.runTask(ctx, "first")
	waitOn(t, r.started, "the first task")
	tu.queue("also this")
	tu.queue("refused")
	if len(r.taken) != 1 || r.taken[0] != "also this" || tu.queued != "refused" {
		t.Fatalf("taken %v, queued %q", r.taken, tu.queued)
	}
	text := messageTexts(tu)
	if !strings.Contains(text, "also this") || !strings.Contains(text, "Added to the task in progress") {
		t.Errorf("the conversation must show the message and say it was added:\n%s", text)
	}

	got := driveLoop(ctx, tu)
	r.release <- runOutcome{result: "one"}
	waitOn(t, r.started, "the queued task")
	keys.Write([]byte("x"))
	waitOn(t, got, "the loop to return")
	go func() { r.release <- runOutcome{result: "two"} }()
	settle(t, tu, keys)
}

// The AppRunner steers only the agent of the task in flight.
func TestAppRunnerSteersTheLiveAgent(t *testing.T) {
	r := &AppRunner{}
	if r.Steer("x") {
		t.Fatal("no task is running")
	}
	ag := &steerableAgent{}
	r.live.set(ag)
	if !r.Steer("now") || ag.got != "now" {
		t.Fatalf("the live agent must take the message, got %q", ag.got)
	}
	r.live.set(&fakeAgent{})
	if r.Steer("x") {
		t.Fatal("an agent that cannot be steered reports false")
	}
}

type steerableAgent struct {
	fakeAgent
	got string
}

func (s *steerableAgent) Steer(text string) bool {
	s.got = text
	return true
}
