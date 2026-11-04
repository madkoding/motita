package llm

import (
	"context"
	"errors"
	"time"

	"github.com/madkoding/motita/internal/config"
)

// claudeProbeTimeout bounds one ProbeClaudeQuota.
const claudeProbeTimeout = time.Minute

// claudeProbeArgs is claude's command line for ProbeClaudeQuota: the smallest model, no tools, no
// settings, no session left behind, and a system prompt of one line instead of claude's own.
var claudeProbeArgs = append(claudeBareArgs(), "--model", "haiku",
	"--system-prompt", "Answer with the single word: ok", "--max-turns", "1", "--permission-mode", "dontAsk")

// ProbeClaudeQuota asks claude the smallest question there is so that it states the subscription's
// limits, which it only does with an answer; they land in the ledger like those of any turn. The
// question is not counted as a call. It fails when claude cannot run or states no limits.
func ProbeClaudeQuota(ctx context.Context) error {
	p, err := startClaude(ctx, claudeProbeTimeout, claudeProbeArgs, claudeEnv(config.LLM{MaxTokens: 16}))
	if err != nil {
		return err
	}
	// One frame, so nothing waits on claude; a claude that died shows up as the end of its output.
	_, frames, _ := claudeFrames([]Message{{Role: "user", Content: "ok"}})
	_ = p.replay(frames)
	if !p.drain(claudeProbeTimeout) {
		return errors.New("claude stated no subscription limits")
	}
	noteClaudeSignIn(true)
	return nil
}
