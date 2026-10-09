package skills

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// ProposedDir is where a document waits for the user before any session can read it: inside
// the shared directory, under a dot-name the index never reads. The background review saves
// there, because what it read (tool output, files, web pages) may have been written to steer it.
const ProposedDir = ".proposed"

// proposalPath is where the proposal called n is kept.
func (l *Library) proposalPath(n string) string {
	return filepath.Join(l.Root(), ProposedDir, n+".md")
}

// Proposed lists the documents waiting for the user, sorted. A directory that is not there
// holds none.
func (l *Library) Proposed() ([]string, error) {
	entries, err := os.ReadDir(filepath.Join(l.Root(), ProposedDir))
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("could not read the proposed skills: %w", err)
	}
	var out []string
	for _, e := range entries {
		if !e.Type().IsRegular() || !strings.HasSuffix(e.Name(), ".md") || strings.HasPrefix(e.Name(), ".") {
			continue
		}
		out = append(out, strings.TrimSuffix(e.Name(), ".md"))
	}
	sort.Strings(out)
	return out, nil
}

// Proposal reads one proposed document, for the user to read before deciding. A name with no
// proposal wraps ErrNotFound.
func (l *Library) Proposal(name string) (Skill, error) {
	n := Name(name)
	if n == "" {
		return Skill{}, errors.New("the skill name is empty")
	}
	p, err := l.regularProposal(n)
	if err != nil {
		return Skill{}, err
	}
	body, err := l.read(p)
	if err != nil {
		return Skill{}, fmt.Errorf("could not read the proposed skill %q: %w", n, err)
	}
	s := parse(n, p, body)
	s.Body = body
	return s, nil
}

// AcceptProposal moves a proposed document onto the shared shelf, replacing a document of the
// same name there: a proposal that patches a skill is a new version of it.
func (l *Library) AcceptProposal(name string) error {
	n := Name(name)
	if n == "" {
		return errors.New("the skill name is empty")
	}
	p, err := l.regularProposal(n)
	if err != nil {
		return err
	}
	if err := renameFile(p, filepath.Join(l.Root(), n+".md")); err != nil {
		return fmt.Errorf("could not accept the skill %q: %w", n, err)
	}
	return nil
}

// RejectProposal deletes a proposed document.
func (l *Library) RejectProposal(name string) error {
	n := Name(name)
	if n == "" {
		return errors.New("the skill name is empty")
	}
	p, err := l.regularProposal(n)
	if err != nil {
		return err
	}
	if err := os.Remove(p); err != nil {
		return fmt.Errorf("could not reject the skill %q: %w", n, err)
	}
	return nil
}

// regularProposal is the path of the proposal called n when it is a regular file: a link
// there would make accepting it install, or reading it show, whatever the link points at.
func (l *Library) regularProposal(n string) (string, error) {
	p := l.proposalPath(n)
	info, err := os.Lstat(p)
	if err != nil || !info.Mode().IsRegular() {
		return "", fmt.Errorf("%w: no proposed skill %q", ErrNotFound, n)
	}
	return p, nil
}
