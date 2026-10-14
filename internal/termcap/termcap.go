// Package termcap decides what an output can show: whether it interprets escape sequences at all,
// how many colours it renders, and whether its font can be trusted with anything beyond ASCII.
//
// The question used to be answered by "is TERM set and is the output a character device". That is
// right on Unix and wrong on Windows, where TERM is normally unset and a classic console window
// (powershell.exe, cmd.exe under conhost) prints escapes as text until the program asks for virtual
// terminal processing. The result was an interface with no colour at all in Windows Terminal, and a
// setup wizard printing every colour code as text in the classic console. Detect answers the whole question in one
// place, so the interface and the wizard cannot disagree about it.
package termcap

import (
	"io"
	"os"
	"runtime"
	"strings"
)

// Level is how many colours an output renders. The interface draws with the 16-colour palette, so
// any level from ANSI16 up shows it identically; the higher levels are reported for what asks.
type Level int

const (
	None      Level = iota // no colour: escapes are stripped
	ANSI16                 // the 16-colour palette (SGR 30-37, 90-97)
	ANSI256                // the 256-colour palette (SGR 38;5;n)
	TrueColor              // 24-bit colour (SGR 38;2;r;g;b)
)

// String names a level the way MOTITA_COLOR spells it.
func (l Level) String() string {
	switch l {
	case ANSI16:
		return "16"
	case ANSI256:
		return "256"
	case TrueColor:
		return "truecolor"
	default:
		return "none"
	}
}

// Profile is what an output can show.
type Profile struct {
	// Level is the colour depth. None means no colour escapes are written.
	Level Level
	// Escapes reports whether the output interprets escape sequences at all: cursor moves and
	// screen clears as well as colour. It is false for a pipe, a file, TERM=dumb, and a Windows
	// console that refused virtual terminal processing.
	Escapes bool
	// ASCII asks for plain-ASCII stand-ins for box drawing, bullets and arrows, for a console whose
	// font or code page would show them as boxes or question marks.
	ASCII bool
}

// Color reports whether colour escapes may be written.
func (p Profile) Color() bool { return p.Level > None }

// The platform seams. They are variables so the tests can play every platform from one machine:
// the Windows answer is the one this package exists for, and the CI runs on Linux.
var (
	goos       = runtime.GOOS
	enableVTFn = enableVT
)

// Detect works out what out can show, from the environment lookup (os.LookupEnv in production).
//
// The order, most specific first:
//
//	MOTITA_COLOR  never|auto|always|16|256|truecolor - motita's own switch, beats everything
//	NO_COLOR      any non-empty value turns colour off (https://no-color.org)
//	FORCE_COLOR   0 turns it off, 1/2/3 force 16/256/truecolor even into a pipe
//	auto          a terminal that interprets escapes, at the depth COLORTERM/TERM/WT_SESSION say
//
// MOTITA_ASCII=1 forces the ASCII glyphs and MOTITA_ASCII=0 forbids them; left unset, they are
// used only for a Windows console that cannot do virtual terminal processing, which is the legacy
// console whose raster font has no box drawing.
//
// On Windows, Detect also switches the console into virtual terminal processing. It has to happen
// before anything is drawn, and this is the one place that knows the output is a console.
func Detect(lookup func(string) (string, bool), out io.Writer) Profile {
	term := IsTerminal(out)
	escapes := term
	if term && goos == "windows" {
		escapes = enableVTFn(unwrapFile(out))
	}
	termName := strings.ToLower(strings.TrimSpace(env(lookup, "TERM")))
	if termName == "dumb" {
		escapes = false
	}

	p := Profile{Escapes: escapes, ASCII: goos == "windows" && term && !escapes}
	p.Level = override(lookup, autoLevel(lookup, termName, escapes))
	if v, ok := lookup("MOTITA_ASCII"); ok {
		if on, known := parseBool(v); known {
			p.ASCII = on
		}
	}
	return p
}

// autoLevel is the depth a terminal that interprets escapes is assumed to have.
func autoLevel(lookup func(string) (string, bool), termName string, escapes bool) Level {
	if !escapes {
		return None
	}
	switch strings.ToLower(env(lookup, "COLORTERM")) {
	case "truecolor", "24bit":
		return TrueColor
	}
	if env(lookup, "WT_SESSION") != "" {
		// Windows Terminal sets this, and renders 24-bit colour.
		return TrueColor
	}
	switch env(lookup, "TERM_PROGRAM") {
	case "iTerm.app", "WezTerm", "vscode", "ghostty":
		return TrueColor
	}
	switch {
	case strings.Contains(termName, "truecolor"), strings.Contains(termName, "24bit"),
		strings.Contains(termName, "direct"):
		return TrueColor
	case strings.Contains(termName, "256color"):
		return ANSI256
	case termName != "":
		return ANSI16
	case goos == "windows":
		// TERM is not a Windows variable. A console that accepted virtual terminal processing is
		// Windows 10 or later, whose console renders 24-bit colour.
		return TrueColor
	default:
		// A Unix terminal with no TERM is one nobody described: keep it plain.
		return None
	}
}

// override applies the variables a user sets to overrule the detection.
func override(lookup func(string) (string, bool), auto Level) Level {
	if v, ok := lookup("MOTITA_COLOR"); ok {
		switch strings.ToLower(strings.TrimSpace(v)) {
		case "never", "off", "none", "no", "false", "0":
			return None
		case "always", "on", "force", "yes", "true", "1":
			return atLeast16(auto)
		case "16", "ansi", "ansi16":
			return ANSI16
		case "256", "ansi256":
			return ANSI256
		case "truecolor", "24bit":
			return TrueColor
		}
		// "auto", empty or unknown: fall through to the conventions.
	}
	if env(lookup, "NO_COLOR") != "" {
		return None
	}
	if v, ok := lookup("FORCE_COLOR"); ok {
		switch strings.ToLower(strings.TrimSpace(v)) {
		case "0", "false":
			return None
		case "2":
			return ANSI256
		case "3":
			return TrueColor
		default:
			// "", "1", "true" and anything else: force at least the basic palette.
			return atLeast16(auto)
		}
	}
	return auto
}

func atLeast16(l Level) Level {
	if l < ANSI16 {
		return ANSI16
	}
	return l
}

// parseBool reads the usual spellings of a switch. known=false leaves the default alone.
func parseBool(v string) (on, known bool) {
	switch strings.ToLower(strings.TrimSpace(v)) {
	case "1", "true", "yes", "on":
		return true, true
	case "0", "false", "no", "off":
		return false, true
	}
	return false, false
}

func env(lookup func(string) (string, bool), key string) string {
	v, _ := lookup(key)
	return v
}

// IsTerminal reports whether a writer is a terminal: a character device, seen through any filter
// this package put in front of it.
//
// The test is "is a character device", which is the portable stand-in: the standard library
// exposes no ioctl without unsafe and this module takes none. Anything that is not an *os.File (a
// buffer, a pipe an embedder supplied) is not a terminal, and neither is a file that cannot be
// inspected.
func IsTerminal(w io.Writer) bool {
	f := unwrapFile(w)
	if f == nil {
		return false
	}
	info, err := f.Stat()
	if err != nil {
		return false
	}
	return info.Mode()&os.ModeCharDevice != 0
}

// unwrapFile is the *os.File behind w, or nil.
func unwrapFile(w io.Writer) *os.File {
	for {
		switch v := w.(type) {
		case *os.File:
			return v
		case interface{ Unwrap() io.Writer }:
			w = v.Unwrap()
		default:
			return nil
		}
	}
}
