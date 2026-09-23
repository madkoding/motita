package gateway

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The service file exists so a process started LATER can find a gateway that is already running.
// A file written by one process and read by another is the only thing that makes that possible,
// so the round trip is the first property to pin.

func TestTheServiceFileCanBeReadBackWhatWasWritten(t *testing.T) {
	path := filepath.Join(t.TempDir(), "gateway.json")
	want := ServiceFile{
		Address: "127.0.0.1:7477",
		Token:   "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef",
		PID:     4321,
		Owned:   false,
	}

	if err := WriteServiceFile(path, want); err != nil {
		t.Fatalf("WriteServiceFile: %v", err)
	}
	got, err := ReadServiceFile(path)
	if err != nil {
		t.Fatalf("ReadServiceFile: %v", err)
	}

	if got.Address != want.Address || got.Token != want.Token || got.PID != want.PID || got.Owned != want.Owned {
		t.Fatalf("read back %+v, want %+v", got, want)
	}
}

// It carries the bearer token, and a token any user on the machine can read is not a token. The
// mode is checked rather than trusted: umask is the classic reason a file asked for as 0600 lands
// as 0644.
func TestTheServiceFileIsNotWorldReadable(t *testing.T) {
	path := filepath.Join(t.TempDir(), "gateway.json")
	if err := WriteServiceFile(path, ServiceFile{Address: "127.0.0.1:7477", Token: "x", PID: 1}); err != nil {
		t.Fatalf("WriteServiceFile: %v", err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("Stat: %v", err)
	}
	if perm := info.Mode().Perm(); perm != 0o600 {
		t.Fatalf("the service file mode is %o, the token inside it requires 0600", perm)
	}
}

// A stale file that names a dead gateway is worse than no file, because discovery trusts it.
// Overwriting in place is what keeps one file meaning one gateway.
func TestWritingTheServiceFileReplacesAnOlderOne(t *testing.T) {
	path := filepath.Join(t.TempDir(), "gateway.json")
	if err := WriteServiceFile(path, ServiceFile{Address: "127.0.0.1:1111", Token: "old", PID: 1}); err != nil {
		t.Fatalf("WriteServiceFile: %v", err)
	}
	if err := WriteServiceFile(path, ServiceFile{Address: "127.0.0.1:2222", Token: "new", PID: 2}); err != nil {
		t.Fatalf("WriteServiceFile: %v", err)
	}
	got, err := ReadServiceFile(path)
	if err != nil {
		t.Fatalf("ReadServiceFile: %v", err)
	}
	if got.Address != "127.0.0.1:2222" || got.Token != "new" {
		t.Fatalf("the file still describes the old gateway: %+v", got)
	}
}

// "There is no gateway" is the normal state on a fresh machine, and it is a question the caller
// asks constantly. Reporting it as an error would make every caller write the same ignore, and one
// of them would forget.
func TestReadingAMissingServiceFileIsNotAFailure(t *testing.T) {
	path := filepath.Join(t.TempDir(), "absent.json")
	got, err := ReadServiceFile(path)
	if err != nil {
		t.Fatalf("reading a missing service file must not fail: %v", err)
	}
	if !got.IsZero() {
		t.Fatalf("a missing service file must read as empty, got %+v", got)
	}
}

// A truncated or hand-edited file must NOT be silently treated as "no gateway". An empty answer
// would send the caller off to start a SECOND gateway while the first one is listening - two
// services on one machine, which is the failure this file exists to prevent.
func TestReadingGarbageIsAnError(t *testing.T) {
	path := filepath.Join(t.TempDir(), "gateway.json")
	if err := os.WriteFile(path, []byte("{not json"), 0o600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	if _, err := ReadServiceFile(path); err == nil {
		t.Fatal("a corrupt service file must be reported, not treated as an absent gateway")
	}
}

// A file that cannot be read for a reason OTHER than absence is reported: an unreadable file is a
// broken installation, and answering "no gateway" would have the caller start a second one on top
// of the first.
func TestAnUnreadableServiceFileIsAnError(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "gateway.json")
	if err := os.MkdirAll(path, 0o700); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	if _, err := ReadServiceFile(path); err == nil {
		t.Fatal("a directory where the service file should be must be reported as an error")
	}
}

// Removing a gateway that is not described is not a failure: the process that owned it may have
// died without cleaning up, and the next start removes the stale entry anyway. A failure there
// would turn normal recovery into an error.
func TestRemovingAnAbsentServiceFileIsNotAFailure(t *testing.T) {
	if err := RemoveServiceFile(filepath.Join(t.TempDir(), "absent.json")); err != nil {
		t.Fatalf("removing a missing service file must not fail: %v", err)
	}
}

func TestRemovingTheServiceFileTakesItAway(t *testing.T) {
	path := filepath.Join(t.TempDir(), "gateway.json")
	if err := WriteServiceFile(path, ServiceFile{Address: "127.0.0.1:7477", Token: "t", PID: 1}); err != nil {
		t.Fatalf("WriteServiceFile: %v", err)
	}
	if err := RemoveServiceFile(path); err != nil {
		t.Fatalf("RemoveServiceFile: %v", err)
	}
	got, err := ReadServiceFile(path)
	if err != nil {
		t.Fatalf("ReadServiceFile after removal: %v", err)
	}
	if !got.IsZero() {
		t.Fatalf("the file is still there after removal: %+v", got)
	}
}

// A removal that fails for a reason OTHER than absence is reported rather than swallowed. The
// provocation is a non-empty directory, which os.Remove refuses with ENOTEMPTY: unlike a
// permission error, that is deterministic whether or not the suite runs as root.
func TestARemovalThatFailsIsReported(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "gateway.json")
	if err := os.MkdirAll(filepath.Join(path, "child"), 0o700); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	if err := RemoveServiceFile(path); err == nil {
		t.Fatal("a removal that fails must be reported, not swallowed")
	}
}

// A pathless call is refused rather than silently writing to the working directory: the service
// file is the address of a gateway, and an address written somewhere nobody looks is the same as
// no address at all.
func TestWritingWithNoPathIsRefused(t *testing.T) {
	if err := WriteServiceFile("  ", ServiceFile{Address: "127.0.0.1:7477"}); err == nil {
		t.Fatal("writing a service file with no path must be refused")
	}
}

// An unset path reads as "no gateway" instead of failing: a caller running without the setting is
// in the normal state of having no service, not in an error state.
func TestReadingWithNoPathIsNoGateway(t *testing.T) {
	got, err := ReadServiceFile("")
	if err != nil {
		t.Fatalf("a pathless read must not fail: %v", err)
	}
	if !got.IsZero() {
		t.Fatalf("a pathless read must be empty, got %+v", got)
	}
}

// And removing one that was never named is likewise not a failure.
func TestRemovingWithNoPathIsNotAFailure(t *testing.T) {
	if err := RemoveServiceFile(""); err != nil {
		t.Fatalf("a pathless removal must not fail: %v", err)
	}
}

// A directory that cannot be created is reported with the path in it: the operator has to be able
// to see WHICH path, because the usual cause is a configuration pointing somewhere it cannot go.
func TestAnUncreatableDirectoryIsReported(t *testing.T) {
	restore := mkdirAllServiceFile
	mkdirAllServiceFile = func(string, os.FileMode) error { return errors.New("read-only filesystem") }
	defer func() { mkdirAllServiceFile = restore }()

	err := WriteServiceFile(filepath.Join(t.TempDir(), "sub", "gateway.json"), ServiceFile{Address: "a"})
	if err == nil {
		t.Fatal("a directory that cannot be created must be reported")
	}
	if !strings.Contains(err.Error(), "gateway.json") {
		t.Fatalf("the error must name the path, got: %v", err)
	}
}

// A file that cannot be written is reported: swallowing it would leave the reader looking for a
// gateway that was never described.
func TestAnUnwritableServiceFileIsReported(t *testing.T) {
	restore := writeServiceFileTo
	writeServiceFileTo = func(string, []byte, os.FileMode) error { return errors.New("no space left") }
	defer func() { writeServiceFileTo = restore }()

	if err := WriteServiceFile(filepath.Join(t.TempDir(), "gateway.json"), ServiceFile{Address: "a"}); err == nil {
		t.Fatal("a file that cannot be written must be reported")
	}
}

// A mode that cannot be set is reported rather than ignored. It is the one failure in this file
// that is a SECURITY failure: the file holds the token, and the caller is entitled to know that
// the 0600 it asked for did not happen.
func TestAModeThatCannotBeSetIsReported(t *testing.T) {
	restore := chmodServiceFile
	chmodServiceFile = func(string, os.FileMode) error { return errors.New("operation not permitted") }
	defer func() { chmodServiceFile = restore }()

	if err := WriteServiceFile(filepath.Join(t.TempDir(), "gateway.json"), ServiceFile{Address: "a"}); err == nil {
		t.Fatal("a mode that cannot be set must be reported, the file holds the token")
	}
}

// The temporary lives beside the target, not in the system temp directory, because rename is only
// atomic within one filesystem - and an atomic rename is the whole reason a reader cannot catch a
// half-written document.
func TestTheTemporaryIsBesideTheTarget(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "sub", "gateway.json")
	if err := WriteServiceFile(path, ServiceFile{Address: "127.0.0.1:7477", Token: "t", PID: 1}); err != nil {
		t.Fatalf("WriteServiceFile: %v", err)
	}
	entries, err := os.ReadDir(filepath.Dir(path))
	if err != nil {
		t.Fatalf("ReadDir: %v", err)
	}
	for _, e := range entries {
		if e.Name() != "gateway.json" {
			t.Fatalf("writing left %q behind beside the service file", e.Name())
		}
	}
}
