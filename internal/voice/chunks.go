package voice

import (
	"encoding/binary"
	"errors"
	"strings"
)

// splitText cuts text into pieces of at most limit characters, at sentence ends when it can and at
// the limit when a sentence is longer than that.
func splitText(text string, limit int) []string {
	var out []string
	var cur []rune
	flush := func() {
		if t := strings.TrimSpace(string(cur)); t != "" {
			out = append(out, t)
		}
		cur = cur[:0]
	}
	for _, sentence := range sentences(text) {
		r := []rune(sentence)
		if len(cur)+len(r) > limit {
			flush()
		}
		for len(r) > limit {
			cur = append(cur, r[:limit]...)
			flush()
			r = r[limit:]
		}
		cur = append(cur, r...)
	}
	flush()
	return out
}

// sentences splits text after each sentence end (. ! ? or their full-width forms, and newlines).
func sentences(text string) []string {
	var out []string
	start := 0
	for i, r := range text {
		if strings.ContainsRune(".!?。！？\n", r) {
			end := i + len(string(r))
			out = append(out, text[start:end])
			start = end
		}
	}
	if start < len(text) {
		out = append(out, text[start:])
	}
	return out
}

// wavData finds the data chunk of a RIFF/WAVE file: where its samples start and end.
func wavData(b []byte) (start, end int, err error) {
	if len(b) < 12 || string(b[:4]) != "RIFF" || string(b[8:12]) != "WAVE" {
		return 0, 0, errors.New("the audio is not a wav file")
	}
	for off := 12; off+8 <= len(b); {
		size := int(binary.LittleEndian.Uint32(b[off+4 : off+8]))
		if string(b[off:off+4]) == "data" {
			start = off + 8
			end = min(start+size, len(b))
			return start, end, nil
		}
		off += 8 + size + size&1
	}
	return 0, 0, errors.New("the wav file has no data chunk")
}

// joinWAV joins wav files of one format into one: the first file's header, every file's samples.
func joinWAV(parts [][]byte) ([]byte, error) {
	start, end, err := wavData(parts[0])
	if err != nil {
		return nil, err
	}
	out := append([]byte(nil), parts[0][:end]...)
	for _, p := range parts[1:] {
		s, e, err := wavData(p)
		if err != nil {
			return nil, err
		}
		out = append(out, p[s:e]...)
	}
	binary.LittleEndian.PutUint32(out[4:8], uint32(len(out)-8))
	binary.LittleEndian.PutUint32(out[start-4:start], uint32(len(out)-start))
	return out, nil
}
