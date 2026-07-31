//go:build windows

// Package winsys holds the changes that reach outside LyX: the registry, the
// Defender exclusion list, and elevation. Everything here is reversible and
// every caller is expected to ask first.
package winsys

import (
	"fmt"
	"os/exec"
	"strings"

	"golang.org/x/sys/windows"
)

// IsAdmin reports whether the process holds the Administrators group.
func IsAdmin() bool {
	var sid *windows.SID
	err := windows.AllocateAndInitializeSid(
		&windows.SECURITY_NT_AUTHORITY, 2,
		windows.SECURITY_BUILTIN_DOMAIN_RID,
		windows.DOMAIN_ALIAS_RID_ADMINS,
		0, 0, 0, 0, 0, 0, &sid)
	if err != nil {
		return false
	}
	defer windows.FreeSid(sid)

	token := windows.Token(0) // the process token
	member, err := token.IsMember(sid)
	return err == nil && member
}

// DefenderExclusions returns the current exclusion paths.
//
// Defender has no stable native API for this; Add-MpPreference is a PowerShell
// cmdlet, so shelling out is the honest approach rather than a shortcut.
func DefenderExclusions() ([]string, error) {
	out, err := exec.Command("powershell", "-NoProfile", "-NonInteractive", "-Command",
		"(Get-MpPreference).ExclusionPath -join [char]10").Output()
	if err != nil {
		return nil, err
	}
	var paths []string
	for _, line := range strings.Split(string(out), "\n") {
		if s := strings.TrimSpace(line); s != "" {
			paths = append(paths, s)
		}
	}
	return paths, nil
}

// AddDefenderExclusions excludes paths from real-time scanning. Needs admin.
func AddDefenderExclusions(paths []string) error {
	if len(paths) == 0 {
		return nil
	}
	quoted := make([]string, len(paths))
	for i, p := range paths {
		quoted[i] = "'" + strings.ReplaceAll(p, "'", "''") + "'"
	}
	cmd := fmt.Sprintf("Add-MpPreference -ExclusionPath %s", strings.Join(quoted, ","))
	return exec.Command("powershell", "-NoProfile", "-NonInteractive", "-Command", cmd).Run()
}

// RemoveDefenderExclusions undoes AddDefenderExclusions.
func RemoveDefenderExclusions(paths []string) error {
	if len(paths) == 0 {
		return nil
	}
	quoted := make([]string, len(paths))
	for i, p := range paths {
		quoted[i] = "'" + strings.ReplaceAll(p, "'", "''") + "'"
	}
	cmd := fmt.Sprintf("Remove-MpPreference -ExclusionPath %s", strings.Join(quoted, ","))
	return exec.Command("powershell", "-NoProfile", "-NonInteractive", "-Command", cmd).Run()
}
