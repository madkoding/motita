package agent

// This file proves the read-only guarantee the way it matters: by trying to change
// the filesystem and checking that nothing changed. The policy tests assert the
// decision; these assert the effect, which is what "nothing is written" means.

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/madkoding/starlight/internal/config"
	"github.com/madkoding/starlight/internal/execx"
	"github.com/madkoding/starlight/internal/logx"
)

// realExecutor runs the request the way the sandbox does, so the test exercises the
// real path (a process is started with the program and arguments, with no shell).
func realExecutor() func(context.Context, execx.Request) (string, bool, int, error) {
	return func(ctx context.Context, r execx.Request) (string, bool, int, error) {
		return execx.Run(ctx, r)
	}
}

func agentForEffect(t *testing.T, readOnly bool, dir string) *Agent {
	t.Helper()
	cfg := config.Default()
	cfg.Agent.ReadOnly = readOnly
	cfg.Agent.WorkspaceDir = dir
	log, err := logx.New(logx.Options{Level: logx.Error, Console: false})
	if err != nil {
		t.Fatal(err)
	}
	a := &Agent{cfg: cfg, log: log}
	a.ExecCommand = realExecutor()
	return a
}

// TestNothingIsWrittenInReadOnlyMode is the guarantee, measured: a series of actions
// that would each change something are proposed, and afterwards the filesystem is
// exactly as it was.
func TestNothingIsWrittenInReadOnlyMode(t *testing.T) {
	dir := t.TempDir()
	victim := filepath.Join(dir, "importante.txt")
	if err := os.WriteFile(victim, []byte("contenido-original\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	before, err := os.ReadFile(victim)
	if err != nil {
		t.Fatal(err)
	}

	a := agentForEffect(t, true, dir)

	// Every one of these would change something if it ran.
	writers := []Command{
		{Command: "rm -f " + victim},
		{Command: "echo PISADO > " + victim},
		{Command: "echo MAS >> " + victim},
		{Command: "mv " + victim + " " + victim + ".movido"},
		{Command: "cp " + victim + " copia.txt"},
		{Command: "touch " + filepath.Join(dir, "nuevo.txt")},
		{Command: "mkdir " + filepath.Join(dir, "nuevodir")},
		{Command: "truncate -s 0 " + victim},
		{Command: "tee " + victim},
		{Command: "dd if=/dev/zero of=" + victim + " bs=1 count=1"},
		{Command: "sed -i s/original/otro/ " + victim},
		{Command: "sh -c 'rm -f " + victim + "'"},
		{Command: "bash -c 'echo x > " + victim + "'"},
	}
	for _, c := range writers {
		output, err := a.runActions(context.Background(), []Command{c}, "ro: ")
		if err == nil {
			t.Errorf("%q must be refused", c.Command)
		}
		if !strings.Contains(output, "refused") {
			t.Errorf("%q must be reported as refused, output: %q", c.Command, output)
		}
	}

	// The file is untouched, still there, with its original content.
	after, err := os.ReadFile(victim)
	if err != nil {
		t.Fatalf("the protected file must still exist: %v", err)
	}
	if string(after) != string(before) {
		t.Errorf("the file changed: %q -> %q", before, after)
	}
	// And nothing new appeared.
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 || entries[0].Name() != "importante.txt" {
		var names []string
		for _, e := range entries {
			names = append(names, e.Name())
		}
		t.Errorf("the directory must hold only the original file, found: %v", names)
	}
}

// TestReadersRunForRealInReadOnlyMode: the mode has to be useful, so a reader must
// actually run and bring back real output.
func TestReadersRunForRealInReadOnlyMode(t *testing.T) {
	dir := t.TempDir()
	file := filepath.Join(dir, "datos.txt")
	if err := os.WriteFile(file, []byte("linea-uno\nlinea-dos\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	a := agentForEffect(t, true, dir)
	output, err := a.runActions(context.Background(), []Command{
		{Command: "cat " + file},
		{Command: "wc -l " + file},
	}, "ro: ")
	if err != nil {
		t.Fatalf("readers must run: %v", err)
	}
	if !strings.Contains(output, "linea-uno") {
		t.Errorf("cat's output must come back: %q", output)
	}
	// wc -l on a file with two lines prints 2.
	if !strings.Contains(output, "2") {
		t.Errorf("wc's output must come back: %q", output)
	}
}

// TestTheRefusalReachesTheModel: the reason is the useful part — it is what the model
// reads to correct itself on the next attempt.
func TestTheRefusalReachesTheModel(t *testing.T) {
	dir := t.TempDir()
	a := agentForEffect(t, true, dir)

	output, err := a.runActions(context.Background(), []Command{
		{Command: "rm -rf /tmp/x"},
	}, "ro: ")
	if err == nil {
		t.Fatal("the refusal must be an error, so the attempt counts as failed")
	}
	if !strings.Contains(err.Error(), "rm") {
		t.Errorf("the error must name the command: %v", err)
	}
	// The reason is in the output, which is what goes back to the model.
	if !strings.Contains(output, "refused") || !strings.Contains(output, "rm") {
		t.Errorf("the model must be told what was refused and why: %q", output)
	}
}

// TestOutsideReadOnlyModeTheSameActionsRun: the other mode must not be affected by
// any of this.
func TestOutsideReadOnlyModeTheSameActionsRun(t *testing.T) {
	dir := t.TempDir()
	file := filepath.Join(dir, "escrito.txt")

	a := agentForEffect(t, false, dir)
	_, err := a.runActions(context.Background(), []Command{
		{Command: "echo contenido > " + file},
	}, "rw: ")
	if err != nil {
		t.Fatalf("outside read-only mode the action must run: %v", err)
	}
	got, err := os.ReadFile(file)
	if err != nil {
		t.Fatalf("the file must have been created: %v", err)
	}
	if !strings.Contains(string(got), "contenido") {
		t.Errorf("the file must hold what the action wrote, got %q", got)
	}
}
