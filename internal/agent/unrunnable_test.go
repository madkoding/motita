package agent

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// stepActions is an execute-phase reply with actions of any kind.
func stepActions(done bool, actions ...map[string]string) string {
	return mustJSON(map[string]any{
		"reasoning": "scripted", "actions": actions,
		"final_action": map[string]string{"command": ""}, "done": done,
	})
}

func shellAction(command string) map[string]string {
	return map[string]string{"kind": "command", "description": "step", "command": command}
}

// TestMissingDependenciesAndNoNetworkEndAsNotValidated is the regression of the Laravel session:
// vendor/bin/phpunit is missing, composer cannot reach the network (curl error 6), and the cheap
// way out is a phpunit that exits 0 behind a composer script. None of that may happen: the files
// stay as they were, and the run ends reporting that nothing was validated.
func TestMissingDependenciesAndNoNetworkEndAsNotValidated(t *testing.T) {
	const manifest = `{"scripts":{"test":"php artisan test"}}`
	s := &scriptServer{
		synthReply: `{"status":"done","summary":"all tests pass","verification":` +
			`[{"check":"vendor/bin/phpunit","result":"pass","evidence":"exit 0"}]}`,
		execute: func(round int, _ string) string {
			switch round {
			case 1:
				return stepActions(false, shellAction(`printf '%s' '`+manifest+`' > composer.json`),
					shellAction(`sh -c 'echo "curl error 6 while downloading: Could not resolve host: repo.packagist.org" >&2; exit 1'`),
					shellAction(`vendor/bin/phpunit`))
			case 2: // the stand-ins, one of each kind
				return stepActions(false,
					map[string]string{"kind": "write_file", "description": "stub", "command": "vendor/bin/phpunit\n<?php exit(0);"},
					map[string]string{"kind": "edit_file", "description": "script", "command": "composer.json\n<<<<<<< SEARCH\n" +
						`"test":"php artisan test"` + "\n=======\n" + `"test":"exit 0"` + "\n>>>>>>> REPLACE"},
					shellAction(`composer config scripts.test "exit 0"`))
			default:
				return stepActions(true)
			}
		},
	}
	e, result, err := runScript(t, s, "exit 0", nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, statErr := os.Stat(filepath.Join(e.dir, "vendor")); !errors.Is(statErr, os.ErrNotExist) {
		t.Error("no stand-in may exist under vendor/")
	}
	if got := readBack(t, filepath.Join(e.dir, "composer.json")); got != manifest {
		t.Errorf("the composer script must be untouched: %s", got)
	}
	if strings.Count(s.executes[2], "[refused:") < 3 {
		t.Errorf("the three stand-ins must come back refused:\n%s", s.executes[2])
	}
	if !strings.Contains(s.executes[1], "COULD NOT RUN") && !strings.Contains(s.executes[2], "COULD NOT RUN") {
		t.Errorf("the run must be told the check could not run:\n%s", s.executes[2])
	}
	if !strings.Contains(result.Reason, "NOT VALIDATED") {
		t.Errorf("the result must say it is not validated: %s", result.Reason)
	}
	rep := result.Report
	if rep == nil || rep.Status != "partial" {
		t.Fatalf("the report must be partial, not done: %+v", rep)
	}
	if len(rep.Verification) != 1 || rep.Verification[0].Result != "skipped" ||
		!strings.Contains(rep.Verification[0].Check, "vendor/bin/phpunit") {
		t.Errorf("the check that could not run must be listed as skipped, not as a pass: %+v", rep.Verification)
	}
	if !strings.Contains(s.synth[0], "CHECKS THAT COULD NOT RUN") {
		t.Errorf("the synthesis must be told what could not run:\n%s", s.synth[0])
	}
}

// TestAStandInCannotCloseAnOpenCheck: after a check could not run, a script written into
// vendor/bin through the shell is refused, so the check stays open and the result stays not
// validated.
func TestAStandInCannotCloseAnOpenCheck(t *testing.T) {
	s := &scriptServer{
		synthReply: `{"status":"done","summary":"ok","verification":[{"check":"phpunit","result":"pass","evidence":"3 tests"}]}`,
		execute: func(round int, _ string) string {
			switch round {
			case 1:
				return stepActions(false, shellAction(`vendor/bin/phpunit`))
			case 2:
				return stepActions(false, shellAction(`mkdir -p vendor/bin && printf 'echo OK' > vendor/bin/phpunit`),
					shellAction(`vendor/bin/phpunit`))
			default:
				return stepActions(true)
			}
		},
	}
	e, result, err := runScript(t, s, "exit 0", nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, statErr := os.Stat(filepath.Join(e.dir, "vendor/bin/phpunit")); !errors.Is(statErr, os.ErrNotExist) {
		t.Error("the stand-in must not exist")
	}
	if !strings.Contains(result.Reason, "NOT VALIDATED") || result.Report.Status != "partial" {
		t.Errorf("reason=%s report=%+v", result.Reason, result.Report)
	}
}

// TestMarkUnvalidatedKeepsAnHonestReportAsItIs: an already honest report is not rewritten.
func TestMarkUnvalidatedKeepsAnHonestReportAsItIs(t *testing.T) {
	blocked := []verifRecord{{command: "php artisan test", tail: "Failed opening required 'vendor/autoload.php'"}}
	rep := &Report{Status: "partial", Verification: []ReportCheck{
		{Check: "php artisan test", Result: "skipped", Evidence: "no vendor"},
		{Check: "composer test", Result: "pass"},
	}}
	rep.markUnvalidated(blocked)
	if rep.Status != "partial" || len(rep.Verification) != 2 || rep.Verification[0].Evidence != "no vendor" {
		t.Errorf("an honest report must stay: %+v", rep)
	}
	var none Report
	none.Status = "done"
	none.markUnvalidated(nil)
	if none.Status != "done" {
		t.Error("with nothing blocked the report is untouched")
	}
	lying := &Report{Status: "failed", Verification: []ReportCheck{{Check: "php artisan test", Result: "pass"}}}
	lying.markUnvalidated(blocked)
	if lying.Status != "failed" || lying.Verification[0].Result != "skipped" {
		t.Errorf("a pass over a check that could not run becomes skipped: %+v", lying)
	}
}

// TestBlockedCausesAreTold covers how the cause is read and when there is none to show.
func TestBlockedCausesAreTold(t *testing.T) {
	for tail, want := range map[string]string{
		"x\nPHP Warning: Failed opening required '/app/vendor/autoload.php'\ny": "vendor/autoload.php",
		"curl error 6 while downloading https://repo.packagist.org":             "curl error 6",
		"sh: 1: vendor/bin/phpunit: not found":                                  "phpunit: not found",
		"nothing recognisable":                                                  "nothing recognisable",
	} {
		if got := blockedCause(verifRecord{tail: tail}); !strings.Contains(got, want) {
			t.Errorf("cause of %q = %q, want %q", tail, got, want)
		}
	}
	if blockedNote(nil) != "" || unvalidatedBlock(nil) != "" {
		t.Error("nothing blocked, nothing to say")
	}
	if len(blockedOf([]verifRecord{{tail: "1 failed, 2 passed"}, {tail: "x: command not found"}})) != 1 {
		t.Error("a failing test is not a blocked check")
	}
}

// TestTheAuditSeesWhatCouldNotRun: a criterion about tests is read against the checks that never
// ran, by name, so it cannot rest on a simulated validation.
func TestTheAuditSeesWhatCouldNotRun(t *testing.T) {
	var seen string
	s := &scriptServer{
		audit: func(prompt string) string {
			seen = prompt
			return `{"criteria":[{"criterion":"it is done","met":true,"evidence":"shown"}]}`
		},
		execute: func(round int, _ string) string {
			if round == 1 {
				return stepActions(false, shellAction(`vendor/bin/phpunit`))
			}
			return stepActions(true)
		},
	}
	if _, _, err := runScript(t, s, "exit 0", nil); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(seen, "CHECKS THAT COULD NOT RUN") || !strings.Contains(seen, "vendor/bin/phpunit") {
		t.Errorf("the audit must be told which check never ran:\n%s", seen)
	}
}
