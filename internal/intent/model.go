package intent

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"sync"
	"time"
)

// SourceModel means the rules matched nothing and a model call picked the mode.
const SourceModel = "model"

// ModelTimeout bounds the model call. It runs before the turn starts, so the person is waiting
// on it; a slow answer falls back to the rules' default instead of holding the turn.
const ModelTimeout = 10 * time.Second

// ReasonNoModel is the Reason of a message the rules could not place and the model did not
// answer for (an error, a timeout, an answer that is not a kind): the person reads that the
// guess is the bare default, and that switching the mode is likely worth it.
const ReasonNoModel = "default, the model did not answer"

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
// one word, and every token of it is spent before every ambiguous turn.
const ModelSystem = "You route a user's message to a coding agent. " +
	`Answer with JSON only: {"kind":"plan"} or {"kind":"task"}.` + "\n" +
	`"plan": the user wants an answer, an explanation, a review or a design, ` +
	"or reports a problem without saying what to do, " +
	"and nothing in the project should change yet.\n" +
	`"task": the user wants something done or changed in the project; ` +
	"also greetings, thanks and small talk.\n" +
	`When unsure, answer "plan": planning when work was wanted costs one message, ` +
	"working when a plan was wanted changes files."

// Completer asks a model for one completion of a system and a user message.
type Completer func(ctx context.Context, system, user string) (string, error)

// Refine asks the model to decide what the rules could not. A decision the rules made, or an
// explicit one, is returned as it is: the model is the fallback, never an override, so the
// cheap and explainable path stays the one that runs whenever it can. Any failure of the call
// (an error, a timeout, an answer that is not one of the two kinds) keeps the rules' default,
// with ReasonNoModel so the person can tell that guess from one the model made.
func Refine(ctx context.Context, d Decision, complete Completer) Decision {
	if d.Source != SourceDefault || complete == nil {
		return d
	}
	ctx, cancel := context.WithTimeout(ctx, ModelTimeout)
	defer cancel()
	text, err := complete(ctx, ModelSystem, d.Prompt)
	kind, ok := modelKind(text)
	if err != nil || !ok {
		d.Reason = ReasonNoModel
		return d
	}
	return Decision{kind, d.Prompt, SourceModel, "model"}
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
