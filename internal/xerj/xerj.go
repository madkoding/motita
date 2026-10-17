// Package xerj is the client of a local XERJ node (https://github.com/xerj-org/xerj): a search
// engine that indexes a folder (`xerj autoindex <folder>`) and answers with the matching passage
// instead of the whole file.
//
// motita does not run, install or index anything: it only asks a node the person already started.
// That keeps the dependency out of the binary (stdlib only) and the cost where it belongs: an
// agent that queries an index reads a few passages instead of grepping whole files into its context.
package xerj

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
)

const (
	// DefaultURL is where `xerj` listens when started with no flags.
	DefaultURL = "http://localhost:9200"
	// DefaultIndex is the wildcard of the indices `xerj autoindex` creates with its default prefix.
	DefaultIndex = "ax-*"
	// DefaultSize is how many hits a search returns when the caller does not say.
	DefaultSize = 5
	// MaxSize caps one search: every hit is text that goes into the model's context.
	MaxSize = 20

	maxResponseBytes = 4 << 20
	maxPassageRunes  = 1500
	requestTimeout   = 30 * time.Second
)

// Client asks one XERJ node.
type Client struct {
	URL    string
	APIKey string
	Index  string
	HTTP   *http.Client
}

// New builds a client for url and index; empty values take the defaults.
func New(url, apiKey, index string) *Client {
	if strings.TrimSpace(url) == "" {
		url = DefaultURL
	}
	if strings.TrimSpace(index) == "" {
		index = DefaultIndex
	}
	return &Client{
		URL:    strings.TrimRight(strings.TrimSpace(url), "/"),
		APIKey: apiKey,
		Index:  strings.TrimSpace(index),
		HTTP:   &http.Client{Timeout: requestTimeout},
	}
}

// Hit is one matching passage.
type Hit struct {
	Path    string
	Line    int // 1-based; 0 when the node did not say
	Title   string
	Passage string
	Score   float64
}

// searchBody is the query `xerj search` itself sends: definitions rank above callers, and the
// passage replaces the whole file (measured by XERJ at about 9x fewer tokens).
func searchBody(query string, size int) map[string]any {
	q := strings.TrimSpace(query)
	return map[string]any{
		"size": size,
		"query": map[string]any{"bool": map[string]any{"should": []any{
			map[string]any{"multi_match": map[string]any{
				"query":  q,
				"fields": []string{"name^4", "code^2", "body", "defs^3", "title"},
				"type":   "most_fields",
			}},
			map[string]any{"match_phrase": map[string]any{"defs": map[string]any{"query": q, "boost": 8}}},
			map[string]any{"term": map[string]any{"name": map[string]any{"value": q, "boost": 10}}},
		}}},
		"_source": []string{"ax_path", "line", "start_line", "title"},
		"fields":  []string{"_passage"},
	}
}

type response struct {
	Error any `json:"error"`
	Hits  struct {
		Hits []struct {
			Score  float64 `json:"_score"`
			Source struct {
				Path      string `json:"ax_path"`
				Line      int    `json:"line"`
				StartLine int    `json:"start_line"`
				Title     string `json:"title"`
			} `json:"_source"`
			Fields struct {
				Passage []struct {
					Field   string `json:"field"`
					Ordinal int    `json:"ordinal"`
					Text    string `json:"text"`
				} `json:"_passage"`
			} `json:"fields"`
		} `json:"hits"`
	} `json:"hits"`
}

// Search returns the best passages for a plain-words query. size is clamped to 1..MaxSize.
func (c *Client) Search(ctx context.Context, query string, size int) ([]Hit, error) {
	if strings.TrimSpace(query) == "" {
		return nil, errors.New("the query is empty")
	}
	if size < 1 {
		size = DefaultSize
	}
	size = min(size, MaxSize)
	data, err := c.post(ctx, searchBody(query, size))
	if err != nil {
		return nil, err
	}
	var parsed response
	if err := json.Unmarshal(data, &parsed); err != nil {
		return nil, fmt.Errorf("XERJ answered something that is not JSON: %w", err)
	}
	return parsed.toHits(), nil
}

// post sends one search body and returns the answer's bytes, or why there are none.
func (c *Client) post(ctx context.Context, payload map[string]any) ([]byte, error) {
	body, _ := json.Marshal(payload)
	endpoint := c.URL + "/" + c.Index + "/_search?ignore_unavailable=true"
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("the XERJ address %q is not usable: %w", c.URL, err)
	}
	req.Header.Set("Content-Type", "application/json")
	if c.APIKey != "" {
		req.Header.Set("Authorization", "ApiKey "+c.APIKey)
	}
	res, err := c.HTTP.Do(req)
	if err != nil {
		return nil, fmt.Errorf("no XERJ node answers at %s (start one with `xerj --insecure --data-dir ~/xerj-data` "+
			"and index a folder with `xerj autoindex <folder>`): %w", c.URL, err)
	}
	defer res.Body.Close()
	data, err := io.ReadAll(io.LimitReader(res.Body, maxResponseBytes))
	if err != nil {
		return nil, fmt.Errorf("reading the XERJ answer: %w", err)
	}
	if res.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("XERJ answered HTTP %d: %s", res.StatusCode, firstRunes(string(data), 300))
	}
	return data, nil
}

// bodyField reports whether a passage comes from the document's text, not from a symbol list or title.
func bodyField(field string) bool {
	return field != "defs" && field != "title" && field != "name"
}

// toHits turns the node's answer into hits.
func (r response) toHits() []Hit {
	hits := make([]Hit, 0, len(r.Hits.Hits))
	for _, h := range r.Hits.Hits {
		hit := Hit{Path: h.Source.Path, Title: h.Source.Title, Score: h.Score, Line: h.Source.Line}
		if hit.Line == 0 {
			hit.Line = h.Source.StartLine
		}
		for _, p := range h.Fields.Passage {
			if !bodyField(p.Field) {
				continue
			}
			hit.Passage = firstRunes(strings.TrimSpace(p.Text), maxPassageRunes)
			if hit.Line == 0 {
				hit.Line = p.Ordinal + 1
			}
			break
		}
		hits = append(hits, hit)
	}
	return hits
}

// Format renders hits as the plain text a model reads: path:line, then the passage.
func Format(query string, hits []Hit) string {
	if len(hits) == 0 {
		return fmt.Sprintf("No indexed passage matches %q. The index may not hold it: fall back to search_in_files.", query)
	}
	var sb strings.Builder
	for i, h := range hits {
		if i > 0 {
			sb.WriteString("\n---\n")
		}
		path := h.Path
		if path == "" {
			path = "?"
		}
		if h.Line > 0 {
			fmt.Fprintf(&sb, "%s:%d\n", path, h.Line)
		} else {
			fmt.Fprintf(&sb, "%s\n", path)
		}
		if h.Passage != "" {
			sb.WriteString(h.Passage)
			sb.WriteString("\n")
		}
	}
	return strings.TrimRight(sb.String(), "\n")
}

func firstRunes(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n]) + "…"
}
