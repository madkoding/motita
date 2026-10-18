package devbox

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
)

// COMPOSE PROJECTS.
//
// A compose file is a program a model may have written, so it is read as resolved by compose
// itself (`config --format json`, which expands variables, includes and extends) and refused if
// it asks for anything that would let a container out: a port on the host, the host's network,
// pid or ipc, privileges, devices, extra capabilities, a bind mount or a config/secret file from
// outside the project, a network other than the default. What remains is overridden before it
// runs: the default network becomes --internal and every service and the network carry the box
// label, so the sweep and the panel see them.

type composeConfig struct {
	Services map[string]composeService `json:"services"`
	Configs  map[string]composeRef     `json:"configs"`
	Secrets  map[string]composeRef     `json:"secrets"`
}

type composeRef struct {
	File string `json:"file"`
}

type composeService struct {
	Ports       []json.RawMessage `json:"ports"`
	Privileged  bool              `json:"privileged"`
	NetworkMode string            `json:"network_mode"`
	Pid         string            `json:"pid"`
	Ipc         string            `json:"ipc"`
	UsernsMode  string            `json:"userns_mode"`
	CapAdd      []string          `json:"cap_add"`
	Devices     []json.RawMessage `json:"devices"`
	SecurityOpt []string          `json:"security_opt"`
	Networks    map[string]any    `json:"networks"`
	Volumes     []struct {
		Type   string `json:"type"`
		Source string `json:"source"`
	} `json:"volumes"`
}

// inside reports whether p is dir or below it.
func inside(dir, p string) bool {
	rel, err := filepath.Rel(dir, p)
	return err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}

// serviceFault is what a service asks for that would let its container out, "" when nothing.
func serviceFault(s composeService, dir string) string {
	for _, c := range []struct {
		bad  bool
		what string
	}{
		{len(s.Ports) > 0, "publishes ports on the host (remove `ports:`; the box has a private network)"},
		{s.Privileged, "is privileged"},
		{s.NetworkMode != "", "sets network_mode"},
		{s.Pid != "" || s.Ipc != "" || s.UsernsMode != "", "shares the host's pid, ipc or user namespace"},
		{len(s.CapAdd) > 0, "adds capabilities"},
		{len(s.Devices) > 0, "maps devices"},
	} {
		if c.bad {
			return c.what
		}
	}
	for _, o := range s.SecurityOpt {
		if strings.Contains(o, "unconfined") || strings.HasPrefix(o, "label=disable") {
			return "turns a security profile off"
		}
	}
	for n := range s.Networks {
		if n != "default" {
			return "joins the network " + n + " (only the default one is allowed)"
		}
	}
	for _, v := range s.Volumes {
		if v.Type == "bind" && !inside(dir, v.Source) {
			return "mounts " + v.Source + ", outside the project"
		}
	}
	return ""
}

// checkCompose returns why a resolved compose file may not run, or nil.
func checkCompose(cfg composeConfig, dir, service string) error {
	if _, ok := cfg.Services[service]; !ok {
		names := make([]string, 0, len(cfg.Services))
		for n := range cfg.Services {
			names = append(names, n)
		}
		sort.Strings(names)
		return fmt.Errorf("the compose file has no service %q (it has: %s)", service, strings.Join(names, ", "))
	}
	for name, s := range cfg.Services {
		if bad := serviceFault(s, dir); bad != "" {
			return fmt.Errorf("service %q %s", name, bad)
		}
	}
	for _, group := range []map[string]composeRef{cfg.Configs, cfg.Secrets} {
		for name, f := range group {
			if f.File != "" && !inside(dir, f.File) {
				return fmt.Errorf("%q reads %s, outside the project", name, f.File)
			}
		}
	}
	return nil
}

// composeOverride is merged over the project's file: the private network and the labels.
func (m *Manager) composeOverride(services []string) string {
	sort.Strings(services)
	var sb strings.Builder
	sb.WriteString("services:\n")
	for _, s := range services {
		fmt.Fprintf(&sb, "  %s:\n    labels:\n      %s: \"1\"\n      %s: %q\n",
			strconv.Quote(s), Label, OwnerLabel, m.owner)
	}
	fmt.Fprintf(&sb, "networks:\n  default:\n    internal: true\n    labels:\n      %s: \"1\"\n", Label)
	return sb.String()
}

// upCompose starts a compose project and waits for its service.
func (m *Manager) upCompose(ctx context.Context, b *Box, s Spec) error {
	file, ok := composeFile(s.Compose)
	if !ok {
		return errors.New(s.Compose + " has no compose.yaml, compose.yml, docker-compose.yaml or docker-compose.yml")
	}
	base := []string{"compose", "-p", b.Name, "--project-directory", s.Compose, "-f", file}
	out, err := runCmd(ctx, m.rt.Bin, append(append([]string{}, base...), "config", "--format", "json")...)
	if err != nil {
		return fmt.Errorf("the compose file could not be read (the provider must support "+
			"`compose config --format json`): %v\n%s", err, tail(string(out), 1000))
	}
	var cfg composeConfig
	if err := json.Unmarshal(out, &cfg); err != nil {
		return fmt.Errorf("the compose file did not resolve to JSON: %v", err)
	}
	if err := checkCompose(cfg, s.Compose, s.Service); err != nil {
		return err
	}
	names := make([]string, 0, len(cfg.Services))
	for n := range cfg.Services {
		names = append(names, n)
	}
	scratch, err := tempDir("", "motita-compose-")
	if err != nil {
		return err
	}
	over := filepath.Join(scratch, "override.yaml")
	if err := os.WriteFile(over, []byte(m.composeOverride(names)), 0o600); err != nil {
		_ = os.RemoveAll(scratch)
		return err
	}
	b.scratch = scratch
	b.compose = append(base, "-f", over)
	b.network = b.Name + "_default"
	b.URL = "http://" + s.Service + ":" + strconv.Itoa(s.Port)
	bctx, cancel := context.WithTimeout(ctx, buildTimeout)
	o, err := runCmd(bctx, m.rt.Bin, append(append([]string{}, b.compose...), "up", "-d", "--build")...)
	cancel()
	if err != nil {
		m.destroy(ctx, b)
		return fmt.Errorf("the project did not start: %v\n%s", err, tail(string(o), 1500))
	}
	if err := m.waitReady(ctx, b); err != nil {
		logs, _ := runCmd(ctx, m.rt.Bin, append(append([]string{}, b.compose...), "logs", "--tail", "40", s.Service)...)
		m.destroy(ctx, b)
		return fmt.Errorf("%v\nlast log lines:\n%s", err, tail(string(logs), 1500))
	}
	return nil
}
