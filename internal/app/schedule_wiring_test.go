package app

import (
	"context"
	"errors"
	"io"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/madkoding/motita/internal/config"
	"github.com/madkoding/motita/internal/gateway"
	"github.com/madkoding/motita/internal/logx"
)

// The schedule directory lives under the motita home like the sessions and the projects:
// everything the program owns is under one folder the user can back up or delete as a
// unit. A gateway wired with an absolute path outside that home is a gateway that scatters
// state, which is the thing the home exists to prevent.
func TestTheScheduleDirectoryLivesUnderTheHome(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	got := scheduleDir()
	want := filepath.Join(home, ".motita", "schedules")
	if got != want {
		t.Fatalf("scheduleDir() = %q, want %q", got, want)
	}
}

// With no HOME there is no sensible home, and the empty result tells the caller to keep the
// in-memory behaviour rather than guess a location. Same rule sessionDir and projectDir
// follow, and the end-to-end container runs with HOME=/.
func TestTheScheduleDirectoryIsEmptyWithoutAHome(t *testing.T) {
	t.Setenv("HOME", "")
	if got := scheduleDir(); got != "" {
		t.Fatalf("scheduleDir() = %q with no HOME, want empty", got)
	}
}

// The gateway is started with the scheduling settings the configuration carries: the tick
// and the minimum cadence are decided ONCE, in the configuration, and a second default in
// the wiring is how the two drift.
func TestTheGatewayGetsTheScheduleSettingsFromTheConfiguration(t *testing.T) {
	inTempDir(t, func() {
		silence(t)
		cfg := config.Default()
		cfg.Schedule.Tick = 7 * time.Second
		cfg.Schedule.MinEvery = 3 * time.Minute
		cfg.Agent.LogFile = ""
		cfg.Gateway.Listen = "127.0.0.1:0"
		cfg.Gateway.TokenFile = filepath.Join(t.TempDir(), "gateway.token")

		var got *gateway.Server
		op := Options{
			Out: io.Discard, Err: io.Discard, Goos: "linux", Goarch: "amd64",
			BaseCtx: context.Background(),
			NewGateway: func(opts gateway.Options, _ config.Config) (*gateway.Server, error) {
				if opts.ScheduleTick != 7*time.Second {
					t.Errorf("ScheduleTick = %s, want the configured 7s", opts.ScheduleTick)
				}
				if opts.ScheduleMinEvery != 3*time.Minute {
					t.Errorf("ScheduleMinEvery = %s, want the configured 3m", opts.ScheduleMinEvery)
				}
				if !strings.HasSuffix(opts.ScheduleDir, filepath.Join(".motita", "schedules")) {
					t.Errorf("ScheduleDir = %q, want it under the motita home", opts.ScheduleDir)
				}
				return nil, errors.New("stop here: the wiring is what is under test")
			},
		}
		_ = got
		srv, err := op.startGateway(flags{}, cfg, nil, nil, logx.Global(), false)
		if srv != nil || err == nil {
			t.Fatalf("startGateway = (%v, %v), want a nil server and the injected error", srv, err)
		}
	})
}

// The disabled block means NOTHING is scheduled, and the directory is withheld rather than
// merely ignored: a store opened with a watcher running IS scheduling, which is exactly what
// schedule.enabled=false asks not to happen.
func TestTheGatewayIsWiredWithoutAScheduleDirectoryWhenSchedulingIsOff(t *testing.T) {
	inTempDir(t, func() {
		silence(t)
		home := t.TempDir()
		t.Setenv("HOME", home)
		cfg := config.Default()
		cfg.Schedule.Enabled = false
		cfg.Agent.LogFile = ""
		cfg.Gateway.Listen = "127.0.0.1:0"
		cfg.Gateway.TokenFile = filepath.Join(t.TempDir(), "gateway.token")

		// Non-vacuity: with a HOME there IS a directory to withhold, so an empty one below is
		// the disabled block doing it and not a missing home.
		if scheduleDir() == "" {
			t.Fatalf("scheduleDir() is empty with HOME=%q: this test would pass for the wrong reason", home)
		}

		op := Options{
			Out: io.Discard, Err: io.Discard, Goos: "linux", Goarch: "amd64",
			BaseCtx: context.Background(),
			NewGateway: func(opts gateway.Options, _ config.Config) (*gateway.Server, error) {
				if opts.ScheduleDir != "" {
					t.Errorf("ScheduleDir = %q with schedule.enabled=false, want empty: a store opened with a watcher running IS scheduling", opts.ScheduleDir)
				}
				return nil, errors.New("stop here: the wiring is what is under test")
			},
		}
		srv, err := op.startGateway(flags{}, cfg, nil, nil, logx.Global(), false)
		if srv != nil || err == nil {
			t.Fatalf("startGateway = (%v, %v), want a nil server and the injected error", srv, err)
		}
	})
}
