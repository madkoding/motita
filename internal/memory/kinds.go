package memory

import (
	"strings"
	"time"
)

// The memory works like a brain: every entry is one KIND of memory, and entries are tied to each
// other by named ASSOCIATIONS. Both are front matter properties, so Obsidian shows them as they are
// and the person reads them in plain words:
//
//	---
//	type: decision
//	replaces: "[[Old rollback plan]]"
//	related: "[[Deploys]], [[Staging]]"
//	---

// The kinds of memory an entry can be.
const (
	KindFact       = "fact"       // what is true of the person's world
	KindEpisode    = "episode"    // what happened, and when
	KindDecision   = "decision"   // what was chosen, and why
	KindPreference = "preference" // how the person likes things done
	KindPerson     = "person"     // who someone is and what matters to them
	KindProcedure  = "procedure"  // how something is done
	KindLesson     = "lesson"     // a trap or a command of one project
)

// Kinds lists every kind, in the order the map's legend names them.
var Kinds = []string{KindFact, KindEpisode, KindDecision, KindPreference, KindPerson, KindProcedure, KindLesson}

// kindAliases are the words a model or a person may write for a kind, in English and Spanish.
var kindAliases = map[string]string{
	"hecho": KindFact, "dato": KindFact, "semantic": KindFact,
	"episodio": KindEpisode, "event": KindEpisode, "evento": KindEpisode, "episodic": KindEpisode,
	"decision": KindDecision, "decisión": KindDecision,
	"preferencia": KindPreference,
	"persona":     KindPerson, "people": KindPerson, "personas": KindPerson,
	"procedimiento": KindProcedure, "how-to": KindProcedure,
	"leccion": KindLesson, "lección": KindLesson, "lecciones": KindLesson,
}

// NormalizeKind reads a kind as written ("Decisión", "facts", "lesson"), "" when it is none of them.
func NormalizeKind(s string) string {
	s = strings.ToLower(strings.Trim(strings.TrimSpace(s), "\"'"))
	for _, k := range Kinds {
		if s == k || s == k+"s" {
			return k
		}
	}
	return kindAliases[s]
}

// The associations an entry can have with another one. RelRelated is the plain one, the others say
// how the two are tied.
const (
	RelRelated     = "related"
	RelReplaces    = "replaces"
	RelContradicts = "contradicts"
	RelDependsOn   = "depends_on"
	RelCausedBy    = "caused_by"
	RelPartOf      = "part_of"
	RelExampleOf   = "example_of"
)

// RelationKinds lists every association.
var RelationKinds = []string{RelRelated, RelReplaces, RelContradicts, RelDependsOn, RelCausedBy, RelPartOf, RelExampleOf}

// Relation is one association an entry states: the kind and the entry it leads to, as written.
type Relation struct {
	Kind   string `json:"kind"`
	Target string `json:"target"`
}

// relationKey is the association a front matter key names ("depends-on", "Depends_On"), "" for none.
func relationKey(key string) string {
	key = strings.ReplaceAll(strings.ToLower(strings.TrimSpace(key)), "-", "_")
	for _, k := range RelationKinds {
		if key == k {
			return k
		}
	}
	return ""
}

// relationTargets reads the entries a property names: its [[links]], or its comma separated names
// when it has none.
func relationTargets(value string) []string {
	var out []string
	for _, l := range ParseLinks(value) {
		out = append(out, l.Target)
	}
	if len(out) > 0 {
		return out
	}
	for _, t := range strings.Split(strings.Trim(value, "[]\"'"), ",") {
		if t = strings.Trim(strings.TrimSpace(t), "\"'"); t != "" {
			out = append(out, t)
		}
	}
	return out
}

// front is an entry split into its front matter lines and the rest. has says it had front matter.
type front struct {
	lines []string
	rest  string
	has   bool
}

// splitFront takes the front matter of body apart. A body without one gets an empty one.
func splitFront(body string) front {
	_, rest, skipped := ParseMeta(body, time.Time{})
	if skipped == 0 {
		return front{rest: body}
	}
	lines := strings.Split(body, "\n")[1 : skipped-1]
	return front{lines: append([]string(nil), lines...), rest: rest, has: true}
}

// String puts the entry back together.
func (f front) String() string {
	if len(f.lines) == 0 {
		return f.rest
	}
	rest := f.rest
	if !f.has {
		rest = "\n" + rest
	}
	return "---\n" + strings.Join(f.lines, "\n") + "\n---\n" + rest
}

// find is the line that holds key, -1 when none does.
func (f front) find(key string) int {
	for i, line := range f.lines {
		k, _, ok := strings.Cut(line, ":")
		if ok && strings.EqualFold(strings.TrimSpace(k), key) {
			return i
		}
	}
	return -1
}

// set writes key: value, replacing the line that held key.
func (f *front) set(key, value string) {
	line := key + ": " + value
	if i := f.find(key); i >= 0 {
		f.lines[i] = line
		return
	}
	f.lines = append(f.lines, line)
}

// SetKind writes the kind of an entry into its front matter, keeping every other property.
func SetKind(body, kind string) string {
	f := splitFront(body)
	f.set("type", kind)
	return f.String()
}

// AddRelation adds an association to an entry's front matter: target joins the entries the
// property already names, or the property is added. Unchanged when it is already there.
func AddRelation(body, kind, target string) string {
	f := splitFront(body)
	link := "[[" + target + "]]"
	i := f.find(kind)
	if i < 0 {
		f.set(kind, `"`+link+`"`)
		return f.String()
	}
	_, value, _ := strings.Cut(f.lines[i], ":")
	for _, t := range relationTargets(value) {
		if strings.EqualFold(t, target) {
			return body
		}
	}
	names := strings.TrimSpace(strings.Trim(strings.TrimSpace(value), `"`))
	if names != "" {
		names += ", "
	}
	f.lines[i] = kind + `: "` + names + link + `"`
	return f.String()
}

// StripFront is the entry without its front matter: what a person reads.
func StripFront(body string) string {
	return splitFront(body).rest
}
