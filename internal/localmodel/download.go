package localmodel

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"hash"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

// A RESUMABLE, VERIFIED DOWNLOAD.
//
// The file grows in <dest>.part. A download that stops (Motita closed, the network gone, the user
// pressed Cancel) keeps what it had, and the next one asks only for the rest (HTTP Range). The
// whole file is hashed as it is written and checked against the hash the caller expects before it
// is renamed into place, so a half file or a tampered one is never used: on a mismatch the part is
// deleted and the error says so.

// Progress is called as bytes arrive: done so far out of total (0 when unknown).
type Progress func(done, total int64)

// download is one file to fetch.
type download struct {
	url    string
	dest   string
	size   int64        // expected size, 0 when unknown
	sha256 string       // expected hex digest, required
	client *http.Client // follows the CDN's redirects (net/http stops at ten)
}

// errDigest is a finished file whose hash is not the expected one.
var errDigest = errors.New("the downloaded file does not match its published SHA-256: it was deleted, try again")

func (d download) run(ctx context.Context, progress Progress) error {
	if d.sha256 == "" {
		return errors.New("no SHA-256 to check the download against")
	}
	if err := os.MkdirAll(filepath.Dir(d.dest), 0o755); err != nil {
		return err
	}
	part := d.dest + ".part"
	h := sha256.New()
	have, err := rehash(part, h)
	if err != nil {
		return err
	}
	if d.size > 0 && have > d.size {
		// Longer than the file can be: something else wrote it. Start over.
		have = 0
		h.Reset()
	}
	if d.size == 0 || have < d.size {
		if have, err = d.fetch(ctx, part, have, h, progress); err != nil {
			return err
		}
	}
	if d.size > 0 && have != d.size {
		return fmt.Errorf("the download stopped at %d of %d bytes: try again to resume it", have, d.size)
	}
	if !strings.EqualFold(hex.EncodeToString(h.Sum(nil)), d.sha256) {
		_ = os.Remove(part)
		return errDigest
	}
	return os.Rename(part, d.dest)
}

// rehash feeds what an earlier attempt left in part to h, and says how much that was.
func rehash(part string, h hash.Hash) (int64, error) {
	f, err := os.Open(part)
	if errors.Is(err, os.ErrNotExist) {
		return 0, nil
	}
	if err != nil {
		return 0, err
	}
	defer func() { _ = f.Close() }()
	return io.Copy(h, f)
}

// fetch appends the rest of the file to part and answers how long the part is now.
func (d download) fetch(ctx context.Context, part string, have int64, h hash.Hash, progress Progress) (int64, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, d.url, nil)
	if err != nil {
		return have, err
	}
	if have > 0 {
		req.Header.Set("Range", "bytes="+strconv.FormatInt(have, 10)+"-")
	}
	resp, err := d.client.Do(req)
	if err != nil {
		return have, err
	}
	defer func() { _ = resp.Body.Close() }()
	flags := os.O_CREATE | os.O_WRONLY | os.O_APPEND
	switch {
	case resp.StatusCode == http.StatusPartialContent && have > 0 && rangeStart(resp) == have:
	case resp.StatusCode == http.StatusOK:
		// The server sent the whole file: what was kept is dropped.
		have = 0
		h.Reset()
		flags = os.O_CREATE | os.O_WRONLY | os.O_TRUNC
	case resp.StatusCode == http.StatusRequestedRangeNotSatisfiable && d.size > 0 && have == d.size:
		return have, nil
	default:
		return have, fmt.Errorf("download: %s answered %s", hostOf(d.url), resp.Status)
	}
	total := d.size
	if total == 0 && resp.ContentLength > 0 {
		total = have + resp.ContentLength
	}
	f, err := os.OpenFile(part, flags, 0o600)
	if err != nil {
		return have, err
	}
	n, err := io.Copy(io.MultiWriter(f, h), &counting{r: resp.Body, done: have, total: total, progress: progress})
	if closeErr := f.Close(); err == nil {
		err = closeErr
	}
	return have + n, err
}

// rangeStart is the first byte a 206 answer carries ("bytes 100-999/1000"), or -1.
func rangeStart(resp *http.Response) int64 {
	var start int64
	if _, err := fmt.Sscanf(resp.Header.Get("Content-Range"), "bytes %d-", &start); err != nil {
		return -1
	}
	return start
}

func hostOf(raw string) string {
	s := strings.TrimPrefix(strings.TrimPrefix(raw, "https://"), "http://")
	host, _, _ := strings.Cut(s, "/")
	return host
}

// counting reports progress as a body is read.
type counting struct {
	r           io.Reader
	done, total int64
	progress    Progress
}

func (c *counting) Read(p []byte) (int, error) {
	n, err := c.r.Read(p)
	c.done += int64(n)
	if c.progress != nil && n > 0 {
		c.progress(c.done, c.total)
	}
	return n, err
}
