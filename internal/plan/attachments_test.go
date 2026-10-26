package plan

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"sync"
	"testing"
)

// A picture the message names is sent to the model as an image part, not left as a path.
func TestRunShowsTheModelThePicturesTheMessageNames(t *testing.T) {
	a, _ := makeAgent(t, true)
	ws := t.TempDir()
	dir := filepath.Join(ws, ".motita", "uploads")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "shot.png"), []byte("png"), 0o644); err != nil {
		t.Fatal(err)
	}
	srv := llmServer(t, []replyStep{{content: "I see it"}})
	defer srv.Close()
	var mu sync.Mutex
	var sent []byte
	inner := srv.Config.Handler
	srv.Config.Handler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		mu.Lock()
		sent = append(sent, body...)
		mu.Unlock()
		r.Body = io.NopCloser(bytes.NewReader(body))
		inner.ServeHTTP(w, r)
	})
	p := New(newClient(t, srv), a).WithAttachments(ws)
	msg := "what is this?\n\n[The person attached these files, saved in your workspace:\n" +
		"- .motita/uploads/shot.png (image, 3 B)]"
	if _, err := p.Run(context.Background(), msg); err != nil {
		t.Fatal(err)
	}
	mu.Lock()
	defer mu.Unlock()
	if !bytes.Contains(sent, []byte(`"image_url"`)) || !bytes.Contains(sent, []byte("data:image/png;base64,cG5n")) {
		t.Fatalf("the request carried no picture: %.300s", sent)
	}
}
