//go:build windows

package winenv

import (
	"unsafe"

	"golang.org/x/sys/windows"
)

// The version-resource API is pointer-based. These helpers keep the unsafe
// conversions in one small, obvious place rather than scattered through
// version_windows.go.

func unsafePtr(b []byte) unsafe.Pointer {
	if len(b) == 0 {
		return nil
	}
	return unsafe.Pointer(&b[0])
}

func unsafePtrPtr(p **windows.VS_FIXEDFILEINFO) unsafe.Pointer {
	return unsafe.Pointer(p)
}
