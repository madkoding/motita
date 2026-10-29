package voice

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/madkoding/motita/internal/httpbody"
)

// Speaker calls one endpoint that speaks the OpenAI speech API (POST {base}/audio/speech): the
// text goes in, the audio comes back. Like Transcribe, nothing is kept: the bytes live for the
// length of one request.
type Speaker struct {
	// BaseURL is the endpoint's base, without the trailing /audio/speech.
	BaseURL string
	// APIKey is sent as a bearer token when it is not empty. A local server needs none.
	APIKey string
	// Model and Format are sent as the fields of the same name (format as response_format).
	Model  string
	Format string
	// API is the endpoint's protocol: "dashscope" for Qwen3-TTS on Alibaba Model Studio, anything
	// else for the OpenAI speech API.
	API string
	// Client is the HTTP client. Nil means one with a one-minute timeout.
	Client *http.Client
}

// maxSpeechAudio caps the audio read back: minutes of speech in any format the API answers. A
// variable so a test need not send 32 MB.
var maxSpeechAudio = 32 << 20

// ErrNoText is returned for a request with nothing to say.
var ErrNoText = errors.New("there is no text to say")

// formatTypes are the content types of the formats the speech API answers, for an endpoint that
// does not name one.
var formatTypes = map[string]string{
	"mp3":  "audio/mpeg",
	"opus": "audio/ogg",
	"aac":  "audio/aac",
	"flac": "audio/flac",
	"wav":  "audio/wav",
}

// Speak says one text with one voice, and returns the audio and its content type.
func (s Speaker) Speak(ctx context.Context, text, voiceName string) ([]byte, string, error) {
	text = strings.TrimSpace(text)
	if text == "" {
		return nil, "", ErrNoText
	}
	if s.API == "dashscope" {
		return s.speakDashScope(ctx, text, voiceName)
	}
	body, _ := json.Marshal(map[string]string{
		"model": s.Model, "input": text, "voice": voiceName, "response_format": s.Format,
	})
	req, err := http.NewRequestWithContext(ctx, http.MethodPost,
		strings.TrimRight(s.BaseURL, "/")+"/audio/speech", bytes.NewReader(body))
	if err != nil {
		return nil, "", fmt.Errorf("the speech request could not be built: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	if s.APIKey != "" {
		req.Header.Set("Authorization", "Bearer "+s.APIKey)
	}
	client := s.Client
	if client == nil {
		client = &http.Client{Timeout: time.Minute}
	}
	resp, err := client.Do(req)
	if err != nil {
		return nil, "", fmt.Errorf("the speech endpoint could not be reached: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode/100 != 2 {
		raw, _ := io.ReadAll(io.LimitReader(resp.Body, maxErrorBody+1))
		msg := httpbody.Note(raw, maxErrorBody)
		if msg == "" {
			return nil, "", fmt.Errorf("the speech endpoint answered %d", resp.StatusCode)
		}
		return nil, "", fmt.Errorf("the speech endpoint answered %d: %s", resp.StatusCode, msg)
	}
	audio, err := io.ReadAll(io.LimitReader(resp.Body, int64(maxSpeechAudio)+1))
	if err != nil {
		return nil, "", fmt.Errorf("the speech endpoint's audio could not be read: %w", err)
	}
	if len(audio) > maxSpeechAudio {
		return nil, "", fmt.Errorf("the speech endpoint answered more than %d MB of audio", maxSpeechAudio>>20)
	}
	if len(audio) == 0 {
		return nil, "", errors.New("the speech endpoint answered no audio")
	}
	ctype := resp.Header.Get("Content-Type")
	if !strings.HasPrefix(ctype, "audio/") {
		// Some local servers answer application/octet-stream: the format asked for names it.
		ctype = formatTypes[s.Format]
		if ctype == "" {
			ctype = "audio/mpeg"
		}
	}
	return audio, ctype, nil
}

// dashScopePath is where Qwen3-TTS answers, under the endpoint's host.
const dashScopePath = "/api/v1/services/aigc/multimodal-generation/generation"

// speakDashScope calls Qwen3-TTS: the answer is a JSON document naming a short-lived WAV URL,
// which is fetched in a second request. The audio is always WAV, whatever Format asks.
func (s Speaker) speakDashScope(ctx context.Context, text, voiceName string) ([]byte, string, error) {
	body, _ := json.Marshal(map[string]any{
		"model": s.Model,
		"input": map[string]string{"text": text, "voice": voiceName, "language_type": "Auto"},
	})
	raw, err := s.do(ctx, http.MethodPost, dashScopeBase(s.BaseURL)+dashScopePath, body, s.APIKey, 1<<20)
	if err != nil {
		return nil, "", err
	}
	var out struct {
		Output struct {
			Audio struct {
				URL string `json:"url"`
			} `json:"audio"`
		} `json:"output"`
		Message string `json:"message"`
	}
	_ = json.Unmarshal(raw, &out)
	url := out.Output.Audio.URL
	if !strings.HasPrefix(url, "https://") && !strings.HasPrefix(url, "http://") {
		if out.Message != "" {
			return nil, "", fmt.Errorf("the speech endpoint refused the text: %s", out.Message)
		}
		return nil, "", errors.New("the speech endpoint answered no audio")
	}
	// The link is pre-signed: it carries its own authorization and gets no bearer token.
	audio, err := s.do(ctx, http.MethodGet, url, nil, "", maxSpeechAudio)
	if err != nil {
		return nil, "", err
	}
	if len(audio) == 0 {
		return nil, "", errors.New("the speech endpoint answered no audio")
	}
	return audio, "audio/wav", nil
}

// dashScopeBase is the host of a DashScope base URL: the compatible-mode path of the language
// model's endpoint is not where the speech API lives.
func dashScopeBase(base string) string {
	base = strings.TrimRight(base, "/")
	if i := strings.Index(base, "/compatible-mode"); i >= 0 {
		return base[:i]
	}
	return strings.TrimSuffix(base, "/api/v1")
}

// do sends one request and reads at most limit bytes of a 2xx answer.
func (s Speaker) do(ctx context.Context, method, url string, body []byte, key string, limit int) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, method, url, bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("the speech request could not be built: %w", err)
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if key != "" {
		req.Header.Set("Authorization", "Bearer "+key)
	}
	client := s.Client
	if client == nil {
		client = &http.Client{Timeout: time.Minute}
	}
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("the speech endpoint could not be reached: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode/100 != 2 {
		raw, _ := io.ReadAll(io.LimitReader(resp.Body, maxErrorBody+1))
		if msg := httpbody.Note(raw, maxErrorBody); msg != "" {
			return nil, fmt.Errorf("the speech endpoint answered %d: %s", resp.StatusCode, msg)
		}
		return nil, fmt.Errorf("the speech endpoint answered %d", resp.StatusCode)
	}
	raw, err := io.ReadAll(io.LimitReader(resp.Body, int64(limit)+1))
	if err != nil {
		return nil, fmt.Errorf("the speech endpoint's answer could not be read: %w", err)
	}
	if len(raw) > limit {
		return nil, fmt.Errorf("the speech endpoint answered more than %d bytes", limit)
	}
	return raw, nil
}
