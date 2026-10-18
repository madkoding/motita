package llm

import "encoding/base64"

// PICTURES A TURN SHOWS THE MODEL.
//
// A user turn can carry images next to its text: a screenshot of the desktop the agent is driving,
// so the model sees what it is about to click. Every provider takes them in its own shape, and each
// converter adds them after the turn's text with the helpers below. Only user turns carry them.

// Image is one picture, as its bytes and their media type (image/png, image/jpeg).
type Image struct {
	MediaType string
	Data      []byte
}

// base64 is the picture's bytes in standard base64.
func (img Image) base64() string { return base64.StdEncoding.EncodeToString(img.Data) }

// dataURL is the picture as a data: URL, the shape the OpenAI-style APIs take.
func (img Image) dataURL() string { return "data:" + img.MediaType + ";base64," + img.base64() }

// anthropicImages are the image blocks of the Messages API (and of the claude CLI's stream-json).
func anthropicImages(images []Image) []map[string]any {
	blocks := make([]map[string]any, 0, len(images))
	for _, img := range images {
		blocks = append(blocks, map[string]any{"type": "image", "source": map[string]any{
			"type": "base64", "media_type": img.MediaType, "data": img.base64(),
		}})
	}
	return blocks
}

// openAIContent is a chat-completions turn's content: its text alone, or, with images, a list of
// a text part and one image_url part per picture.
func openAIContent(text string, images []Image) any {
	if len(images) == 0 {
		return text
	}
	parts := []map[string]any{{"type": "text", "text": text}}
	for _, img := range images {
		parts = append(parts, map[string]any{"type": "image_url", "image_url": map[string]any{"url": img.dataURL()}})
	}
	return parts
}

// geminiImages are the inline_data parts of generateContent.
func geminiImages(images []Image) []map[string]any {
	parts := make([]map[string]any, 0, len(images))
	for _, img := range images {
		parts = append(parts, map[string]any{"inlineData": map[string]any{"mimeType": img.MediaType, "data": img.base64()}})
	}
	return parts
}

// responsesImages are the input_image parts of /responses.
func responsesImages(images []Image) []map[string]any {
	parts := make([]map[string]any, 0, len(images))
	for _, img := range images {
		parts = append(parts, map[string]any{"type": "input_image", "image_url": img.dataURL()})
	}
	return parts
}

// claudeImages are the image blocks of the claude CLI's stream-json, the Messages API's own.
func claudeImages(images []Image) []claudeBlock {
	blocks := make([]claudeBlock, 0, len(images))
	for _, img := range images {
		blocks = append(blocks, claudeBlock{Type: "image", Source: &claudeSource{
			Type: "base64", MediaType: img.MediaType, Data: img.base64(),
		}})
	}
	return blocks
}
