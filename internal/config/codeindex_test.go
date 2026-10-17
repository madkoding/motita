package config

import (
	"strings"
	"testing"
)

func TestCodeIndexIsOnByDefault(t *testing.T) {
	c := Default()
	if !c.CodeIndex.Enabled || c.CodeIndex.MaxFileKB != 256 || c.CodeIndex.MaxFiles != 5000 {
		t.Fatalf("default code_index = %+v", c.CodeIndex)
	}
	if err := c.ValidateWithoutKey(); err != nil {
		t.Errorf("the default must validate: %v", err)
	}
}

func TestValidateCodeIndex(t *testing.T) {
	cases := []struct {
		name   string
		mutate func(*CodeIndex)
		want   string
	}{
		{"zero means default", func(x *CodeIndex) { x.MaxFileKB, x.MaxFiles = 0, 0 }, ""},
		{"negative size", func(x *CodeIndex) { x.MaxFileKB = -1 }, "code_index.max_file_kb"},
		{"negative files", func(x *CodeIndex) { x.MaxFiles = -1 }, "code_index.max_files"},
		{"off is not checked", func(x *CodeIndex) { x.Enabled, x.MaxFiles = false, -1 }, ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			c := Default()
			tc.mutate(&c.CodeIndex)
			err := c.ValidateWithoutKey()
			if tc.want == "" && err != nil {
				t.Errorf("unexpected error: %v", err)
			}
			if tc.want != "" && (err == nil || !strings.Contains(err.Error(), tc.want)) {
				t.Errorf("err = %v, want %q", err, tc.want)
			}
		})
	}
}
