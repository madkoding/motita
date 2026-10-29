package config

import (
	"os"
	"path/filepath"

	"github.com/madkoding/motita/internal/memory"
)

// OpenMemory is the person's memory, in MemoryDir. nil when there is no home to keep it in. Plan
// and task mode, the interface and the gateway read and write the same folder of Markdown files.
func OpenMemory() *memory.Store {
	dir := MemoryDir()
	if dir == "" {
		return nil
	}
	return memory.Open(dir)
}

// MemoryDir is the memory: ~/.motita/memory, plain Markdown files another program (Obsidian) can
// open. Empty without a home.
//
// Before there was a memory there were notes, in ~/.motita/notes. The first time the memory is asked
// for, a notes folder with no memory beside it is renamed to it, files and all, so nothing the
// person or the agent wrote is lost. If it cannot be renamed it stays where it is and is what
// MemoryDir answers, so it is still read.
func MemoryDir() string {
	d := Dir()
	if d == "" {
		return ""
	}
	return migrateNotes(filepath.Join(d, "notes"), filepath.Join(d, "memory"))
}

// renameDir moves a folder; a variable so a test can make it fail.
var renameDir = os.Rename

// migrateNotes renames the legacy notes folder to dir when dir does not exist yet, and answers the
// folder the memory is in.
func migrateNotes(legacy, dir string) string {
	if _, err := os.Stat(dir); err == nil {
		return dir
	}
	if fi, err := os.Stat(legacy); err != nil || !fi.IsDir() {
		return dir
	}
	if err := renameDir(legacy, dir); err != nil {
		return legacy
	}
	// the marker the notes scan kept is of no use to the memory
	_ = os.Remove(filepath.Join(dir, ".scanned"))
	return dir
}
