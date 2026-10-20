// Package repeats finds the procedures a session repeated: the same run of commands, in more than
// one turn. It reads what the session DID (the commands, as the checkpoints keep them) and never
// what it said, so it needs no model and costs nothing; only when it finds something does the
// skill review get asked to write it down.
package repeats

import (
	"fmt"
	"sort"
	"strings"
)

// A run of commands is a procedure when it has at least MinSteps of them and shows up in at
// least MinTurns different turns. Longer than MaxSteps is not looked for: a run that long that
// repeats exactly is a script, and its prefix is found already.
const (
	MinSteps = 2
	MaxSteps = 5
	MinTurns = 2
)

// maxAsks is how many of the requests a pattern served are kept as context for the review.
const maxAsks = 3

// Turn is one turn of a session: what was asked, and the command lines it ran, in order.
type Turn struct {
	Ask   string
	Steps []string
}

// Pattern is a run of commands that came up in several turns.
type Pattern struct {
	// Steps are the commands as they were first seen.
	Steps []string
	// Turns is in how many different turns the run came up.
	Turns int
	// Asks are some of the requests those turns were answering.
	Asks []string
}

// Detect finds the repeated runs of commands, the most repeated and then the longest first. A
// run that is only part of a longer one found in the same turns is left out, so one procedure is
// reported once and not as each of its pieces.
func Detect(turns []Turn) []Pattern {
	type seen struct {
		steps []string
		turns map[int]bool
		asks  []string
	}
	found := map[string]*seen{}
	var order []string
	for ti, t := range turns {
		keys := make([]string, len(t.Steps))
		for i, s := range t.Steps {
			keys[i] = shape(s)
		}
		for n := MinSteps; n <= MaxSteps; n++ {
			for i := 0; i+n <= len(keys); i++ {
				key := strings.Join(keys[i:i+n], "\n")
				p := found[key]
				if p == nil {
					p = &seen{steps: append([]string(nil), t.Steps[i:i+n]...), turns: map[int]bool{}}
					found[key] = p
					order = append(order, key)
				}
				if !p.turns[ti] {
					p.turns[ti] = true
					if len(p.asks) < maxAsks {
						p.asks = append(p.asks, t.Ask)
					}
				}
			}
		}
	}
	var out []Pattern
	for _, key := range order {
		if p := found[key]; len(p.turns) >= MinTurns && !distinct(p.steps) {
			out = append(out, Pattern{Steps: p.steps, Turns: len(p.turns), Asks: p.asks})
		}
	}
	out = dropPieces(out)
	sort.SliceStable(out, func(a, b int) bool {
		if out[a].Turns != out[b].Turns {
			return out[a].Turns > out[b].Turns
		}
		return len(out[a].Steps) > len(out[b].Steps)
	})
	return out
}

// distinct says a run is one command over and over: a loop, not a procedure.
func distinct(steps []string) bool {
	first := shape(steps[0])
	for _, s := range steps[1:] {
		if shape(s) != first {
			return false
		}
	}
	return true
}

// dropPieces removes the patterns that are a stretch of a longer one found in as many turns.
func dropPieces(ps []Pattern) []Pattern {
	var out []Pattern
	for i, p := range ps {
		piece := false
		for j, q := range ps {
			if i != j && q.Turns == p.Turns && len(q.Steps) > len(p.Steps) && contains(q.Steps, p.Steps) {
				piece = true
				break
			}
		}
		if !piece {
			out = append(out, p)
		}
	}
	return out
}

// contains says inner is a run of consecutive steps of outer, compared by shape.
func contains(outer, inner []string) bool {
	for i := 0; i+len(inner) <= len(outer); i++ {
		match := true
		for k := range inner {
			if shape(outer[i+k]) != shape(inner[k]) {
				match = false
				break
			}
		}
		if match {
			return true
		}
	}
	return false
}

// shape is a command with what changes between two runs of the same step taken out: it keeps the
// program and its first argument (and the script's name after "run"), so "go test ./a" and "go
// test ./b" are one step and "git status" and "git commit" are not.
func shape(cmd string) string {
	f := strings.Fields(strings.ToLower(cmd))
	n := 2
	if len(f) > 2 && f[1] == "run" {
		n = 3
	}
	return strings.Join(f[:min(n, len(f))], " ")
}

// Summary is the patterns as the text the review reads: the steps of each, how often they came
// up and what they were for. Empty when there is none.
func Summary(ps []Pattern) string {
	var b strings.Builder
	for i, p := range ps {
		fmt.Fprintf(&b, "### Pattern %d (repeated in %d turns)\n", i+1, p.Turns)
		for _, s := range p.Steps {
			fmt.Fprintf(&b, "- %s\n", firstLine(s))
		}
		for _, a := range p.Asks {
			fmt.Fprintf(&b, "  asked: %s\n", firstLine(a))
		}
		b.WriteByte('\n')
	}
	return b.String()
}

// firstLine is the first line of s, cut so a pasted log does not flood the summary.
func firstLine(s string) string {
	line, _, _ := strings.Cut(strings.TrimSpace(s), "\n")
	if r := []rune(line); len(r) > 200 {
		return string(r[:200]) + "…"
	}
	return line
}
