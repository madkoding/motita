package gateway

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

// ServiceFile says where a running gateway can be reached, so that a process started LATER can
// find it.
//
// It exists because the gateway can outlive the process that started it. While the gateway lived
// inside the interface, the two shared the address in memory and nothing had to be written down;
// the moment the gateway is a separate service, an address that is only in the memory of one
// process is an address no other process can reach.
//
// The token is in here because the gateway requires one on every endpoint but /v1/health. It is
// written 0600 and read from the user's home, which is the same trust boundary the token file
// itself already lives in - a second file with a weaker mode would be the weaker link, and the one
// an attacker picks.
type ServiceFile struct {
	Address string `json:"address"`
	Token   string `json:"token"`
	PID     int    `json:"pid"`
	// Owned says whether the process that WROTE this file also brought the gateway up.
	//
	// It is what decides who shuts it down, and it is stored rather than inferred because the
	// inference is wrong in the case that matters: a user starts a service with `gateway start` and
	// then opens a terminal, and "the interface started it" would be false - the interface found it.
	// Getting that wrong either kills a service the user asked to keep, or leaks one nobody asked
	// for, and neither is recoverable from inside the process.
	Owned bool `json:"owned"`
	// Reachable says whether the gateway that wrote this file was bound beyond loopback.
	//
	// Stored for the same reason Owned is: the address in this file is the CALLABLE one (loopback
	// when the socket is a wildcard), so exposure cannot be recovered from it, and a reader who
	// tried would conclude that a gateway answering the whole network is a private one.
	Reachable bool `json:"reachable"`
	// Allow is the origin rule set this gateway enforces, as its own description rather than the
	// list of strings: it is written by the server that parses those strings, so a later reader
	// gets the rules that are actually in force and not a second interpretation of them.
	//
	// In the file for the same reason Reachable is: a gateway left running across an upgrade or a
	// configuration edit goes on enforcing what it was started with, and telling its operator what
	// THIS process's configuration says would describe a gateway that is not the one answering.
	Allow string `json:"allow"`
}

// The three filesystem calls that can fail while writing the service file, as variables so their
// failure paths are TESTS rather than hypotheticals.
//
// They are injected rather than provoked with a read-only directory because that provocation is
// not reliable: root ignores the directory's mode, so the test would pass as a user and silently
// stop exercising the branch the moment the suite runs in a container as root - which is exactly
// how this repository's tests are run. The same shape is used in token.go for the same reason.
var (
	mkdirAllServiceFile = os.MkdirAll
	writeServiceFileTo  = os.WriteFile
	chmodServiceFile    = os.Chmod
)

// IsZero reports whether this describes no gateway at all.
func (s ServiceFile) IsZero() bool { return s.Address == "" && s.Token == "" && s.PID == 0 }

// ServiceFilePath is where the description of a running gateway lives.
//
// It sits beside the token file under ~/.motita, so that the whole of the program's state is
// one folder the user can find, back up or delete as a unit. It resolves HOME the same way
// config.Dir does, and for the same reasons - including the fallback: with no HOME there is no
// home to use, and a relative path is better than guessing a directory the user did not choose.
func ServiceFilePath() string {
	home := strings.TrimSpace(os.Getenv("HOME"))
	if home == "" {
		return "gateway.json"
	}
	return filepath.Join(home, ".motita", "gateway.json")
}

// WriteServiceFile writes the description of a running gateway, replacing any previous one.
//
// Replacing rather than refusing is deliberate: the file means "the gateway you can reach right
// now", and a second one would be a file that disagrees with itself. A stale entry naming a dead
// gateway is worse than no entry, because discovery trusts it.
func WriteServiceFile(path string, svc ServiceFile) error {
	if strings.TrimSpace(path) == "" {
		return errors.New("no service file path was given: a gateway that cannot say where it is cannot be found")
	}
	if err := mkdirAllServiceFile(filepath.Dir(path), 0o700); err != nil {
		return fmt.Errorf("the directory for %s could not be created: %w", path, err)
	}
	// The error is discarded on purpose: ServiceFile is a fixed struct of a string, a string, an
	// int and a bool, none of which can fail to encode. A branch that no input can reach is a
	// branch that rots, and an interface type here would buy a test for a case that cannot happen.
	data, _ := json.MarshalIndent(svc, "", "  ")
	// Written through a temporary file and renamed, so a reader never sees a half-written file.
	//
	// The window is small and real: a reader that catches the space between create and write would
	// parse a truncated document and - with the rule below - report a corrupt service file on a
	// machine where nothing is wrong. Rename is atomic on the same filesystem, which is why the
	// temporary lives beside the target and not in the system temp directory.
	tmp := path + ".tmp"
	if err := writeServiceFileTo(tmp, append(data, '\n'), 0o600); err != nil {
		return fmt.Errorf("the service file could not be written: %w", err)
	}
	// The mode is set explicitly after writing as well as at creation: os.WriteFile only applies it
	// when the file does not exist, so a temporary left behind by a crashed run - and therefore
	// already present with an older, looser mode - would be renamed into place as-is.
	if err := chmodServiceFile(tmp, 0o600); err != nil {
		return fmt.Errorf("the service file mode could not be set: %w", err)
	}
	return os.Rename(tmp, path)
}

// ReadServiceFile returns the gateway the file describes, or a zero value when there is none.
//
// A missing file is NOT an error - it is the normal state on a machine where nothing is running,
// and the most common question a caller asks. A CORRUPT file IS an error, and the distinction is
// the whole reason it is not the other way round: treating corruption as "no gateway" would send
// the caller off to start a second service while the first one is still listening.
func ReadServiceFile(path string) (ServiceFile, error) {
	var svc ServiceFile
	if strings.TrimSpace(path) == "" {
		return svc, nil
	}
	data, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return svc, nil
		}
		return svc, fmt.Errorf("the service file could not be read: %w", err)
	}
	if err := json.Unmarshal(data, &svc); err != nil {
		return ServiceFile{}, fmt.Errorf("%s does not describe a gateway: %w", path, err)
	}
	return svc, nil
}

// RemoveServiceFile deletes the description of a gateway that is no longer running.
//
// A missing file is not an error: the process that owned the gateway may have died without the
// chance to clean up, and the next start removes the stale entry anyway. Reporting an error there
// would turn a normal recovery into a failure.
func RemoveServiceFile(path string) error {
	if strings.TrimSpace(path) == "" {
		return nil
	}
	if err := os.Remove(path); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return err
	}
	return nil
}
