//go:build windows

package winsys

import (
	"fmt"
	"os"
	"strings"
	"syscall"
	"unsafe"

	"golang.org/x/sys/windows"
)

// golang.org/x/sys/windows exposes ShellExecute but not ShellExecuteEx, and
// only the Ex form hands back a process handle. Without that handle we could
// launch the elevated copy but never learn whether it succeeded, so the
// binding is declared here.

var (
	shell32          = windows.NewLazySystemDLL("shell32.dll")
	procShellExecute = shell32.NewProc("ShellExecuteExW")
)

const (
	seeMaskNoCloseProcess = 0x00000040
	seeMaskNoAsyncFlag    = 0x00000100
)

type shellExecuteInfo struct {
	cbSize       uint32
	fMask        uint32
	hwnd         windows.Handle
	lpVerb       *uint16
	lpFile       *uint16
	lpParameters *uint16
	lpDirectory  *uint16
	nShow        int32
	hInstApp     windows.Handle
	lpIDList     uintptr
	lpClass      *uint16
	hkeyClass    windows.Handle
	dwHotKey     uint32
	hIcon        windows.Handle
	hProcess     windows.Handle
}

// Relaunch restarts this executable elevated and returns the child's exit code.
//
// Callers must confirm a console is present first: a UAC dialog with nobody
// there to accept it is worse than skipping the work.
func Relaunch(args []string) (int, error) {
	exe, err := os.Executable()
	if err != nil {
		return 1, err
	}

	verb, _ := syscall.UTF16PtrFromString("runas")
	file, _ := syscall.UTF16PtrFromString(exe)
	params, _ := syscall.UTF16PtrFromString(strings.Join(quoteAll(args), " "))
	cwd, _ := syscall.UTF16PtrFromString(mustGetwd())

	info := shellExecuteInfo{
		fMask:        seeMaskNoCloseProcess | seeMaskNoAsyncFlag,
		lpVerb:       verb,
		lpFile:       file,
		lpParameters: params,
		lpDirectory:  cwd,
		nShow:        windows.SW_NORMAL,
	}
	info.cbSize = uint32(unsafe.Sizeof(info))

	ret, _, callErr := procShellExecute.Call(uintptr(unsafe.Pointer(&info)))
	if ret == 0 {
		// The usual cause is the user declining the UAC prompt.
		return 1, fmt.Errorf("elevation declined or failed: %w", callErr)
	}
	if info.hProcess == 0 {
		return 0, nil // launched, but no handle to wait on
	}
	defer windows.CloseHandle(info.hProcess)

	if _, err := windows.WaitForSingleObject(info.hProcess, windows.INFINITE); err != nil {
		return 1, err
	}
	var code uint32
	if err := windows.GetExitCodeProcess(info.hProcess, &code); err != nil {
		return 1, err
	}
	return int(code), nil
}

func quoteAll(args []string) []string {
	out := make([]string, 0, len(args))
	for _, a := range args {
		if strings.ContainsAny(a, " \t\"") {
			out = append(out, `"`+strings.ReplaceAll(a, `"`, `\"`)+`"`)
			continue
		}
		out = append(out, a)
	}
	return out
}

func mustGetwd() string {
	wd, err := os.Getwd()
	if err != nil {
		return `C:\`
	}
	return wd
}
