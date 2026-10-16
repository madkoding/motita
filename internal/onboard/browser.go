package onboard

// BrowserProvider is the entry of the model that runs in an open tab of the web UI. It is kept
// out of Providers(): the setup wizard connects a provider by a key or a login, and this one is
// turned on from the web UI's Settings → Models, so only the web UI lists it.
func BrowserProvider() Provider {
	return Provider{
		ID:     "browser",
		Name:   "Browser (WebGPU)",
		Short:  "Browser",
		Access: "a small model that runs in this browser tab, on your graphics card",
		Models: []Model{
			{ID: "qwen2.5-0.5b-instruct", Label: "Qwen2.5 0.5B", Note: "titles and summaries, while a tab is open",
				Tier: "basic", Cost: "local"},
		},
	}
}
