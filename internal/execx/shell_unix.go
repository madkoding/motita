//go:build unix && !android

package execx

// Shell is the POSIX shell commands are run through.
const Shell = "/bin/sh"
