//go:build !linux

package plan

import "testing"

// makeDumpable is Linux-only: elsewhere there is no dumpable flag to undo.
func makeDumpable(*testing.T) {}
