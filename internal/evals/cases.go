// Package evals is a small set of fixed tasks that tells whether a model, or the instructions it is
// given, got worse: the same tasks run in an isolated temporary folder, each one judged by a
// command that looks at the real result, and the run is compared with the one before.
//
// The tasks are deliberately tiny and deterministic. They do not measure how good a model is; they
// notice that something which used to work no longer does, or that it now takes more rounds and
// money to get there.
package evals

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
)

// Case is one fixed task: what the model is asked, the files it starts with, and the shell check
// that prints OK when the folder holds the right result.
type Case struct {
	// ID names the task for good; the interface words it in the person's language by this id.
	ID     string
	Prompt string
	// Files are written into the folder before the task starts, by relative path.
	Files map[string]string
	Check string
}

// Cases is the fixed set. Changing a task changes CasesHash, so a run is never compared with one
// made on a different set.
func Cases() []Case {
	return []Case{
		{
			ID:     "create-file",
			Prompt: "Create a file named hello.txt that contains exactly the word hello.",
			Check:  `[ "$(cat hello.txt)" = hello ] && echo OK`,
		},
		{
			ID:     "fix-typo",
			Prompt: "The file letter.txt has a misspelled word. Fix it so the file reads: I receive your mail",
			Files:  map[string]string{"letter.txt": "I recieve your mail\n"},
			Check:  `[ "$(cat letter.txt)" = "I receive your mail" ] && echo OK`,
		},
		{
			ID:     "count-lines",
			Prompt: "Write the number of lines of data.txt into count.txt and nothing else.",
			Files:  map[string]string{"data.txt": "a\nb\nc\nd\ne\nf\ng\n"},
			Check:  `[ "$(tr -d ' \n' < count.txt)" = 7 ] && echo OK`,
		},
		{
			ID:     "rename-file",
			Prompt: "Rename old.txt to new.txt without changing what it contains.",
			Files:  map[string]string{"old.txt": "keep me\n"},
			Check:  `[ ! -e old.txt ] && [ "$(cat new.txt)" = "keep me" ] && echo OK`,
		},
		{
			ID:     "sort-lines",
			Prompt: "Write the lines of names.txt in alphabetical order into sorted.txt.",
			Files:  map[string]string{"names.txt": "mia\nzoe\nada\nleo\n"},
			Check:  `[ "$(cat sorted.txt)" = "$(printf 'ada\nleo\nmia\nzoe')" ] && echo OK`,
		},
	}
}

// CasesHash fingerprints the set, so a run made on another set is not compared with this one.
func CasesHash() string {
	return hashOf(Cases())
}

// hashOf is a short fingerprint of any JSON-able value.
func hashOf(v any) string {
	raw, _ := json.Marshal(v)
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:6])
}

// PromptsHash fingerprints the instructions the agent works with, so a run can say they changed.
func PromptsHash(prompts any) string {
	return hashOf(prompts)
}
