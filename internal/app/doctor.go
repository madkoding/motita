package app

import (
	"fmt"

	"github.com/madkoding/motita/internal/config"
	"github.com/madkoding/motita/internal/doctor"
)

// runDoctor is `motita doctor`: the machine's checks, then the configuration this run would
// read and the sandbox it would build from it. It exits with RunError when a check fails, so a
// script can tell a working machine from a broken one.
func (op Options) runDoctor(fl flags) int {
	env := doctor.System(op.Version, op.ExePath)
	if op.DoctorEnv != nil {
		env = *op.DoctorEnv
	}
	checks := append(doctor.Environment(env), op.doctorConfig(fl)...)
	if doctor.Write(op.Out, checks) {
		return RunError
	}
	return Success
}

// doctorConfig checks the configuration file and the sandbox built from it. It reads the
// same file a run would (resolvedConfigPath), and like -isolation it opens no log file, so it
// leaves nothing behind.
func (op Options) doctorConfig(fl flags) []doctor.Check {
	path := resolvedConfigPath(fl)
	if path == "" {
		return []doctor.Check{{Name: "config", Status: doctor.Warn, Detail: "no configuration file yet: run motita config"}}
	}
	cfg, err := loadPreferringKey(path, fl)
	if err != nil {
		return []doctor.Check{{Name: "config", Status: doctor.Fail, Detail: fmt.Sprintf("%s: %v", path, err)}}
	}
	checks := []doctor.Check{{Name: "config", Detail: path + ": " + DescribeConfig(cfg)}}

	cfg.Agent.LogFile = ""
	cfg.Agent.LogConsole = false
	log, err := op.newLogger(cfg.Agent)
	if err != nil {
		return append(checks, doctor.Check{Name: "sandbox", Status: doctor.Fail, Detail: err.Error()})
	}
	defer log.Close()
	opts := SandboxOptions(cfg, log)
	opts.HiddenPaths = config.SecretFiles(path)
	box, err := op.newSandbox(opts)
	if err != nil {
		return append(checks, doctor.Check{Name: "sandbox", Status: doctor.Fail, Detail: "could not prepare the sandbox: " + err.Error()})
	}
	defer box.Close()
	checks = append(checks, doctor.Check{Name: "sandbox", Detail: fmt.Sprintf("isolation %v", box.Isolation())})
	for _, s := range box.NotApplied() {
		checks = append(checks, doctor.Check{Name: "sandbox", Status: doctor.Warn, Detail: "not applied: " + s})
	}
	return checks
}
