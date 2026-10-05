package tui

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"unicode/utf8"
)

// artifactPreviewLines bounds how much of a text file the chat shows: the whole file is one
// command away (/artifacts save) and a long one would bury the conversation.
const artifactPreviewLines = 40

// artifactsCommand answers /artifacts: with nothing, the list; with a name, the file's text;
// with `save <name>`, the file written into the current directory.
func (t *TUI) artifactsCommand(ctx context.Context, arg string) {
	browser, ok := t.Runner.(ArtifactBrowser)
	if !ok {
		t.addMessage(AuthorSystem, t.tr("this interface is not attached to a gateway that keeps artifacts"))
		return
	}
	fields := strings.Fields(arg)
	switch {
	case len(fields) == 0:
		t.listArtifacts(ctx, browser)
	case fields[0] == "save" && len(fields) == 2:
		t.saveArtifact(ctx, browser, fields[1])
	case len(fields) == 1:
		t.showArtifact(ctx, browser, fields[0])
	default:
		t.addMessage(AuthorSystem, t.tr("usage: /artifacts [name | save name]"))
	}
}

func (t *TUI) listArtifacts(ctx context.Context, b ArtifactBrowser) {
	all, err := b.ListArtifacts(ctx)
	if err != nil {
		t.addMessage(AuthorSystem, fmt.Errorf(t.tr("the gateway could not be asked for the artifacts: %w"), err).Error())
		return
	}
	if len(all) == 0 {
		t.addMessage(AuthorSystem, t.tr("Nothing saved in this session yet. Ask for a report, a page or a diagram and it will show up here."))
		return
	}
	var sb strings.Builder
	for _, a := range all {
		fmt.Fprintf(&sb, "  %s  (%s, %d KB)\n", a.Name, a.Type, max(1, int(a.Size/1024)))
	}
	t.addPreformatted(AuthorSystem, strings.TrimRight(sb.String(), "\n"))
}

func (t *TUI) showArtifact(ctx context.Context, b ArtifactBrowser, name string) {
	data, err := b.ReadArtifact(ctx, name)
	if err != nil {
		t.addMessage(AuthorSystem, err.Error())
		return
	}
	if !utf8.Valid(data) || strings.ContainsRune(string(data), 0) {
		t.addMessage(AuthorSystem, t.trf("%s is not text: /artifacts save %s writes it to the current directory", name, name))
		return
	}
	lines := strings.Split(strings.TrimRight(string(data), "\n"), "\n")
	more := 0
	if len(lines) > artifactPreviewLines {
		more = len(lines) - artifactPreviewLines
		lines = lines[:artifactPreviewLines]
	}
	t.addPreformatted(AuthorSystem, strings.Join(lines, "\n"))
	if more > 0 {
		t.addMessage(AuthorSystem, t.trf("… and %d more lines: /artifacts save %s writes the whole file", more, name))
	}
}

func (t *TUI) saveArtifact(ctx context.Context, b ArtifactBrowser, name string) {
	// A name comes from the gateway's folder and from the user's keyboard, and either could
	// carry a path: only a plain file name is written, and never over a file that is there.
	if name != filepath.Base(name) || strings.HasPrefix(name, ".") {
		t.addMessage(AuthorSystem, t.tr("an artifact is saved under its plain name, without a path"))
		return
	}
	data, err := b.ReadArtifact(ctx, name)
	if err != nil {
		t.addMessage(AuthorSystem, err.Error())
		return
	}
	f, err := os.OpenFile(name, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o644)
	if errors.Is(err, os.ErrExist) {
		t.addMessage(AuthorSystem, t.trf("%s already exists here: move it or rename it first", name))
		return
	}
	if err == nil {
		_, err = f.Write(data)
		if cerr := f.Close(); err == nil {
			err = cerr
		}
	}
	if err != nil {
		t.addMessage(AuthorSystem, err.Error())
		return
	}
	t.addMessage(AuthorSystem, t.trf("saved %s (%d bytes)", name, len(data)))
}
