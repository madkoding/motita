package mcp

import (
	"context"
	"strings"
)

// RESOURCES AND SKILLS SERVED BY A SERVER.
//
// A server can serve skills: documents that say HOW to use its tools well. The convention (the
// "skills over MCP" extension, still a draft) uses resources: a skill named release-notes is the
// resource skill://release-notes/SKILL.md, with its other files beside it. The protocol does not
// know what a skill is; they are resources, and the meaning is the client's.
//
// A skill of a server is instructions written by whoever runs the server. The caller must treat
// them as such: only for a server the person trusted, and never as something to execute.

// Resource is one document a server offers.
type Resource struct {
	URI         string `json:"uri"`
	Name        string `json:"name,omitempty"`
	Description string `json:"description,omitempty"`
	MIMEType    string `json:"mimeType,omitempty"`
}

// Resources lists everything the server offers, following its pages.
func (c *Client) Resources(ctx context.Context) ([]Resource, error) {
	var all []Resource
	cursor := ""
	for {
		params := map[string]any{}
		if cursor != "" {
			params["cursor"] = cursor
		}
		var page struct {
			Resources  []Resource `json:"resources"`
			NextCursor string     `json:"nextCursor"`
		}
		if err := c.Request(ctx, "resources/list", params, &page); err != nil {
			return nil, err
		}
		all = append(all, page.Resources...)
		if page.NextCursor == "" {
			return all, nil
		}
		cursor = page.NextCursor
	}
}

// ReadResource returns the text of a resource. A binary one is not carried: it has no use as
// instructions and would only spend the context.
func (c *Client) ReadResource(ctx context.Context, uri string) (string, error) {
	var res struct {
		Contents []struct {
			Text string `json:"text"`
		} `json:"contents"`
	}
	if err := c.Request(ctx, "resources/read", map[string]any{"uri": uri}, &res); err != nil {
		return "", err
	}
	var parts []string
	for _, ct := range res.Contents {
		if ct.Text != "" {
			parts = append(parts, ct.Text)
		}
	}
	return strings.Join(parts, "\n"), nil
}

// Skill is a skill a server serves.
type Skill struct {
	Name        string
	Description string
	URI         string
}

const (
	skillScheme = "skill://"
	skillFile   = "/SKILL.md"
)

// Skills picks the skills out of a server's resources: those named skill://<name>/SKILL.md.
func Skills(resources []Resource) []Skill {
	var out []Skill
	for _, r := range resources {
		if !strings.HasPrefix(r.URI, skillScheme) || !strings.HasSuffix(r.URI, skillFile) {
			continue
		}
		name := strings.TrimSuffix(strings.TrimPrefix(r.URI, skillScheme), skillFile)
		if name == "" || strings.Contains(name, "..") {
			continue
		}
		out = append(out, Skill{Name: name, Description: r.Description, URI: r.URI})
	}
	return out
}

// SkillURI is the address of a file of a skill: "name" is its SKILL.md and "name/file" another
// of its files. It says false for a path that could leave the skill.
func SkillURI(path string) (string, bool) {
	path = strings.Trim(path, "/")
	if path == "" || strings.Contains(path, "..") || strings.Contains(path, "\\") {
		return "", false
	}
	if i := strings.Index(path, "/"); i >= 0 {
		return skillScheme + path, true
	}
	return skillScheme + path + skillFile, true
}
