package voice

import (
	"context"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"net/url"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"
)

// Endpoint is a speech server found on this machine: the base that POST {Base}/audio/speech is
// called on, and the models it lists (empty when it lists none).
type Endpoint struct {
	Base   string   `json:"base_url"`
	Models []string `json:"models"`
}

// DetectPorts are the ports local speech servers listen on by default: vLLM and Kokoro-FastAPI
// (8000, 8880), Flask-style servers (5000), llama.cpp-style servers (8080) and Gradio (7860).
var DetectPorts = []int{8000, 8880, 5000, 8080, 7860}

// detectTimeout bounds one probe: a server on this machine answers at once or not at all.
var detectTimeout = 800 * time.Millisecond

// maxDetectBody caps what is read of a /models answer.
const maxDetectBody = 64 << 10

// maxDetectModels caps the model names kept per server.
const maxDetectModels = 20

// DetectSpeech looks for OpenAI-compatible speech servers on this machine: DetectPorts and ports
// (voice.tts.detect_ports) on the loopback, with and without the /v1 prefix, and extra (the
// configured base_url) when it too is on the loopback. A candidate is probed with OPTIONS
// {base}/audio/speech and GET {base}/models, and
// neither carries any text: nothing the person wrote ever reaches a server that was only looked at.
// A base is never followed through a redirect. The result is in probe order, without repeats.
func DetectSpeech(ctx context.Context, ports []int, extra ...string) []Endpoint {
	var bases []string
	add := func(b string) {
		if b = strings.TrimRight(strings.TrimSpace(b), "/"); b != "" && !slices.Contains(bases, b) {
			bases = append(bases, b)
		}
	}
	for _, e := range extra {
		if loopbackBase(e) {
			add(e)
		}
	}
	for _, p := range append(slices.Clone(DetectPorts), ports...) {
		host := "http://" + net.JoinHostPort("127.0.0.1", strconv.Itoa(p))
		add(host + "/v1")
		add(host)
	}
	client := detectClient()
	found := make([]*Endpoint, len(bases))
	var wg sync.WaitGroup
	for i, base := range bases {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if hasSpeechRoute(ctx, client, base) {
				found[i] = &Endpoint{Base: base, Models: listModels(ctx, client, base)}
			}
		}()
	}
	wg.Wait()
	out := []Endpoint{}
	for _, f := range found {
		if f != nil {
			out = append(out, *f)
		}
	}
	return out
}

// detectClient is the client of the probes: short timeout, and a redirect is an answer, not a road.
func detectClient() *http.Client {
	return &http.Client{
		Timeout:       detectTimeout,
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}
}

// loopbackBase reports whether a base URL points at this machine.
func loopbackBase(base string) bool {
	u, err := url.Parse(strings.TrimSpace(base))
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") {
		return false
	}
	host := u.Hostname()
	if host == "localhost" {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

// hasSpeechRoute asks, with OPTIONS, whether {base}/audio/speech exists. A missing route answers
// 404; a route that takes only other methods says so in Allow. Anything else (405 allowing POST,
// 200/204, 401/403 for a server that wants a key, 400/422) means the route is there.
func hasSpeechRoute(ctx context.Context, client *http.Client, base string) bool {
	req, err := http.NewRequestWithContext(ctx, http.MethodOptions, base+"/audio/speech", nil)
	if err != nil {
		return false
	}
	resp, err := client.Do(req)
	if err != nil {
		return false
	}
	defer func() { _ = resp.Body.Close() }()
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, maxDetectBody))
	if resp.StatusCode == http.StatusNotFound || resp.StatusCode/100 == 5 || resp.StatusCode/100 == 3 {
		return false
	}
	if resp.StatusCode == http.StatusMethodNotAllowed {
		allow := strings.ToUpper(resp.Header.Get("Allow"))
		return allow == "" || strings.Contains(allow, "POST")
	}
	return true
}

// listModels reads the model ids of {base}/models, the OpenAI listing. A server that lists none, or
// answers something else, has no names to offer.
func listModels(ctx context.Context, client *http.Client, base string) []string {
	models := []string{}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, base+"/models", nil)
	if err != nil {
		return models
	}
	resp, err := client.Do(req)
	if err != nil {
		return models
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return models
	}
	var body struct {
		Data []struct {
			ID string `json:"id"`
		} `json:"data"`
	}
	if json.NewDecoder(io.LimitReader(resp.Body, maxDetectBody)).Decode(&body) != nil {
		return models
	}
	for _, m := range body.Data {
		if id := strings.TrimSpace(m.ID); id != "" && len(models) < maxDetectModels {
			models = append(models, id)
		}
	}
	return models
}
