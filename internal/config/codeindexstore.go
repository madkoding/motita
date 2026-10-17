package config

import (
	"errors"
	"fmt"
	"net/url"
	"os"
	"strconv"
	"strings"
)

// CodeIndexChange is what the settings page changes of the code_index block: each field that is
// not nil is written, the others stay as the file has them. The embeddings key is not among them:
// it belongs in the environment, not in a file the page writes.
type CodeIndexChange struct {
	Enabled         *bool
	Semantic        *bool
	EmbeddingsAuto  *bool
	EmbeddingsURL   *string
	EmbeddingsModel *string
	MaxFileKB       *int
	MaxFiles        *int
}

// pairs are the YAML lines of the change, in a fixed order.
func (c CodeIndexChange) pairs() [][2]string {
	var kv [][2]string
	add := func(key, value string) { kv = append(kv, [2]string{key, value}) }
	for _, b := range []struct {
		key string
		v   *bool
	}{{"enabled", c.Enabled}, {"semantic", c.Semantic}, {"embeddings_auto", c.EmbeddingsAuto}} {
		if b.v != nil {
			add(b.key, strconv.FormatBool(*b.v))
		}
	}
	for _, s := range []struct {
		key string
		v   *string
	}{{"embeddings_url", c.EmbeddingsURL}, {"embeddings_model", c.EmbeddingsModel}} {
		if s.v != nil {
			add(s.key, yamlQuoted(*s.v))
		}
	}
	for _, n := range []struct {
		key string
		v   *int
	}{{"max_file_kb", c.MaxFileKB}, {"max_files", c.MaxFiles}} {
		if n.v != nil {
			add(n.key, strconv.Itoa(*n.v))
		}
	}
	return kv
}

// SetCodeIndex writes the change into the code_index block, in place, inside a transaction. The
// file must still load afterwards, and the loaded configuration must carry what was written: the
// environment (MOTITA_CODE_INDEX_*) overrides the file, and a change it would hide is refused.
func (t Tx) SetCodeIndex(c CodeIndexChange) error {
	kv := c.pairs()
	if len(kv) == 0 {
		return nil
	}
	info, statErr := os.Stat(t.path)
	body, readErr := os.ReadFile(t.path)
	if err := errors.Join(statErr, readErr); err != nil {
		return fmt.Errorf("the configuration file cannot be read: %w", err)
	}
	text, err := setBlockValues(string(body), "code_index", kv)
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
	if !c.appliedTo(cfg.CodeIndex) {
		return errors.New("a MOTITA_CODE_INDEX_* variable in the gateway's environment overrides the code_index block: " +
			"remove it from the environment, or change it there")
	}
	return nil
}

// appliedTo says the loaded block carries every field of the change.
func (c CodeIndexChange) appliedTo(x CodeIndex) bool {
	return (c.Enabled == nil || *c.Enabled == x.Enabled) &&
		(c.Semantic == nil || *c.Semantic == x.Semantic) &&
		(c.EmbeddingsAuto == nil || *c.EmbeddingsAuto == x.EmbeddingsAuto) &&
		(c.EmbeddingsURL == nil || *c.EmbeddingsURL == x.EmbeddingsURL) &&
		(c.EmbeddingsModel == nil || *c.EmbeddingsModel == x.EmbeddingsModel) &&
		(c.MaxFileKB == nil || *c.MaxFileKB == x.MaxFileKB) &&
		(c.MaxFiles == nil || *c.MaxFiles == x.MaxFiles)
}

// IsLocalURL reports whether an http(s) address names this machine or the person's own network, the
// place text can be sent without it leaving.
func IsLocalURL(raw string) bool {
	u, err := url.Parse(strings.TrimSpace(raw))
	return err == nil && u.Host != "" && isLocalHost(u.Hostname())
}
