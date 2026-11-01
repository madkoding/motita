package agent

import (
	"strings"
	"sync"
	"time"

	"github.com/madkoding/motita/internal/policy"
)

// READS SIDE BY SIDE. A round that opens with `cat a.go`, `cat b.go`, `grep -rn X .` waited for each
// in turn, although none of them can change what the next one reads. Consecutive reads the policy
// allows now run at the same time, at most maxParallelReads, and are reported in the order the
// model wrote them, so the round's output reads exactly as before - only sooner.

// maxParallelReads bounds how many reads of one round run at the same time.
const maxParallelReads = 4

// readBatch is how many of the actions at the head of rest are reads that may run side by side:
// shell commands that only read, that the policy allows without a question, and that the working
// memory does not already hold.
func (rr *roundRun) readBatch(rest []Command) int {
	n := 0
	for n < len(rest) && n < maxParallelReads && rr.parallelRead(rest[n]) {
		n++
	}
	return n
}

// parallelRead says one action may run beside the reads around it.
func (rr *roundRun) parallelRead(c Command) bool {
	if !isRead(c) || strings.TrimSpace(c.Command) == "" {
		return false
	}
	if rr.mem != nil {
		if _, held := rr.mem.recall(c.Command); held {
			return false
		}
	}
	return rr.a.planRequest(c.Command).Verdict == policy.Allow
}

// readResult is what one read of a batch printed.
type readResult struct {
	output    string
	truncated bool
	exit      int
	err       error
	took      time.Duration
}

// runReads runs a batch of reads (see readBatch) at the same time, then reports and records each
// one in order; first is the index of the batch's first action in the round.
func (rr *roundRun) runReads(first int, batch []Command) {
	a := rr.a
	results := make([]readResult, len(batch))
	var wg sync.WaitGroup
	for i, action := range batch {
		wg.Add(1)
		go func(i int, line string) {
			defer wg.Done()
			started := time.Now()
			r := &results[i]
			r.output, r.truncated, r.exit, r.err = a.exec(rr.ctx, a.planRequest(line).Request)
			r.took = time.Since(started)
		}(i, action.Command)
	}
	wg.Wait()
	for i, action := range batch {
		r := results[i]
		rr.announce(action, "running: %s", action.Command)
		a.log.Info(rr.prefix+"running in sandbox", "n", first+i+1, "command", action.Command,
			"description", truncate(action.Description, 120), "parallel", len(batch))
		a.clock.commands.Add(int64(r.took))
		a.clock.commandRuns.Add(1)
		rr.record(action, r.output, r.truncated, r.exit, r.err)
		if r.err != nil {
			rr.lastErr = errRun(first+i, action.Command, r.err)
		}
	}
}
