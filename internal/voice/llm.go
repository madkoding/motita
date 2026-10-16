package voice

// THE BROWSER MODEL: a small language model the page runs itself on WebGPU, for the cheap jobs
// (titles, summaries). It lives in this catalog because the machinery is the same one the voices
// use: the gateway downloads from fixed addresses, checks every file's hash and serves the files
// from this origin, so the page's policy keeps naming nothing but 'self'. The voice lists leave
// these entries out (the page filters on EngineLLM).

// EngineLLM is a text model of the catalog: it answers the gateway's requests, it does not speak.
const EngineLLM = "llm"

// The ids of the browser model: one 4-bit build for a card with 16-bit floats in its shaders,
// and a larger one for a card without them.
const (
	BrowserLLMID    = "llm-qwen25-0.5b"
	BrowserLLMIDF32 = BrowserLLMID + "-f32"
)

// qwenRepo is onnx-community's ONNX export of Qwen2.5-0.5B-Instruct, at a fixed commit.
const qwenRepo = "https://huggingface.co/onnx-community/Qwen2.5-0.5B-Instruct/resolve/" +
	"22942cb7d7ba4cc81bb4673549ca4d4614469b5e/"

// browserLLM is the model with the weights file it runs: the tokenizer and its settings are the
// same for both builds, the weights are renamed model.onnx.
func browserLLM(id, name string, weights ModelFile) Model {
	weights.Name = "model.onnx"
	return Model{ID: id, Name: name, Lang: "mul", Quality: "low", Engine: EngineLLM, Runtime: GPURuntime,
		Files: []ModelFile{
			{Name: "tokenizer.json", URL: qwenRepo + "tokenizer.json", Size: 7031673,
				SHA256: "a8506e7111b80c6d8635951a02eab0f4e1a8e4e5772da83846579e97b16f61bf"},
			{Name: "tokenizer_config.json", URL: qwenRepo + "tokenizer_config.json", Size: 7849,
				SHA256: "6a14243f0ae04884e597b3f032c05e15de9f417318fdcac24ab4e2680e691430"},
			weights,
		}}
}

// BrowserLLMModels are the builds of the browser model, the lighter one first.
var BrowserLLMModels = []Model{
	browserLLM(BrowserLLMID, "Qwen2.5 0.5B (4-bit, 16-bit floats)",
		ModelFile{URL: qwenRepo + "onnx/model_q4f16.onnx", Size: 483003582,
			SHA256: "b11c1dd99efd57e6c6e5bc4443a019931a5fbd5dd500d48644d8225f5ce0b2cb"}),
	browserLLM(BrowserLLMIDF32, "Qwen2.5 0.5B (4-bit, 32-bit floats)",
		ModelFile{URL: qwenRepo + "onnx/model_q4.onnx", Size: 786156820,
			SHA256: "09235a3b1c135cd04ef570e5053b5c079e028078a2cc5f76ba6a251e91bf3296"}),
}
