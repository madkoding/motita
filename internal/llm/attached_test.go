package llm

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestAttachedImagesReadsOnlyPicturesTheMessageNames(t *testing.T) {
	ws := t.TempDir()
	dir := filepath.Join(ws, ".motita", "uploads")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	write := func(name string, n int) {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(strings.Repeat("x", n)), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("my shot.PNG", 4)
	write("photo.jpg", 5)
	write("notes.pdf", 5)
	write("huge.png", maxAttachedImageBytes+1)
	write("empty.gif", 0)
	text := "look\n\n[The person attached these files, saved in your workspace:\n" +
		"- .motita/uploads/my shot.PNG (image, 4 B)\n" +
		"- .motita/uploads/photo.jpg (image, 5 B) - look at it\n" +
		"- .motita/uploads/notes.pdf (document, 5 B)\n" +
		"- .motita/uploads/huge.png (image, 3.0 MB)\n" +
		"- .motita/uploads/gone.png (image, 1 B)\n" +
		"- .motita/uploads/../escape.png (image, 1 B)\n" +
		"- .motita/uploads/empty.gif (image, 0 B)]"
	got := AttachedImages(ws, text)
	if len(got) != 3 {
		t.Fatalf("images = %d, want 3 (png, jpg, empty gif)", len(got))
	}
	if got[0].MediaType != "image/png" || string(got[0].Data) != "xxxx" || got[1].MediaType != "image/jpeg" {
		t.Fatalf("images = %+v", got)
	}
}

func TestAttachedImagesStaysWithinItsLimits(t *testing.T) {
	ws := t.TempDir()
	dir := filepath.Join(ws, ".motita", "uploads")
	_ = os.MkdirAll(dir, 0o755)
	var text strings.Builder
	for _, n := range []string{"a", "b", "c", "d", "e", "f"} {
		_ = os.WriteFile(filepath.Join(dir, n+".png"), make([]byte, 2<<20), 0o644)
		text.WriteString("- .motita/uploads/" + n + ".png (image, 2.0 MB)\n")
	}
	got := AttachedImages(ws, text.String())
	if len(got) != 4 {
		t.Fatalf("with 2 MB each the 8 MB total allows 4, got %d", len(got))
	}
}

func TestAttachedImagesWithNothingToShow(t *testing.T) {
	if AttachedImages("", "- .motita/uploads/a.png (image, 1 B)") != nil {
		t.Error("no workspace")
	}
	if AttachedImages(t.TempDir(), "just text") != nil {
		t.Error("no attachments named")
	}
	if AttachedImages(t.TempDir(), "- .motita/uploads/a.png (image, 1 B)") != nil {
		t.Error("no upload folder")
	}
}
