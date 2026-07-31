//go:build windows

package winenv

import (
	"fmt"

	"golang.org/x/sys/windows"
)

// fileVersion reads the version resource from a PE file, which is how the
// installed LyX reports itself. Falls back to "" so callers can try the
// directory name instead.
func fileVersion(path string) string {
	size, err := windows.GetFileVersionInfoSize(path, nil)
	if err != nil || size == 0 {
		return ""
	}
	buf := make([]byte, size)
	if err := windows.GetFileVersionInfo(path, 0, size, unsafePtr(buf)); err != nil {
		return ""
	}
	var fixed *windows.VS_FIXEDFILEINFO
	var fixedLen uint32
	if err := windows.VerQueryValue(unsafePtr(buf), `\`, unsafePtrPtr(&fixed), &fixedLen); err != nil {
		return ""
	}
	if fixed == nil {
		return ""
	}
	return fmt.Sprintf("%d.%d.%d",
		fixed.FileVersionMS>>16&0xffff,
		fixed.FileVersionMS&0xffff,
		fixed.FileVersionLS>>16&0xffff)
}
