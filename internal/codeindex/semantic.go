package codeindex

import (
	"hash/fnv"
	"math"
	"sort"
)

// The semantic ranking is random indexing: every term gets a fixed random sparse vector, and the
// meaning of a term is the sum of the random vectors of the terms it shares windows with. Terms
// used in the same places end up pointing the same way ("rotate" and "roll" in a log package),
// which BM25 cannot see. It is learned from the folder itself, in this process, with no model
// and no network, so it knows this codebase's vocabulary and nothing else: it is not a neural
// embedding and does not know that two words are synonyms in English if the code never shows it.
const (
	semDim       = 96
	semNonZero   = 8
	semMinCosine = 0.35
	semCandidate = 50
)

// semantic holds a vector per eligible term and one per window.
type semantic struct {
	IDF   map[string]float64
	Ctx   map[string][]float32
	Chunk [][]float32
}

// eligible says whether a term carries meaning: it must appear in at least two windows and not in
// more than half of them.
func eligible(df, chunks int) bool { return df >= 2 && df*2 <= chunks }

// randomVector is the fixed sparse vector of a term, derived from its hash.
func randomVector(term string) []float32 {
	h := fnv.New64a()
	_, _ = h.Write([]byte(term))
	state := h.Sum64() | 1
	v := make([]float32, semDim)
	for i := 0; i < semNonZero; i++ {
		state ^= state << 13
		state ^= state >> 7
		state ^= state << 17
		sign := float32(1)
		if state&(1<<40) != 0 {
			sign = -1
		}
		v[state%semDim] += sign
	}
	return v
}

func addScaled(dst, src []float32, w float64) {
	for i, x := range src {
		dst[i] += float32(w) * x
	}
}

func normalize(v []float32) bool {
	sum := 0.0
	for _, x := range v {
		sum += float64(x) * float64(x)
	}
	if sum == 0 {
		return false
	}
	inv := float32(1 / math.Sqrt(sum))
	for i := range v {
		v[i] *= inv
	}
	return true
}

// weight is how much a term counts in a window.
func (s *semantic) weight(t string, tf float64) float64 { return (1 + math.Log(tf)) * s.IDF[t] }

// buildSemantic learns the term vectors and the window vectors of ix.
func buildSemantic(ix *Index) *semantic {
	n := len(ix.Chunks)
	s := &semantic{IDF: map[string]float64{}, Ctx: map[string][]float32{}}
	random := map[string][]float32{}
	for t, df := range ix.DF {
		if eligible(df, n) {
			s.IDF[t] = math.Log(1 + float64(n)/float64(df))
			random[t] = randomVector(t)
		}
	}
	for i := range ix.Chunks {
		s.learn(&ix.Chunks[i], random)
	}
	for t, v := range s.Ctx {
		if !normalize(v) {
			delete(s.Ctx, t)
		}
	}
	s.Chunk = make([][]float32, n)
	for i := range ix.Chunks {
		s.Chunk[i] = s.vectorOf(ix.Chunks[i].TF)
	}
	return s
}

// learn adds, to every eligible term of a window, the random vectors of the other terms in it.
func (s *semantic) learn(c *chunk, random map[string][]float32) {
	sum := make([]float32, semDim)
	for t, tf := range c.TF {
		if r, ok := random[t]; ok {
			addScaled(sum, r, s.weight(t, tf))
		}
	}
	for t, tf := range c.TF {
		r, ok := random[t]
		if !ok {
			continue
		}
		v := s.Ctx[t]
		if v == nil {
			v = make([]float32, semDim)
			s.Ctx[t] = v
		}
		addScaled(v, sum, 1)
		addScaled(v, r, -s.weight(t, tf))
	}
}

// vectorOf is the normalized sum of the term vectors of a bag of terms, nil when none is known.
func (s *semantic) vectorOf(tf map[string]float64) []float32 {
	v := make([]float32, semDim)
	for t, n := range tf {
		if c, ok := s.Ctx[t]; ok {
			addScaled(v, c, s.weight(t, n))
		}
	}
	if !normalize(v) {
		return nil
	}
	return v
}

// rank orders the windows closest in meaning to the query terms. A nil semantic, a query of
// unknown terms or windows below semMinCosine give nothing.
func (s *semantic) rank(terms []string) []ranked {
	if s == nil {
		return nil
	}
	bag := map[string]float64{}
	for _, t := range terms {
		bag[t] = 1
	}
	q := s.vectorOf(bag)
	if q == nil {
		return nil
	}
	var out []ranked
	for i, v := range s.Chunk {
		if cos := dot(q, v); v != nil && cos >= semMinCosine {
			out = append(out, ranked{i, cos})
		}
	}
	sort.SliceStable(out, func(a, b int) bool { return out[a].score > out[b].score })
	return out[:min(len(out), semCandidate)]
}

func dot(a, b []float32) float64 {
	sum := 0.0
	for i := range a {
		if i < len(b) {
			sum += float64(a[i]) * float64(b[i])
		}
	}
	return sum
}
