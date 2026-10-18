package desktop

import (
	"context"
	"os/exec"
	"strings"
	"testing"
)

// On Windows itself: every script parses, and one runs, which compiles the user32 declarations.
func TestWindowsScriptsRun(t *testing.T) {
	b := winBackend{}
	k, _ := parseKeys("ctrl+shift+esc")
	press, _ := b.press(k)
	all := [][]cmd{b.shot(`C:\x.png`), b.click(1, 1, "left", true), b.move(1, 1), b.write("a+b"), press,
		b.scroll(1, 1, 1), b.windows(), b.focus("x")}
	for _, cmds := range all {
		script := decoded(t, cmds[0])
		check := exec.Command("powershell.exe", "-NoProfile", "-NonInteractive", "-Command",
			"$e=$null;[void][System.Management.Automation.Language.Parser]::ParseInput($env:MOTITA_PS,[ref]$null,[ref]$e);"+
				"if($e){$e;exit 1}")
		check.Env = append(check.Environ(), "MOTITA_PS="+script)
		if out, err := check.CombinedOutput(); err != nil {
			t.Errorf("%s does not parse: %v %s", strings.TrimPrefix(script, winPrelude), err, out)
		}
	}
	if _, err := runCmd(context.Background(), b.windows()[0]); err != nil {
		t.Fatalf("the prelude and the listing run: %v", err)
	}
}
