package notes

import (
	"errors"
	"regexp"
	"strings"
)

// The block motita keeps up to date inside a note it generated. Everything outside it is the
// person's and is never touched, so a note can be scanned again and again while they add to it.
const (
	BlockStart = "<!-- motita:scan:start -->"
	BlockEnd   = "<!-- motita:scan:end -->"
)

// Outcomes of Upsert.
const (
	Created   = "created"
	Updated   = "updated"
	Unchanged = "unchanged"
)

var badNameChars = regexp.MustCompile(`[\\:*?"<>|#^\[\]]`)

// FileName makes the vault path of a title inside folder: no characters a file name cannot hold,
// one ".md". It answers "" when nothing usable is left.
func FileName(folder, title string) string {
	var parts []string
	for _, seg := range strings.Split(title, "/") {
		seg = strings.TrimSpace(badNameChars.ReplaceAllString(seg, ""))
		if seg != "" && !strings.HasPrefix(seg, ".") {
			parts = append(parts, seg)
		}
	}
	name := strings.Join(parts, "/")
	if strings.HasSuffix(strings.ToLower(name), ext) {
		name = name[:len(name)-len(ext)]
	}
	if strings.TrimSpace(name) == "" {
		return ""
	}
	if dir := strings.Trim(folder, "/"); dir != "" {
		name = dir + "/" + name
	}
	return name + ext
}

// Upsert puts block between the scan markers of the note at p. A note that does not exist is
// created with title as its heading; one with markers has only what is between them replaced; one
// without them gets the block at its end. It answers what happened.
func (v *Vault) Upsert(p, title, block string) (string, error) {
	managed := BlockStart + "\n" + strings.TrimSpace(block) + "\n" + BlockEnd
	body, _, err := v.Read(p)
	switch {
	case errors.Is(err, ErrNotFound):
		_, err = v.Write(p, "# "+title+"\n\n"+managed+"\n", true)
		return Created, err
	case err != nil:
		return "", err
	default:
		// the note exists: only its block changes
	}
	next := body
	if i := strings.Index(body, BlockStart); i >= 0 {
		if j := strings.Index(body[i:], BlockEnd); j >= 0 {
			next = body[:i] + managed + body[i+j+len(BlockEnd):]
		} else {
			next = body[:i] + managed
		}
	} else {
		next = strings.TrimRight(body, "\n") + "\n\n" + managed + "\n"
	}
	if next == body {
		return Unchanged, nil
	}
	_, err = v.Write(p, next, false)
	return Updated, err
}
