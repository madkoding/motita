package devbox

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"
)

// THE INTERNAL MODE: no port on the host.
//
// The service is on a network the runtime made with --internal (no route out, no published
// ports). motita cannot open a socket there, so it runs two kinds of throwaway container on the
// same network: a curl to read a page, and a headless browser to photograph one. Both are started
// with the same limits as the box, and removed as soon as they finish.

// Helper images. They are public images, overridable for a machine that mirrors its own.
const (
	curlImage    = "docker.io/curlimages/curl:latest"
	browserImage = "docker.io/zenika/alpine-chrome:latest"
	pullTimeout  = 10 * time.Minute
)

func helperImage(env, def string) string {
	if v := getenv(env); v != "" {
		return v
	}
	return def
}

// ensureImage pulls a helper image once, before the first use: a pull inside a readiness poll
// would eat the poll's whole budget.
func (m *Manager) ensureImage(ctx context.Context, img string) error {
	m.mu.Lock()
	known := m.have[img]
	m.mu.Unlock()
	if known {
		return nil
	}
	c, cancel := context.WithTimeout(ctx, pullTimeout)
	defer cancel()
	if _, err := runCmd(c, m.rt.Bin, "image", "inspect", img); err != nil {
		if out, err := runCmd(c, m.rt.Bin, "pull", img); err != nil {
			return fmt.Errorf("the helper image %s could not be pulled: %v\n%s", img, err, tail(string(out), 500))
		}
	}
	m.mu.Lock()
	m.have[img] = true
	m.mu.Unlock()
	return nil
}

// helperArgs are the flags a helper container starts with: the box's network and its limits.
func (m *Manager) helperArgs(b *Box, mem string) []string {
	return []string{"run", "--network", b.network, "--security-opt", "no-new-privileges",
		"--pids-limit", "256", "--memory", mem, "--label", Label + "=1", "--label", OwnerLabel + "=" + m.owner}
}

// helperCurl runs curl against path on the box's network and returns what it printed. flags are
// curl's; the address is always the box's own.
func (m *Manager) helperCurl(ctx context.Context, b *Box, path string, flags ...string) (string, error) {
	img := helperImage("MOTITA_BOX_CURL_IMAGE", curlImage)
	if err := m.ensureImage(ctx, img); err != nil {
		return "", err
	}
	args := append(m.helperArgs(b, "128m"), "--rm", img)
	args = append(args, flags...)
	out, err := runCmd(ctx, m.rt.Bin, append(args, b.URL+path)...)
	return string(out), err
}

// shotInside photographs url with a browser container on the box's network and copies the picture
// out of it.
func (m *Manager) shotInside(ctx context.Context, b *Box, url, out string, width, height int) error {
	img := helperImage("MOTITA_BOX_BROWSER_IMAGE", browserImage)
	if err := m.ensureImage(ctx, img); err != nil {
		return err
	}
	name := b.Name + "-shot"
	ctx, cancel := context.WithTimeout(ctx, shotTimeout)
	defer cancel()
	defer func() {
		c, stop := context.WithTimeout(context.Background(), 30*time.Second)
		defer stop()
		_, _ = runCmd(c, m.rt.Bin, "rm", "-f", name)
	}()
	args := append(m.helperArgs(b, "1g"), "--name", name, img, "--no-sandbox", "--disable-gpu",
		"--hide-scrollbars", "--virtual-time-budget=8000", "--screenshot=/tmp/shot.png",
		fmt.Sprintf("--window-size=%d,%d", width, height), url)
	if o, err := runCmd(ctx, m.rt.Bin, args...); err != nil {
		return fmt.Errorf("the browser container failed: %v\n%s", err, tail(string(o), 800))
	}
	if o, err := runCmd(ctx, m.rt.Bin, "cp", name+":/tmp/shot.png", out); err != nil {
		return fmt.Errorf("the picture could not be copied out: %v\n%s", err, tail(string(o), 500))
	}
	if st, err := os.Stat(out); err != nil || st.Size() == 0 {
		return errors.New("the browser ran but wrote no picture")
	}
	return nil
}

// composeFile finds the compose file of a folder, by the names compose itself looks for.
func composeFile(dir string) (string, bool) {
	for _, n := range []string{"compose.yaml", "compose.yml", "docker-compose.yaml", "docker-compose.yml"} {
		if st, err := os.Stat(filepath.Join(dir, n)); err == nil && !st.IsDir() {
			return filepath.Join(dir, n), true
		}
	}
	return "", false
}
