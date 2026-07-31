//go:build windows

package steps

import "github.com/Neur0nz/MadLyXInstaller/go/internal/winsys"

func isElevated() bool { return winsys.IsAdmin() }
