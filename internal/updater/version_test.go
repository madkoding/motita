package updater

import "testing"

func TestIsNewer(t *testing.T) {
	tests := []struct {
		current, latest string
		want            bool
	}{
		{"v0.4.0", "v0.5.0", true},
		{"v0.5.0", "v0.5.0", false},
		{"v0.5.0", "v0.4.0", false},
		{"dev", "v0.5.0", true},
		{"v0.5.0", "dev", false},
		{"0.4.0", "0.5.0", true},
		{"v1.0.0", "v1.0.1", true},
		{"v1.0.0", "v2.0.0", true},
		{"", "v0.5.0", true},
		{"v0.5.0", "", false},
		// Pre-releases rank below their release and above the previous one.
		{"v0.5.0-rc1", "v0.5.0", true},
		{"v0.5.0", "v0.5.0-rc1", false},
		{"v0.5.0-rc1", "v0.5.0-rc2", true},
		{"v0.5.0-rc2", "v0.5.0-rc1", false},
		{"v0.4.9", "v0.5.0-rc1", true},
		{"v0.5.0-rc.2", "v0.5.0-rc.10", true},
		{"v0.5.0-alpha", "v0.5.0-alpha.1", true},
		{"v0.5.0-1", "v0.5.0-alpha", true},
		{"v0.5.0-alpha", "v0.5.0-1", false},
		// git describe builds are the tag plus local commits: not older than it, older than the next.
		{"v0.5.0-3-gabc1234", "v0.5.0", false},
		{"v0.5.0-3-gabc1234-dirty", "v0.5.1", true},
		{"v0.5.0+build.7", "v0.5.0", false},
	}
	for _, tt := range tests {
		got := isNewer(tt.current, tt.latest)
		if got != tt.want {
			t.Errorf("isNewer(%q, %q) = %v, want %v", tt.current, tt.latest, got, tt.want)
		}
	}
}

func TestNormalizeVersion(t *testing.T) {
	tests := []struct{ in, want string }{
		{"v0.5.0", "0.5.0"},
		{"0.5.0", "0.5.0"},
		{"v0.5.0-rc1", "0.5.0-rc1"},
		{"v0.5.0-rc1-2-g1a2b3c4", "0.5.0-rc1"},
		{"v0.5.0-dirty", "0.5.0"},
		{"v0.5.0+meta", "0.5.0"},
		{" dev ", "dev"},
		{"", ""},
	}
	for _, tt := range tests {
		got := normalizeVersion(tt.in)
		if got != tt.want {
			t.Errorf("normalizeVersion(%q) = %q, want %q", tt.in, got, tt.want)
		}
	}
}

func TestCompareSemver(t *testing.T) {
	tests := []struct {
		a, b string
		want int
	}{
		{"0.5.0", "0.5.0", 0},
		{"0.5.0", "0.4.0", 1},
		{"0.4.0", "0.5.0", -1},
		{"1.0.0", "0.9.9", 1},
		{"1.2.3", "1.2.4", -1},
	}
	for _, tt := range tests {
		got := compareSemver(tt.a, tt.b)
		wrongSign := (tt.want > 0 && got <= 0) || (tt.want < 0 && got >= 0)
		if wrongSign || (tt.want == 0 && got != 0) {
			t.Errorf("compareSemver(%q, %q) = %d, want sign %d", tt.a, tt.b, got, tt.want)
		}
	}
}

func TestCompareIdentifier(t *testing.T) {
	tests := []struct {
		a, b string
		want int
	}{
		{"2", "10", -1},
		{"010", "9", 1},
		{"007", "7", 0},
		{"1", "rc", -1},
		{"rc", "1", 1},
		{"beta", "alpha", 1},
	}
	for _, tt := range tests {
		got := compareIdentifier(tt.a, tt.b)
		if (got > 0) != (tt.want > 0) || (got < 0) != (tt.want < 0) {
			t.Errorf("compareIdentifier(%q, %q) = %d, want sign %d", tt.a, tt.b, got, tt.want)
		}
	}
	if isNumeric("") {
		t.Error(`isNumeric("") = true`)
	}
}
