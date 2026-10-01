package gateway

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"

	"github.com/madkoding/motita/internal/updater"
)

// UpdateAvailable asks the gateway whether a newer release exists.
//
// The answer is the gateway's cached result of its hourly check, so asking is cheap and never
// reaches GitHub from the client: the same reason the web interface reads it from here. The
// signature uses plain values, not updater.CheckResult, so the text interface can declare the
// capability structurally without importing this package or the updater.
func (c *Client) UpdateAvailable(ctx context.Context) (current, latest string, available bool, err error) {
	var res updater.CheckResult
	if err := c.getJSON(ctx, "/v1/update/check", &res); err != nil {
		return "", "", false, err
	}
	// A check that failed (no network, a gateway started without an executable path) is an
	// error, not "no update": reporting it as up to date would hide that nobody looked.
	if res.Error != "" {
		return res.CurrentVersion, "", false, errors.New(res.Error)
	}
	return res.CurrentVersion, res.LatestVersion, res.UpdateAvailable, nil
}

// RunUpdate asks the gateway to download, verify and install the newest release, and reports each
// STAGE it enters through progress. It returns the version that was installed.
//
// Only stage changes are reported, not every percent: the caller is a conversation, where forty
// "downloaded N%" lines would bury the three that matter. An "error" event is returned as the
// error; a stream that ends without "done" is an error too, because a connection that dropped
// halfway is not an upgrade that finished.
func (c *Client) RunUpdate(ctx context.Context, progress func(string)) (string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+"/v1/update/run", nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("Accept", "text/event-stream")
	req.Header.Set("Authorization", "Bearer "+c.token)

	resp, err := c.http.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", refusalError(resp)
	}

	var stage, version string
	scanner := bufio.NewScanner(resp.Body)
	for scanner.Scan() {
		data, ok := strings.CutPrefix(scanner.Text(), "data: ")
		if !ok {
			continue
		}
		var evt updater.ProgressEvent
		if json.Unmarshal([]byte(data), &evt) != nil {
			continue
		}
		if evt.Version != "" {
			version = evt.Version
		}
		switch evt.Stage {
		case "error":
			return "", errors.New(evt.Message)
		case "done":
			return version, nil
		}
		if evt.Stage != stage {
			stage = evt.Stage
			progress(evt.Message)
		}
	}
	if err := scanner.Err(); err != nil {
		return "", err
	}
	return "", errors.New("the gateway closed the connection before the upgrade finished")
}
