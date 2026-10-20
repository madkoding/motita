package config

import (
	"errors"
	"fmt"
	"os"
	"strconv"
)

// Autopilot is the upkeep Motita does on its own, in two independent switches. Both are OFF by
// default: the first spends model turns every week and the second publishes under the person's
// account, and neither is something an upgrade should start without being asked.
//
// WeeklyAudit audits each project once a week, read-only, and reports the findings that are new;
// it touches no code. AutoPR lets Motita open a DRAFT pull request on a project's own repository
// for a small, safe finding (a text, a missing test, a Sonar warning). It never pushes to the main
// branch, never merges, and keeps at most MaxOpenPRs of its own pull requests open at once.
type Autopilot struct {
	WeeklyAudit bool `yaml:"weekly_audit"`
	AutoPR      bool `yaml:"auto_pr"`
	MaxOpenPRs  int  `yaml:"max_open_prs"`
}

// Bounds of Autopilot.MaxOpenPRs. The ceiling keeps "a few small pull requests" from turning into
// a queue nobody reads.
const (
	defaultAutopilotMaxOpenPRs = 3
	maxAutopilotOpenPRs        = 10
)

// defaultAutopilot is the autopilot block of the default configuration.
func defaultAutopilot() Autopilot {
	return Autopilot{MaxOpenPRs: defaultAutopilotMaxOpenPRs}
}

// validateAutopilot checks the autopilot block.
func (c *Config) validateAutopilot() error {
	if n := c.Autopilot.MaxOpenPRs; n < 1 || n > maxAutopilotOpenPRs {
		return fmt.Errorf("autopilot.max_open_prs is %d: use a number from 1 to %d", n, maxAutopilotOpenPRs)
	}
	return nil
}

// AutopilotChange is what the settings page changes of the autopilot block: each field that is not
// nil is written, the others stay as the file has them.
type AutopilotChange struct {
	WeeklyAudit *bool
	AutoPR      *bool
	MaxOpenPRs  *int
}

// pairs are the YAML lines of the change, in a fixed order.
func (c AutopilotChange) pairs() [][2]string {
	var kv [][2]string
	if c.WeeklyAudit != nil {
		kv = append(kv, [2]string{"weekly_audit", strconv.FormatBool(*c.WeeklyAudit)})
	}
	if c.AutoPR != nil {
		kv = append(kv, [2]string{"auto_pr", strconv.FormatBool(*c.AutoPR)})
	}
	if c.MaxOpenPRs != nil {
		kv = append(kv, [2]string{"max_open_prs", strconv.Itoa(*c.MaxOpenPRs)})
	}
	return kv
}

// appliedTo says the loaded block carries every field of the change.
func (c AutopilotChange) appliedTo(x Autopilot) bool {
	return same(c.WeeklyAudit, x.WeeklyAudit) && same(c.AutoPR, x.AutoPR) && same(c.MaxOpenPRs, x.MaxOpenPRs)
}

// SetAutopilot writes the change into the autopilot block, in place, inside a transaction. The file
// must still load afterwards, and the loaded configuration must carry what was written: the
// environment (MOTITA_AUTOPILOT_*) overrides the file, and a change it would hide is refused.
func (t Tx) SetAutopilot(c AutopilotChange) error {
	kv := c.pairs()
	if len(kv) == 0 {
		return nil
	}
	info, statErr := os.Stat(t.path)
	body, readErr := os.ReadFile(t.path)
	if err := errors.Join(statErr, readErr); err != nil {
		return fmt.Errorf("the configuration file cannot be read: %w", err)
	}
	text, err := setBlockValues(string(body), "autopilot", kv)
	if err != nil {
		return err
	}
	if err := writeConfigAtomic(t.path, []byte(text), info.Mode().Perm()); err != nil {
		return err
	}
	cfg, err := LoadWithoutKey(t.path)
	if err != nil {
		return err
	}
	if !c.appliedTo(cfg.Autopilot) {
		return errors.New("a MOTITA_AUTOPILOT_* variable in the gateway's environment overrides the autopilot block: " +
			"remove it from the environment, or change it there")
	}
	return nil
}
