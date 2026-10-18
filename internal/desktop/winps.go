package desktop

import (
	"encoding/base64"
	"fmt"
	"strings"
	"unicode/utf16"
)

// winBackend drives Windows with Windows PowerShell, which every Windows has: user32 for the
// pointer and System.Windows.Forms for the screen and the keyboard. Each step is one script, handed
// over encoded so no text is ever parsed as PowerShell.
type winBackend struct{}

func (winBackend) label() string { return "Windows" }

// winPrelude makes the process aware of the screen's scaling, so the picture and the pointer are
// both in the screen's real pixels, and declares the user32 calls the steps use.
const winPrelude = `Add-Type -AssemblyName System.Windows.Forms,System.Drawing
Add-Type -TypeDefinition 'using System;using System.Runtime.InteropServices;public static class M{` +
	`[DllImport("user32.dll")]public static extern bool SetProcessDPIAware();` +
	`[DllImport("user32.dll")]public static extern bool SetCursorPos(int x,int y);` +
	`[DllImport("user32.dll")]public static extern void mouse_event(uint f,int x,int y,int d,IntPtr e);}'
[void][M]::SetProcessDPIAware()
`

func (winBackend) shot(file string) []cmd {
	return ps(`$b=[System.Windows.Forms.Screen]::PrimaryScreen.Bounds
$p=New-Object System.Drawing.Bitmap $b.Width,$b.Height
$g=[System.Drawing.Graphics]::FromImage($p)
$g.CopyFromScreen($b.Location,[System.Drawing.Point]::Empty,$b.Size)
$p.Save(` + psQuote(file) + `,[System.Drawing.Imaging.ImageFormat]::Png)`)
}

func (winBackend) size() []cmd { return nil }

// winButtons are mouse_event's down and up flags of each button.
var winButtons = map[string]string{"left": "0x2,0x4", "right": "0x8,0x10", "middle": "0x20,0x40"}

func (winBackend) click(x, y int, button string, double bool) []cmd {
	down, up, _ := strings.Cut(winButtons[button], ",")
	times := 1
	if double {
		times = 2
	}
	return ps(fmt.Sprintf("[void][M]::SetCursorPos(%d,%d)\n1..%d|%%{[M]::mouse_event(%s,0,0,0,[IntPtr]::Zero);"+
		"[M]::mouse_event(%s,0,0,0,[IntPtr]::Zero);Start-Sleep -Milliseconds 60}", x, y, times, down, up))
}

func (winBackend) move(x, y int) []cmd {
	return ps(fmt.Sprintf("[void][M]::SetCursorPos(%d,%d)", x, y))
}

func (winBackend) write(text string) []cmd {
	return ps("[System.Windows.Forms.SendKeys]::SendWait(" + psQuote(sendKeysText(text)) + ")")
}

// sendKeysText is text as SendKeys types it: its own symbols enclosed in braces.
func sendKeysText(text string) string {
	var b strings.Builder
	for _, r := range text {
		if strings.ContainsRune("+^%~(){}[]", r) {
			b.WriteString("{" + string(r) + "}")
			continue
		}
		b.WriteRune(r)
	}
	return b.String()
}

// sendKeysNames are SendKeys' names of the named keys.
var sendKeysNames = map[string]string{
	"enter": "{ENTER}", "tab": "{TAB}", "esc": "{ESC}", "backspace": "{BACKSPACE}", "delete": "{DELETE}",
	"space": " ", "up": "{UP}", "down": "{DOWN}", "left": "{LEFT}", "right": "{RIGHT}", "home": "{HOME}",
	"end": "{END}", "pageup": "{PGUP}", "pagedown": "{PGDN}",
}

// sendKeysModifiers are SendKeys' prefixes of the modifiers. It has none for the Windows key.
var sendKeysModifiers = map[string]string{"ctrl": "^", "alt": "%", "shift": "+"}

func (winBackend) press(k keys) ([]cmd, error) {
	if k.key == "" {
		return nil, fmt.Errorf("%s alone cannot be pressed on Windows: press it with a key", k)
	}
	var b strings.Builder
	for _, m := range k.mods {
		prefix, ok := sendKeysModifiers[m]
		if !ok {
			return nil, fmt.Errorf("the Windows key cannot be pressed from here: use ctrl+esc for the Start menu")
		}
		b.WriteString(prefix)
	}
	switch name, ok := sendKeysNames[k.key]; {
	case ok:
		b.WriteString(name)
	case strings.HasPrefix(k.key, "f") && len(k.key) > 1:
		b.WriteString("{" + strings.ToUpper(k.key) + "}")
	default:
		b.WriteString(sendKeysText(k.key))
	}
	return ps("[System.Windows.Forms.SendKeys]::SendWait(" + psQuote(b.String()) + ")"), nil
}

func (winBackend) scroll(x, y, amount int) []cmd {
	// One notch of the wheel is 120, and a turn towards the person (down) is negative.
	return ps(fmt.Sprintf("[void][M]::SetCursorPos(%d,%d)\n[M]::mouse_event(0x800,0,0,%d,[IntPtr]::Zero)",
		x, y, -120*amount))
}

func (winBackend) windows() []cmd {
	return ps(`Get-Process|Where-Object {$_.MainWindowTitle}|ForEach-Object {$_.ProcessName+': '+$_.MainWindowTitle}`)
}

func (winBackend) focus(window string) []cmd {
	return ps("if(-not (New-Object -ComObject WScript.Shell).AppActivate(" + psQuote(window) + ")){" +
		"throw 'no window has that title'}")
}

// ps is a step that runs script, after the prelude, in Windows PowerShell.
func ps(script string) []cmd {
	args := []string{"-NoProfile", "-NonInteractive", "-EncodedCommand", psEncode(winPrelude + script)}
	return []cmd{{"powershell.exe", args}}
}

// psEncode is script as -EncodedCommand takes it: UTF-16LE, in base64.
func psEncode(script string) string {
	units := utf16.Encode([]rune(script))
	b := make([]byte, 0, 2*len(units))
	for _, u := range units {
		b = append(b, byte(u), byte(u>>8))
	}
	return base64.StdEncoding.EncodeToString(b)
}

// psQuote is s as a single-quoted PowerShell string.
func psQuote(s string) string { return "'" + strings.ReplaceAll(s, "'", "''") + "'" }
