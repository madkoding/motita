package memory

import (
	"regexp"
	"strings"
)

var badNameChars = regexp.MustCompile(`[\\:*?"<>|#^\[\]]`)

// FileName makes the memory path of a title inside folder: no characters a file name cannot hold,
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

// titleWords and titleRunes bound the title NameFor takes from a text.
const (
	titleWords = 8
	titleRunes = 60
)

// NameFor is the title of an entry the model saved without naming one: the first line of the text,
// without Markdown marks, cut to a few words. "Notes" when the text gives nothing usable.
func NameFor(text string) string {
	for _, line := range strings.Split(text, "\n") {
		line = strings.TrimLeft(strings.TrimSpace(line), "#>*-+ \t")
		line = strings.TrimSpace(badNameChars.ReplaceAllString(strings.ReplaceAll(line, "/", " "), ""))
		words := strings.Fields(strings.TrimLeft(line, "."))
		if len(words) == 0 {
			continue
		}
		name := []rune(strings.Join(words[:min(len(words), titleWords)], " "))
		return strings.TrimSpace(string(name[:min(len(name), titleRunes)]))
	}
	return "Notes"
}
