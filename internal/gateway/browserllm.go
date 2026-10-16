package gateway

import (
	"net/http"
	"time"

	"github.com/madkoding/motita/internal/llm"
)

// THE BROWSER MODEL: a tab of the web UI that turned it on asks for work here and answers it (see
// internal/browserllm and web/src/browserLLM.ts). The model's files are the local-voice catalog's
// (voice.BrowserLLMModels), downloaded and served by the voice endpoints; these two carry the
// requests. PROCESS-WIDE, like the other voice endpoints.

// browserJobAnswer is what a tab writes back for a job.
type browserJobAnswer struct {
	Text  string `json:"text"`
	Error string `json:"error"`
}

func (s *Server) browserLLMRoutes(mux *http.ServeMux, plain func(http.HandlerFunc) http.Handler) {
	mux.Handle("GET /v1/browser-llm/next", plain(s.handleBrowserLLMNext))
	mux.Handle("POST /v1/browser-llm/test", plain(s.handleBrowserLLMTest))
	mux.Handle("POST /v1/browser-llm/jobs/{id}", plain(s.handleBrowserLLMAnswer))
}

// errNoBrowserModel is what the browser model routes answer when the gateway was built without one.
const errNoBrowserModel = "this gateway has no browser model"

// handleBrowserLLMNext holds the request open until there is work for the tab, or answers 204.
func (s *Server) handleBrowserLLMNext(w http.ResponseWriter, r *http.Request) {
	b := s.opts.BrowserLLM
	if b == nil {
		writeError(w, http.StatusNotImplemented, errNoBrowserModel)
		return
	}
	job, ok := b.Next(r.Context())
	if !ok {
		w.WriteHeader(http.StatusNoContent)
		return
	}
	writeJSON(w, http.StatusOK, job)
}

// handleBrowserLLMAnswer takes the text a tab wrote for a job.
func (s *Server) handleBrowserLLMAnswer(w http.ResponseWriter, r *http.Request) {
	b := s.opts.BrowserLLM
	if b == nil {
		writeError(w, http.StatusNotImplemented, errNoBrowserModel)
		return
	}
	var body browserJobAnswer
	if !s.decodeBody(w, r, &body) {
		return
	}
	if !b.Answer(r.PathValue("id"), body.Text, body.Error) {
		writeError(w, http.StatusGone, "nobody waits for that job any more")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// browserTestPrompt is what the settings' "Try it" asks the model: a title, the job it is for.
const browserTestPrompt = "Write a title of at most six words for a conversation that starts with: " +
	"\"help me fix the failing test in the Go gateway\". Answer with the title only."

// handleBrowserLLMTest asks the model in the tab for a title and answers it with how long it took,
// so the settings can show that the whole way round works.
func (s *Server) handleBrowserLLMTest(w http.ResponseWriter, r *http.Request) {
	b := s.opts.BrowserLLM
	if b == nil {
		writeError(w, http.StatusNotImplemented, errNoBrowserModel)
		return
	}
	start := time.Now()
	text, err := b.Ask(r.Context(), []llm.Message{{Role: "user", Content: browserTestPrompt}})
	if err != nil {
		writeError(w, http.StatusBadGateway, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"text": text, "ms": time.Since(start).Milliseconds()})
}
