package execx

// Shell is the POSIX shell commands are run through. Android has no /bin: its shell lives
// in /system/bin, and Termux runs on top of that.
const Shell = "/system/bin/sh"
