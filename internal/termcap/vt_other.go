//go:build !windows

package termcap

import "os"

// enableVT is a no-op outside Windows: a terminal there interprets escapes without being asked.
func enableVT(*os.File) bool { return true }
