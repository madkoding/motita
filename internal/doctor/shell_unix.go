//go:build !windows

package doctor

import "github.com/madkoding/motita/internal/execx"

func shellPath() string { return execx.Shell }
