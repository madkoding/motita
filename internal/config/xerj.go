package config

import (
	"fmt"
	"strings"
)

// Xerj is the link to a local XERJ node (https://github.com/xerj-org/xerj), a search engine that
// indexes a folder and answers with the matching passage instead of the whole file.
//
// It is OFF by default. Turning it on gives the agent a search_index tool that asks the node; motita
// never starts, installs or feeds it (the person runs `xerj` and `xerj autoindex <folder>`), and
// what the node returns is text that goes into the model's context like any other tool result.
type Xerj struct {
	Enabled bool `yaml:"enabled"`
	// URL is the node's address. A key may not travel in clear text beyond this machine and its
	// network, the rule every keyed endpoint follows.
	URL string `yaml:"url"`
	// APIKey is sent as `Authorization: ApiKey`; empty for a node started with --insecure.
	APIKey string `yaml:"api_key"`
	// Index is the index or wildcard searched; `xerj autoindex` makes ax-<dataset> by default.
	Index string `yaml:"index"`
}

// Defaults of the xerj block.
const (
	defaultXerjURL   = "http://localhost:9200"
	defaultXerjIndex = "ax-*"
)

// defaultXerj is the xerj block of the default configuration.
func defaultXerj() Xerj { return Xerj{URL: defaultXerjURL, Index: defaultXerjIndex} }

// validateXerj checks the xerj block. A disabled block is not checked, like the gateway.
func (c *Config) validateXerj() error {
	c.Xerj.URL = strings.TrimSpace(c.Xerj.URL)
	c.Xerj.Index = strings.TrimSpace(c.Xerj.Index)
	if !c.Xerj.Enabled {
		return nil
	}
	if c.Xerj.URL == "" {
		return fmt.Errorf("xerj.url is empty: name the node (%s when it runs with no flags)", defaultXerjURL)
	}
	if c.Xerj.Index == "" {
		return fmt.Errorf("xerj.index is empty: name the index to search (%s covers what `xerj autoindex` makes)",
			defaultXerjIndex)
	}
	return checkKeyedEndpoint("xerj.url", c.Xerj.URL, c.Xerj.APIKey)
}
