package voice

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/madkoding/motita/internal/httpbody"
)

// speakDashScope says a text with Qwen3-TTS through DashScope's native API:
// POST {base}/services/aigc/multimodal-generation/generation answers a JSON whose
// output.audio.url is a wav, valid for 24 hours, that is downloaded right away. The key is sent
// to the API only, never to the audio URL (another host, signed by the API).
func (s Speaker) speakDashScope(ctx context.Context, text, voiceName string) ([]byte, string, error) {
	body, _ := json.Marshal(map[string]any{
		"model": s.Model,
		"input": map[string]string{"text": text, "voice": voiceName, "language_type": "Auto"},
	})
	req, err := http.NewRequestWithContext(ctx, http.MethodPost,
		strings.TrimRight(s.BaseURL, "/")+"/services/aigc/multimodal-generation/generation", bytes.NewReader(body))
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
	raw, err := doBounded(client, req, "speech endpoint", maxErrorBody*16)
	if err != nil {
		return nil, "", err
	}
	var out struct {
		Code    string `json:"code"`
		Message string `json:"message"`
		Output  struct {
			Audio struct {
				URL string `json:"url"`
			} `json:"audio"`
		} `json:"output"`
	}
	if err := json.Unmarshal(raw, &out); err != nil {
		return nil, "", fmt.Errorf("the speech endpoint's answer is not JSON: %w", err)
	}
	if out.Code != "" {
		return nil, "", fmt.Errorf("the speech endpoint refused: %s: %s", out.Code, out.Message)
	}
	u, err := url.Parse(out.Output.Audio.URL)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
		return nil, "", errors.New("the speech endpoint answered no audio address")
	}
	// The address parsed above and the method is a constant: building the request cannot fail.
	get, _ := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	audio, err := doBounded(client, get, "audio address", maxSpeechAudio)
	if err != nil {
		return nil, "", err
	}
	if len(audio) == 0 {
		return nil, "", errors.New("the speech endpoint answered no audio")
	}
	return audio, "audio/wav", nil
}

// doBounded sends req and reads at most limit bytes of a 2xx answer, naming what failed.
func doBounded(client *http.Client, req *http.Request, what string, limit int) ([]byte, error) {
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("the %s could not be reached: %w", what, err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode/100 != 2 {
		raw, _ := io.ReadAll(io.LimitReader(resp.Body, maxErrorBody+1))
		if msg := httpbody.Note(raw, maxErrorBody); msg != "" {
			return nil, fmt.Errorf("the %s answered %d: %s", what, resp.StatusCode, msg)
		}
		return nil, fmt.Errorf("the %s answered %d", what, resp.StatusCode)
	}
	data, err := io.ReadAll(io.LimitReader(resp.Body, int64(limit)+1))
	if err != nil {
		return nil, fmt.Errorf("the %s's answer could not be read: %w", what, err)
	}
	if len(data) > limit {
		return nil, fmt.Errorf("the %s answered more than %d bytes", what, limit)
	}
	return data, nil
}
