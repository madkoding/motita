package memory

import (
	"context"
	"errors"
	"math"
)

// Neural embeddings are OPTIONAL and leave the machine: the text of every passage is sent once to
// the endpoint the person set for code search (Settings → Code search), and the vectors are kept in
// memory so a passage is not sent again until it changes.
const (
	embedBatch     = 32
	embedMinCosine = 0.25
)

// maxEmbedded caps how many passages are ever sent, because the cost and the privacy exposure grow
// with it. It is a variable so a test can lower it.
var maxEmbedded = 4000

// embedded ranks the children allowed by keep by the cosine of their embeddings with the query's.
func embedded(ctx context.Context, ix *index, e Embedder, id, query string, keep func(int) bool) ([]scored, error) {
	c := ix.emb
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.id != id {
		c.id, c.m = id, map[uint64]vec{}
	}
	var missing []string
	var keys []uint64
	for _, k := range ix.kids {
		text := k.embedText()
		key := textKey(text)
		if _, ok := c.m[key]; !ok && len(c.m)+len(missing) < maxEmbedded {
			missing, keys = append(missing, text), append(keys, key)
		}
	}
	for start := 0; start < len(missing); start += embedBatch {
		end := min(start+embedBatch, len(missing))
		vs, err := e.Embed(ctx, missing[start:end])
		if err != nil {
			return nil, err
		}
		if len(vs) != end-start {
			return nil, errors.New("the endpoint answered a different number of vectors than it was sent texts")
		}
		for i, v := range vs {
			c.m[keys[start+i]] = unitOf(v)
		}
	}
	qs, err := e.Embed(ctx, []string{query})
	if err != nil {
		return nil, err
	}
	if len(qs) != 1 {
		return nil, errors.New("the endpoint did not answer the query")
	}
	q := unitOf(qs[0])
	score := map[int]float64{}
	for i, k := range ix.kids {
		v, ok := c.m[textKey(k.embedText())]
		if !ok || !keep(i) {
			continue
		}
		if cos := cosine(q, v); cos >= embedMinCosine {
			score[i] = cos
		}
	}
	return rank(score), nil
}

// unitOf is v scaled to length one (a zero vector stays zero).
func unitOf(v []float32) vec {
	out := append(vec{}, v...)
	var n float64
	for _, x := range out {
		n += float64(x) * float64(x)
	}
	if n == 0 {
		return out
	}
	inv := float32(1 / math.Sqrt(n))
	for i := range out {
		out[i] *= inv
	}
	return out
}

// cosine of two unit vectors; vectors of different lengths (a model change in the middle of a run)
// resemble nothing.
func cosine(a, b vec) float64 {
	if len(a) != len(b) {
		return 0
	}
	return dot(a, b)
}
