// Package voice turns speech into text for the browser interface.
//
// It does two things and no more. Transcribe sends one recording to an endpoint that speaks the
// OpenAI transcription API - OpenAI itself, Groq, or a whisper.cpp server on the same machine -
// and returns the text. Match finds a wake phrase at the start of a transcript and returns what
// was said after it.
//
// Nothing here records, stores or decodes audio: the browser records it, the endpoint decodes
// it, and the bytes live only for the length of one request. That is why there is no ffmpeg, no
// cgo and no model in this binary.
package voice

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime"
	"mime/multipart"
	"net/http"
	"strings"
	"time"
)

// Transcriber calls one transcription endpoint.
type Transcriber struct {
	// BaseURL is the endpoint's base, without the trailing /audio/transcriptions.
	BaseURL string
	// APIKey is sent as a bearer token when it is not empty. A local server needs none.
	APIKey string
	// Model and Language are sent as the form fields of the same name. An empty
	// Language lets the model detect it.
	Model    string
	Language string
	// Client is the HTTP client. Nil means one with a two-minute timeout.
	Client *http.Client
}

// maxErrorBody is how much of a refused request's body is quoted back: enough for the
// provider's own message, not a page of HTML from a proxy.
const maxErrorBody = 512

// ErrEmpty is returned for a recording with no bytes in it.
var ErrEmpty = errors.New("the recording is empty")

// extensions maps the types a browser records to the file name the endpoint expects: the
// OpenAI API decides the format from the NAME, not from a content type.
var extensions = map[string]string{
	"audio/webm":  "webm",
	"audio/ogg":   "ogg",
	"audio/mp4":   "mp4",
	"audio/m4a":   "m4a",
	"audio/x-m4a": "m4a",
	"audio/mpeg":  "mp3",
	"audio/mp3":   "mp3",
	"audio/wav":   "wav",
	"audio/x-wav": "wav",
	"audio/wave":  "wav",
	"audio/flac":  "flac",
	"video/webm":  "webm",
	"video/mp4":   "mp4",
}

// Extension is the file extension of a recording's content type, and false for a type no
// transcription endpoint accepts. Parameters ("audio/webm;codecs=opus") are ignored.
func Extension(contentType string) (string, bool) {
	media, _, err := mime.ParseMediaType(contentType)
	if err != nil {
		return "", false
	}
	ext, ok := extensions[media]
	return ext, ok
}

// Transcribe sends one recording and returns its text.
func (t Transcriber) Transcribe(ctx context.Context, audio []byte, contentType string) (string, error) {
	if len(audio) == 0 {
		return "", ErrEmpty
	}
	ext, ok := Extension(contentType)
	if !ok {
		return "", fmt.Errorf("the recording type %q is not one a transcription endpoint accepts", contentType)
	}
	body, formType := form(audio, ext, t.Model, t.Language)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost,
		strings.TrimRight(t.BaseURL, "/")+"/audio/transcriptions", body)
	if err != nil {
		return "", fmt.Errorf("the transcription request could not be built: %w", err)
	}
	req.Header.Set("Content-Type", formType)
	if t.APIKey != "" {
		req.Header.Set("Authorization", "Bearer "+t.APIKey)
	}
	client := t.Client
	if client == nil {
		client = &http.Client{Timeout: 2 * time.Minute}
	}
	resp, err := client.Do(req)
	if err != nil {
		return "", fmt.Errorf("the transcription endpoint could not be reached: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if resp.StatusCode/100 != 2 {
		msg := strings.TrimSpace(string(raw))
		if len(msg) > maxErrorBody {
			msg = msg[:maxErrorBody] + "..."
		}
		return "", fmt.Errorf("the transcription endpoint answered %d: %s", resp.StatusCode, msg)
	}
	var out struct {
		Text string `json:"text"`
	}
	if err := json.Unmarshal(raw, &out); err != nil {
		return "", fmt.Errorf("the transcription endpoint answered something that is not JSON: %w", err)
	}
	return strings.TrimSpace(out.Text), nil
}

// form builds the multipart body. Writing to a bytes.Buffer cannot fail, so the errors of the
// multipart writer are not paths anyone can reach and are not returned.
func form(audio []byte, ext, model, language string) (*bytes.Buffer, string) {
	var buf bytes.Buffer
	w := multipart.NewWriter(&buf)
	part, _ := w.CreateFormFile("file", "speech."+ext)
	_, _ = part.Write(audio)
	_ = w.WriteField("model", model)
	_ = w.WriteField("response_format", "json")
	if language != "" {
		_ = w.WriteField("language", language)
	}
	_ = w.Close()
	return &buf, w.FormDataContentType()
}
