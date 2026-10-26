package llm

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

// PICTURES THE PERSON ATTACHED.
//
// The web interface stores an attached file in the session's workspace and names it in the message
// ("- .motita/uploads/shot.png (image, 2 KB)"). A model that can see should be shown the picture
// itself rather than be told to go and open it, so the agent turns those lines into Images here.

const (
	// attachedDir is where the gateway keeps what a person attached, relative to the workspace.
	attachedDir = ".motita/uploads"
	// maxAttachedImages and the byte limits keep a message with many large pictures from becoming
	// a request no provider takes; the rest stay on disk for the agent to open by path.
	maxAttachedImages     = 4
	maxAttachedImageBytes = 3 << 20
	maxAttachedTotalBytes = 8 << 20
)

// attachedLine matches the line the gateway's message gives an attached picture.
var attachedLine = regexp.MustCompile(`(?m)^- ` + regexp.QuoteMeta(attachedDir) + `/(.+) \(image, [^)]*\)`)

// attachedTypes are the picture formats every provider takes.
var attachedTypes = map[string]string{
	".png": "image/png", ".jpg": "image/jpeg", ".jpeg": "image/jpeg",
	".gif": "image/gif", ".webp": "image/webp",
}

// AttachedImages reads the pictures a message names under the workspace's upload folder. A file
// that is missing, too large, of another type or outside the folder is skipped: it stays in the
// message as a path for the agent to open.
func AttachedImages(workspace, text string) []Image {
	if workspace == "" || !strings.Contains(text, attachedDir) {
		return nil
	}
	root, err := os.OpenRoot(filepath.Join(workspace, filepath.FromSlash(attachedDir)))
	if err != nil {
		return nil
	}
	defer root.Close()
	var images []Image
	total := 0
	for _, m := range attachedLine.FindAllStringSubmatch(text, -1) {
		mediaType, ok := attachedTypes[strings.ToLower(filepath.Ext(m[1]))]
		if !ok || len(images) == maxAttachedImages {
			continue
		}
		data, err := readAttached(root, m[1])
		if err != nil || total+len(data) > maxAttachedTotalBytes {
			continue
		}
		total += len(data)
		images = append(images, Image{MediaType: mediaType, Data: data})
	}
	return images
}

// readAttached reads one file through the root, which refuses a name that leaves the folder, and
// refuses one over the size limit without reading it whole.
func readAttached(root *os.Root, name string) ([]byte, error) {
	f, err := root.Open(name)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil || !info.Mode().IsRegular() || info.Size() > maxAttachedImageBytes {
		return nil, os.ErrInvalid
	}
	data := make([]byte, info.Size())
	if _, err := f.Read(data); err != nil && info.Size() > 0 {
		return nil, err
	}
	return data, nil
}
