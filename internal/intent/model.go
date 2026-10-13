package intent

import (
	"context"
	"encoding/json"
	"strings"
	"time"
)

// SourceModel means the rules matched nothing and a model call picked the mode.
const SourceModel = "model"

// ModelTimeout bounds the model call. It runs before the turn starts, so the person is waiting
// on it; a slow answer falls back to the rules' default instead of holding the turn.
const ModelTimeout = 15 * time.Second

// ModelSystem is the instruction of the model call. It is short on purpose: the call decides
// one word, and every token of it is spent before every ambiguous turn.
const ModelSystem = `You route a user's message to a coding agent. Answer with JSON only: {"kind":"plan"} or {"kind":"task"}.
"plan": the user wants an answer, an explanation, a review or a design, and nothing in the project should change.
"task": the user wants something done or changed in the project; also greetings, thanks and small talk.
When unsure, answer "plan": planning when work was wanted costs one message, working when a plan was wanted changes files.`

// Completer asks a model for one completion of a system and a user message.
type Completer func(ctx context.Context, system, user string) (string, error)

// Refine asks the model to decide what the rules could not. A decision the rules made, or an
// explicit one, is returned as it is: the model is the fallback, never an override, so the
// cheap and explainable path stays the one that runs whenever it can. Any failure of the call
// (an error, a timeout, an answer that is not one of the two kinds) keeps the rules' default.
func Refine(ctx context.Context, d Decision, complete Completer) Decision {
	if d.Source != SourceDefault || complete == nil {
		return d
	}
	ctx, cancel := context.WithTimeout(ctx, ModelTimeout)
	defer cancel()
	text, err := complete(ctx, ModelSystem, d.Prompt)
	if err != nil {
		return d
	}
	kind, ok := modelKind(text)
	if !ok {
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
	return next.Source == SourceExplicit && prev.Source != SourceExplicit && prev.Source != "" &&
		next.Kind != prev.Kind && strings.TrimSpace(next.Prompt) == strings.TrimSpace(prev.Prompt)
}
