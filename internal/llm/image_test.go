package llm

import (
	"encoding/json"
	"strings"
	"testing"
)

// shot is the picture every converter is handed.
var shot = Image{MediaType: "image/png", Data: []byte("png")}

// asJSON is v as the JSON the request carries.
func asJSON(t *testing.T, v any) string {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func TestEveryProviderCarriesTheTurnsPictures(t *testing.T) {
	msgs := []Message{{Role: "system", Content: "sys"}, {Role: "user", Content: "look", Images: []Image{shot}}}
	const b64 = "cG5n" // "png"
	_, anth := anthropicTextConversation(msgs)
	_, anthTools := toAnthropicMessages(msgs)
	_, gem := geminiTextContents(msgs)
	_, gemTools := toGeminiContents(msgs)
	c := &Client{}
	_, frames, err := claudeFrames(msgs)
	if err != nil {
		t.Fatal(err)
	}
	cases := map[string]struct {
		v    any
		want string
	}{
		"anthropic":       {anth, `"source":{"data":"` + b64 + `","media_type":"image/png","type":"base64"}`},
		"anthropic tools": {anthTools, `"type":"image"`},
		"openai":          {toOpenAIMessages(msgs), `"image_url":{"url":"data:image/png;base64,` + b64 + `"}`},
		"gemini":          {gem, `"inlineData":{"data":"` + b64 + `","mimeType":"image/png"}`},
		"gemini tools":    {gemTools, `"inlineData"`},
		"responses": {c.responsesBody(msgs, nil, false),
			`"image_url":"data:image/png;base64,` + b64 + `","type":"input_image"`},
		"claude-code": {frames, `"source":{"type":"base64","media_type":"image/png","data":"` + b64 + `"}`},
	}
	for name, tc := range cases {
		if got := asJSON(t, tc.v); !strings.Contains(got, tc.want) {
			t.Errorf("%s: %s lacks %s", name, got, tc.want)
		}
	}
}

func TestATurnWithoutPicturesStaysPlainText(t *testing.T) {
	got := asJSON(t, toOpenAIMessages([]Message{{Role: "user", Content: "hi"}}))
	if got != `[{"role":"user","content":"hi"}]` {
		t.Fatalf("got %s", got)
	}
}
