//go:build !linux

package sandbox

import "errors"

// errNoAppArmor is what the commands answer where there is no AppArmor.
var errNoAppArmor = errors.New("AppArmor is a Linux feature")

// InstallAppArmor needs Linux.
func InstallAppArmor(string) error { return errNoAppArmor }

// RemoveAppArmor needs Linux.
func RemoveAppArmor() error { return errNoAppArmor }

// NetworkIsolationHint has nothing to suggest where the problem is not AppArmor's.
func NetworkIsolationHint() string { return "" }
