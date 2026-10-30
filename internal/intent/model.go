package intent

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"sync"
	"time"
)

// SourceModel means the model picked the mode.
const SourceModel = "model"

// SourceFallback means the model was asked and did not decide, so the rules' guess was kept; its
// Note says so, so that guess is not mistaken for the model's.
const SourceFallback = "fallback"

// ModelTimeout bounds the model call. It runs before the turn starts, so the person is waiting
// on it; a slow answer falls back to the rules' guess instead of holding the turn. 15 seconds
// was too short for the claude-code provider, which starts the claude CLI for every call: the
// call timed out and every message became the rules' guess (reported from real use as "it
// cannot tell whether I ask for a plan or a task"). Gate keeps that wait to one turn when the
// provider keeps failing.
const ModelTimeout = 30 * time.Second

// NoteNoModel ends the Note of a fallback (an error, a timeout, an answer that is not a kind):
// the person reads that the rules guessed, and that switching the mode may be worth it.
const NoteNoModel = "the model did not decide"

// ModelRest is how long the model is not asked again after a call failed. A provider that times
// out once ("context deadline exceeded") usually does it again, and every ask holds the turn for
// up to ModelTimeout before it starts: one slow turn is enough to learn that.
const ModelRest = 5 * time.Minute

// ErrResting is the answer of a Gate while the model rests after a failure.
var ErrResting = errors.New("auto mode: the model failed recently and is not asked for a while")

// Gate stops asking the model for ModelRest after a call failed. The zero value is ready.
type Gate struct {
	mu    sync.Mutex
	until time.Time
	// Now is the clock; nil is time.Now. A test sets it.
	Now func() time.Time
}

func (g *Gate) now() time.Time {
	if g.Now != nil {
		return g.Now()
	}
	return time.Now()
}

// Wrap is complete behind the gate: refused with ErrResting while the model rests, and putting it
// to rest when it fails. A nil complete stays nil, so Refine still knows there is no model.
func (g *Gate) Wrap(complete Completer) Completer {
	if complete == nil {
		return nil
	}
	return func(ctx context.Context, system, user string) (string, error) {
		g.mu.Lock()
		resting := g.now().Before(g.until)
		g.mu.Unlock()
		if resting {
			return "", ErrResting
		}
		out, err := complete(ctx, system, user)
		if err != nil {
			g.mu.Lock()
			g.until = g.now().Add(ModelRest)
			g.mu.Unlock()
		}
		return out, err
	}
}

// ModelSystem is the instruction of the model call. It is short on purpose: the call decides
// one word, and every token of it is spent before every turn auto mode routes.
const ModelSystem = "You route a user's message to a coding agent. " +
	`Answer with JSON only: {"kind":"plan"} or {"kind":"task"}.` + "\n" +
	`"plan": the user asks a question, wants an explanation, a review or a design, ` +
	"or says not to change anything yet.\n" +
	`"task": the user wants something done or changed in the project; ` +
	"also greetings, thanks and small talk.\n" +
	`A wish is an order ("I need it to...", "quiero que...", ` + // spanish-fixture: what the model reads
	`"it should have..."), ` +
	`a report that something is broken or missing is an order to fix it, ` +
	`and so is a go-ahead ("ok", "dale"): "task". ` +
	`Being told not to change anything, or asked to make a plan or a proposal, is "plan".` + "\n" +
	`When unsure, answer "task": a plan cannot run commands or change files, so planning ` +
	"when work was wanted leaves the person with nothing done, while the work is kept " +
	"in a checkpoint they can go back to."

// Completer asks a model for one completion of a system and a user message.
type Completer func(ctx context.Context, system, user string) (string, error)

// Refine asks the model whether the message wants a plan or a task, and the model decides: word
// lists cannot follow every way people ask (reported from real use: "auto mode should ask the
// cheap model, not decide it with ordinary ifs"). Every message that does not name its mode is
// asked about, whatever the rules guessed. The rules' guess in d is what is kept when there is no
// model or the call fails (an error, a timeout, a resting Gate, an answer that is not one of the
// two kinds), as SourceFallback. An explicit /plan or /task, or an empty message, is never asked.
//
// A go-ahead the model also reads as a task keeps ReasonGoAhead, so the plan on the table is the
// one carried out.
func Refine(ctx context.Context, d Decision, complete Completer) Decision {
	if d.Source == SourceExplicit || complete == nil || strings.TrimSpace(d.Prompt) == "" {
		return d
	}
	ctx, cancel := context.WithTimeout(ctx, ModelTimeout)
	defer cancel()
	text, err := complete(ctx, ModelSystem, d.Prompt)
	kind, ok := modelKind(text)
	if err != nil || !ok {
		d.Source = SourceFallback
		return d
	}
	reason := "model"
	if kind == KindTask && d.Reason == ReasonGoAhead {
		reason = ReasonGoAhead
	}
	return Decision{kind, d.Prompt, SourceModel, reason}
}

// modelKind reads the model's answer: the JSON object it was asked for, found inside whatever
// text surrounds it.
func modelKind(text string) (string, bool) {
	start, end := strings.Index(text, "{"), strings.LastIndex(text, "}")
	if start < 0 || end < start {
		return "", false
	}
	var answer struct {
		Kind string `json:"kind"`
	}
	if json.Unmarshal([]byte(text[start:end+1]), &answer) != nil {
		return "", false
	}
	switch kind := strings.ToLower(strings.TrimSpace(answer.Kind)); kind {
	case KindPlan, KindTask:
		return kind, true
	default:
		return "", false
	}
}

// Corrects reports whether next is the person overriding prev: the same message sent again with
// an explicit /plan or /task that names the other mode. It is how a wrong guess is counted, and
// what the word lists are tuned from.
func Corrects(prev, next Decision) bool {
	sameMessage := strings.TrimSpace(next.Prompt) == strings.TrimSpace(prev.Prompt)
	return overridesGuess(prev, next) && next.Kind != prev.Kind && sameMessage
}

// overridesGuess reports whether next was explicit where prev was a guess (by the rules or the
// model) rather than an explicit mode or nothing at all.
func overridesGuess(prev, next Decision) bool {
	return next.Source == SourceExplicit && prev.Source != SourceExplicit && prev.Source != ""
}
