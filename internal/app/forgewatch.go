package app

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"strings"
	"time"

	"github.com/madkoding/motita/internal/gitforge"
)

// forgeWatchTimeout is how long `pr watch` waits by default: under the hour the agent gives a
// command that waits on the forge, so it ends with a report and is never killed mid-wait.
const forgeWatchTimeout = 55 * time.Minute

// forgePRWatch is `motita forge pr watch`: it reads the pull request again and again, with no model
// in the loop, and returns only when something happened. Each reading is a few requests to the
// host; the model is woken once per event instead of once per look.
func (op Options) forgePRWatch(ctx context.Context, args []string) int {
	fs := flag.NewFlagSet("forge pr watch", flag.ContinueOnError)
	fs.SetOutput(op.Err)
	timeout := fs.Duration("timeout", forgeWatchTimeout, "")
	untilMerged := fs.Bool("until-merged", false, "")
	positional, err := parseInterspersed(fs, args)
	if err != nil {
		return ConfigError
	}
	repo, api, err := op.openRepo(ctx)
	if err != nil {
		fmt.Fprintf(op.Err, "❌ %v\n", err)
		return RunError
	}
	pr, code := op.choosePR(ctx, api, repo, positional)
	if code != Success {
		return code
	}
	ctx, cancel := context.WithTimeout(ctx, *timeout)
	defer cancel()
	var last gitforge.Snapshot
	w := &gitforge.Watcher{
		Src: api, Remote: repo.remote, Number: pr.Number, Sleep: forgeSleep,
		Pace:   gitforge.Pace{Every: forgePollEvery, MaxQuiet: 6, MaxBackoff: 16},
		OnRead: func(s gitforge.Snapshot) { last = s },
	}
	label := fmt.Sprintf("%s#%d", repo.remote.Path, pr.Number)
	for {
		evs, err := w.Next(ctx)
		if errors.Is(err, context.DeadlineExceeded) {
			fmt.Fprintf(op.Out, "%s: nothing new after %s (CI: %s). Run it again to keep waiting.\n",
				label, *timeout, orNone(last.CI.State))
			return forgeCIPending
		}
		if err != nil {
			fmt.Fprintf(op.Err, "❌ %v\n", err)
			return RunError
		}
		if done, code := op.reportEvents(ctx, repo, api, label, evs, *untilMerged); done {
			return code
		}
	}
}

func orNone(s string) string {
	if s == "" {
		return gitforge.StateNone
	}
	return s
}

// reportEvents prints what happened and says whether the watch is over, with its exit status. A
// passed CI, or a CI that never shows up, ends it only when the caller is not waiting for the merge.
func (op Options) reportEvents(
	ctx context.Context, repo forgeRepo, api gitforge.API, label string, evs []gitforge.Event, untilMerged bool,
) (bool, int) {
	code, done := Success, false
	for _, e := range evs {
		switch e.Kind {
		case gitforge.EventMerged:
			fmt.Fprintf(op.Out, "%s was merged.\n", label)
			return true, Success
		case gitforge.EventClosed:
			fmt.Fprintf(op.Out, "%s was closed without being merged.\n", label)
			return true, Success
		case gitforge.EventCIFailed:
			op.printCIFailure(ctx, repo, api, label, *e.CI)
			code, done = RunError, true
		case gitforge.EventConflict:
			fmt.Fprintf(op.Out, "%s: the branch conflicts with its base. Merge the base in, resolve, and push.\n", label)
			code, done = RunError, true
		case gitforge.EventReview:
			op.printComments(label, e.Comments)
			code, done = RunError, true
		case gitforge.EventCIPassed:
			fmt.Fprintf(op.Out, "%s: the CI passed.\n", label)
			done = done || !untilMerged
		default: // gitforge.EventNoCI
			fmt.Fprintf(op.Out, "%s: no CI reported any check.\n", label)
			if !done && !untilMerged {
				code, done = forgeCIPending, true
			}
		}
	}
	return done, code
}

// printCIFailure says the CI of label failed, lists its checks and prints the logs of the ones
// that failed.
func (op Options) printCIFailure(
	ctx context.Context, repo forgeRepo, api gitforge.API, label string, st gitforge.CIStatus,
) {
	fmt.Fprintf(op.Out, "%s: the CI failed.\n", label)
	for _, c := range st.Checks {
		fmt.Fprintln(op.Out, ciCheckLine(c))
	}
	op.printFailureLogs(ctx, repo, api, st)
}

// printComments lists the new comments and reviews, each with who said it and where.
func (op Options) printComments(label string, cs []gitforge.Comment) {
	fmt.Fprintf(op.Out, "%s: %d new comment(s) or review(s):\n", label, len(cs))
	for _, c := range cs {
		head := c.Kind
		if c.State != "" {
			head += " " + c.State
		}
		if c.Path != "" {
			head += " on " + c.Path
		}
		if c.Author != "" {
			head += " by @" + c.Author
		}
		fmt.Fprintf(op.Out, "- [%s]", head)
		if c.URL != "" {
			fmt.Fprintf(op.Out, " %s", c.URL)
		}
		fmt.Fprintf(op.Out, "\n  %s\n", strings.ReplaceAll(c.Body, "\n", "\n  "))
	}
}
