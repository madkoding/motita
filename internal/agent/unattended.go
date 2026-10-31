package agent

import "context"

// unattendedKey marks a context whose run has nobody at the keyboard.
type unattendedKey struct{}

// WithUnattended marks ctx as a run nobody is watching: a scheduled task or a routine. The agent
// it reaches does not stop to ask; it proceeds on the assumption its analysis states, as a batch
// run does. Asking there ended the run with a question nobody would read, and the next firing
// found that question as the last thing the conversation said.
func WithUnattended(ctx context.Context) context.Context {
	return context.WithValue(ctx, unattendedKey{}, true)
}

// Unattended reports whether ctx was marked by WithUnattended.
func Unattended(ctx context.Context) bool {
	on, _ := ctx.Value(unattendedKey{}).(bool)
	return on
}
