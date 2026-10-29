package voice

import (
	"bytes"
	"context"
	"encoding/binary"
	"math"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"sync/atomic"
	"testing"
)

func TestSplitText(t *testing.T) {
	cases := []struct {
		name, text string
		limit      int
		want       []string
	}{
		{"short", "Hola. Que tal?", 100, []string{"Hola. Que tal?"}},
		{"at sentence ends", "Uno. Dos. Tres.", 8, []string{"Uno.", "Dos.", "Tres."}},
		{"packs sentences", "Uno. Dos. Tres.", 10, []string{"Uno. Dos.", "Tres."}},
		{"long sentence", "abcdefghij", 4, []string{"abcd", "efgh", "ij"}},
		{"no end", "uno. dos", 100, []string{"uno. dos"}},
		{"wide end", "你好。再见", 3, []string{"你好。", "再见"}},
		{"blank", "  \n ", 5, nil},
	}
	for _, c := range cases {
		if got := splitText(c.text, c.limit); !slices.Equal(got, c.want) {
			t.Errorf("%s: %q, want %q", c.name, got, c.want)
		}
	}
}

// wavOf builds a minimal wav with a LIST chunk (odd-sized, padded) before the samples.
func wavOf(samples string) []byte {
	var b bytes.Buffer
	b.WriteString("RIFF\x00\x00\x00\x00WAVEfmt ")
	_ = binary.Write(&b, binary.LittleEndian, uint32(2))
	b.WriteString("ab")
	b.WriteString("LIST")
	_ = binary.Write(&b, binary.LittleEndian, uint32(1))
	b.WriteString("x\x00data")
	_ = binary.Write(&b, binary.LittleEndian, uint32(len(samples)))
	b.WriteString(samples)
	return b.Bytes()
}

func TestJoinWAV(t *testing.T) {
	got, err := joinWAV([][]byte{wavOf("AAAA"), wavOf("BB")})
	if err != nil {
		t.Fatal(err)
	}
	start, end, err := wavData(got)
	if err != nil || string(got[start:end]) != "AAAABB" {
		t.Fatalf("samples = %q %v", got[start:end], err)
	}
	if int(binary.LittleEndian.Uint32(got[4:8])) != len(got)-8 {
		t.Error("the RIFF size is not patched")
	}
}

func TestJoinWAVRejectsWhatIsNotWAV(t *testing.T) {
	good := wavOf("AA")
	for name, parts := range map[string][][]byte{
		"first":     {[]byte("nope"), good},
		"later":     {good, []byte("nope")},
		"no data":   {[]byte("RIFF\x00\x00\x00\x00WAVEfmt \x00\x00\x00\x00")},
		"truncated": {good[:len(good)-1], good},
	} {
		_, err := joinWAV(parts)
		if name == "truncated" {
			if err != nil {
				t.Errorf("%s: a short data chunk is clamped, got %v", name, err)
			}
			continue
		}
		if err == nil {
			t.Errorf("%s: want an error", name)
		}
	}
}

// A text over 600 characters goes out in pieces and comes back as one wav.
func TestSpeakDashScopeSplitsLongTexts(t *testing.T) {
	var calls atomic.Int32
	var srv *httptest.Server
	srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/generation") {
			n := calls.Add(1)
			_, _ = w.Write([]byte(`{"output":{"audio":{"url":"` + srv.URL + `/` + string(rune('a'+n)) + `.wav"}}}`))
			return
		}
		_, _ = w.Write(wavOf(strings.TrimSuffix(strings.TrimPrefix(r.URL.Path, "/"), ".wav")))
	}))
	defer srv.Close()
	text := strings.Repeat("x", 500) + ". " + strings.Repeat("y", 500) + "."
	audio, ctype, err := Speaker{API: "dashscope", BaseURL: srv.URL}.Speak(context.Background(), text, "v")
	if err != nil || ctype != "audio/wav" || calls.Load() != 2 {
		t.Fatalf("%v %q, %d calls", err, ctype, calls.Load())
	}
	if s, e, _ := wavData(audio); string(audio[s:e]) != "bc" {
		t.Errorf("samples = %q", audio[s:e])
	}
}

func TestSpeakDashScopeLaterChunkFailures(t *testing.T) {
	var srv *httptest.Server
	srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/generation") {
			_, _ = w.Write([]byte(`{"output":{"audio":{"url":"` + srv.URL + `/a.wav"}}}`))
			return
		}
		_, _ = w.Write([]byte("not a wav"))
	}))
	defer srv.Close()
	text := strings.Repeat("x", 500) + ". " + strings.Repeat("y", 500) + "."
	if _, _, err := (Speaker{API: "dashscope", BaseURL: srv.URL}).Speak(context.Background(), text, "v"); err == nil {
		t.Error("audio that is not a wav cannot be joined")
	}
}

func TestPutSizeClampsWhatDoesNotFit(t *testing.T) {
	for n, want := range map[int]uint32{7: 7, -1: math.MaxUint32, math.MaxUint32 + 1: math.MaxUint32} {
		b := make([]byte, 4)
		putSize(b, n)
		if got := binary.LittleEndian.Uint32(b); got != want {
			t.Errorf("%d: %d, want %d", n, got, want)
		}
	}
}
