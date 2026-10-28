package codeindex

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"sort"
	"strings"
	"sync"
	"time"
)

// Neural embeddings are OPTIONAL and leave the machine: the text of every indexed window is sent to
// the endpoint the person named, once, and the vectors are kept on disk so it is not sent again. The
// endpoint speaks the OpenAI embeddings API (POST {url}/embeddings), which OpenAI, a local Ollama
// or llama.cpp server and most gateways answer.
const (
	embedBatch      = 32
	embedTextRunes  = 1500
	embedMinCosine  = 0.25
	embedTimeout    = 60 * time.Second
	maxEmbedReply   = 64 << 20
	embedErrorRunes = 200
)

// maxEmbedChunks caps how many windows are sent: the cost and the privacy exposure grow with it. It is a
// variable so a test can lower it.
var maxEmbedChunks = 4000

// Embedder turns texts into vectors.
type Embedder interface {
	Embed(ctx context.Context, texts []string) ([][]float32, error)
}

// httpEmbedder is the OpenAI-compatible embeddings client.
type httpEmbedder struct {
	url, key, model string
	client          *http.Client
}

// newEmbedder returns the embedder o names, or nil when it names none.
func newEmbedder(o Options) Embedder {
	if strings.TrimSpace(o.EmbedURL) == "" {
		return nil
	}
	return &httpEmbedder{
		url: strings.TrimRight(strings.TrimSpace(o.EmbedURL), "/"), key: o.EmbedKey, model: o.EmbedModel,
		client: &http.Client{Timeout: embedTimeout},
	}
}

// embedID names the endpoint and model a set of vectors came from: vectors of another model are
// not comparable, so a change of either rebuilds them.
func (o Options) embedID() string {
	if strings.TrimSpace(o.EmbedURL) == "" {
		return ""
	}
	return o.EmbedModel + "@" + strings.TrimRight(strings.TrimSpace(o.EmbedURL), "/")
}

type embedReply struct {
	Data []struct {
		Index     int       `json:"index"`
		Embedding []float32 `json:"embedding"`
	} `json:"data"`
}

// Embed sends texts and returns one vector per text, in order.
func (e *httpEmbedder) Embed(ctx context.Context, texts []string) ([][]float32, error) {
	body, _ := json.Marshal(map[string]any{"model": e.model, "input": texts})
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, e.url+"/embeddings", bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("the embeddings address %q is not usable: %w", e.url, err)
	}
	req.Header.Set("Content-Type", "application/json")
	if e.key != "" {
		req.Header.Set("Authorization", "Bearer "+e.key)
	}
	res, err := e.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("the embeddings endpoint did not answer: %w", err)
	}
	defer res.Body.Close()
	data, err := io.ReadAll(io.LimitReader(res.Body, maxEmbedReply))
	if err != nil {
		return nil, fmt.Errorf("reading the embeddings answer: %w", err)
	}
	if res.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("the embeddings endpoint answered HTTP %d: %s", res.StatusCode,
			clip(strings.TrimSpace(string(data)), embedErrorRunes))
	}
	return decodeEmbeddings(data, len(texts))
}

// decodeEmbeddings reads the answer and checks it holds one usable vector per text.
func decodeEmbeddings(data []byte, want int) ([][]float32, error) {
	var reply embedReply
	if err := json.Unmarshal(data, &reply); err != nil {
		return nil, fmt.Errorf("the embeddings endpoint answered something that is not JSON: %w", err)
	}
	if len(reply.Data) != want {
		return nil, fmt.Errorf("the embeddings endpoint returned %d vectors for %d texts", len(reply.Data), want)
	}
	sort.SliceStable(reply.Data, func(a, b int) bool { return reply.Data[a].Index < reply.Data[b].Index })
	out := make([][]float32, want)
	for i, d := range reply.Data {
		if len(d.Embedding) == 0 || len(d.Embedding) != len(reply.Data[0].Embedding) {
			return nil, errors.New("the embeddings endpoint returned vectors of different or zero length")
		}
		out[i] = d.Embedding
		normalize(out[i])
	}
	return out, nil
}

// embedChunks embeds the windows of ix, up to maxEmbedChunks of them, reading their text from the
// files. It changes nothing when it fails.
func embedChunks(ctx context.Context, ix *Index, e Embedder) ([][]float32, error) {
	n := min(len(ix.Chunks), maxEmbedChunks)
	lc := &lineCache{root: ix.root, byFile: map[string][]string{}}
	out := make([][]float32, len(ix.Chunks))
	for start := 0; start < n; start += embedBatch {
		end := min(start+embedBatch, n)
		texts := make([]string, 0, end-start)
		for i := start; i < end; i++ {
			texts = append(texts, ix.chunkText(lc, &ix.Chunks[i]))
		}
		vecs, err := e.Embed(ctx, texts)
		if err != nil {
			return nil, err
		}
		if len(vecs) != len(texts) {
			return nil, errors.New("the embedder returned the wrong number of vectors")
		}
		copy(out[start:end], vecs)
	}
	return out, nil
}

// chunkText is what is sent for one window: its path and its lines, cut to a size an embedding
// model takes.
func (ix *Index) chunkText(lc *lineCache, c *chunk) string {
	rel := ix.Files[c.File]
	lines, ok := lc.lines(rel)
	if !ok || c.Line-1+c.N > len(lines) {
		return rel
	}
	return clip(rel+"\n"+strings.Join(lines[c.Line-1:c.Line-1+c.N], "\n"), embedTextRunes)
}

// rankEmbedded orders the windows closest to the query vector. Windows that were never embedded
// (past the cap) do not take part.
func (ix *Index) rankEmbedded(q []float32) []ranked {
	var out []ranked
	for i, v := range ix.Emb {
		if cos := dot(q, v); v != nil && cos >= embedMinCosine {
			out = append(out, ranked{i, cos})
		}
	}
	sort.SliceStable(out, func(a, b int) bool { return out[a].score > out[b].score })
	return out[:min(len(out), semCandidate)]
}

// Local embeddings: with nothing configured, a running Ollama that has the nomic-embed-text model
// is used. It is on this machine, so no code leaves it.
const (
	autoModel   = "nomic-embed-text"
	probeBudget = 400 * time.Millisecond
)

var (
	// ollamaBase is where a local Ollama listens. A variable so a test can point it elsewhere.
	ollamaBase = "http://127.0.0.1:11434"
	// probeEvery is how long a search waits before looking again for an Ollama that was not there.
	probeEvery = time.Minute
)

// detect looks for the local Ollama when the settings ask for it and no endpoint is set, and
// switches the embeddings on when it finds the model. The probe runs outside the lock. Indexes
// built without vectors are dropped so they are built again with them.
func (s *Searcher) detect(ctx context.Context) {
	s.mu.Lock()
	if !s.shouldProbe() {
		s.mu.Unlock()
		return
	}
	s.probedAt = time.Now()
	s.mu.Unlock()
	model := localEmbedModel(ctx)
	if model == "" {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.opts.EmbedURL, s.opts.EmbedModel = ollamaBase+"/v1", model
	s.embedder = newEmbedder(s.opts)
	s.byAt, s.used = map[string]*Index{}, nil
	s.gen++
}

// shouldProbe says the settings ask for the look-up, nothing else supplies the embeddings, and the
// last look is old enough. Called with the lock held.
func (s *Searcher) shouldProbe() bool {
	if s.embedder != nil || !s.opts.EmbedAuto || strings.TrimSpace(s.opts.EmbedURL) != "" {
		return false
	}
	return s.probedAt.IsZero() || time.Since(s.probedAt) >= probeEvery
}

// localEmbedModel returns the installed model name of nomic-embed-text, or "" when Ollama does not
// answer or does not have it.
func localEmbedModel(ctx context.Context) string {
	ctx, cancel := context.WithTimeout(ctx, probeBudget)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, ollamaBase+"/api/tags", nil)
	if err != nil {
		return ""
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return ""
	}
	defer resp.Body.Close()
	var tags struct {
		Models []struct {
			Name string `json:"name"`
		} `json:"models"`
	}
	if resp.StatusCode != http.StatusOK || json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&tags) != nil {
		return ""
	}
	for _, m := range tags.Models {
		if m.Name == autoModel || strings.HasPrefix(m.Name, autoModel+":") {
			return m.Name
		}
	}
	return ""
}

// The look-up for a local Ollama that Endpoint does, shared by every caller so a memory that is
// searched on every keystroke asks the machine once a minute, not once a search.
var (
	endpointMu    sync.Mutex
	endpointAt    time.Time
	endpointModel string
)

// Endpoint is the embeddings client the options name, with the id its vectors are kept under: the
// configured endpoint, or, with EmbedAuto and none configured, the Ollama on this machine when it
// has the model. nil, "" when there is none. It is what the memory search uses, so a person who set
// up embeddings for code search has them for memory too.
func Endpoint(ctx context.Context, o Options) (Embedder, string) {
	if strings.TrimSpace(o.EmbedURL) != "" {
		return newEmbedder(o), o.embedID()
	}
	if !o.EmbedAuto {
		return nil, ""
	}
	endpointMu.Lock()
	defer endpointMu.Unlock()
	if endpointAt.IsZero() || time.Since(endpointAt) >= probeEvery {
		endpointAt, endpointModel = time.Now(), localEmbedModel(ctx)
	}
	if endpointModel == "" {
		return nil, ""
	}
	o.EmbedURL, o.EmbedModel = ollamaBase+"/v1", endpointModel
	return newEmbedder(o), o.embedID()
}
