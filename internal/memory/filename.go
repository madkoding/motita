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
