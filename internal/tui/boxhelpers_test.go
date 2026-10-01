package tui

import "strings"

// textEnd is the column where the text of an input-box row ends: the row without its right side
// and the blank padding before it. The cursor belongs one past it, and the padding that keeps the
// box's right edge straight is not text.
func textEnd(row string) int {
	row = strings.TrimRight(row, " ")
	row = strings.TrimSuffix(row, glyphRail)
	return visibleLen(strings.TrimRight(row, " "))
}
