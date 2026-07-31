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
	"golang.org/x/sys/windows/registry"
)

const toggleKey = `Keyboard Layout\Toggle`

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

// LanguageToggleDisabled reports whether Alt+Shift still switches input
// language. The MadLyX guide's standing instruction is that Windows must stay
// on English, because LyX supplies the Hebrew itself through its keyboard map
// and every shortcut breaks when Windows flips.
func LanguageToggleDisabled() (bool, error) {
	k, err := registry.OpenKey(registry.CURRENT_USER, toggleKey, registry.QUERY_VALUE)
	if err != nil {
		return false, nil // key absent means the Windows default, Alt+Shift
	}
	defer k.Close()
	v, _, err := k.GetStringValue("Language Hotkey")
	if err != nil {
		return false, nil
	}
	return v == "3", nil
}

// DisableLanguageToggle stops Alt+Shift switching input language.
//
// The values are REG_SZ, not DWORD - writing DWORDs here is the common mistake
// and Windows silently ignores them. "3" means not assigned. Per-user, no admin
// needed, and reversible from Windows Settings. Win+Space still works, so
// Hebrew stays reachable deliberately rather than by accident.
func DisableLanguageToggle() error {
	k, _, err := registry.CreateKey(registry.CURRENT_USER, toggleKey, registry.SET_VALUE)
	if err != nil {
		return err
	}
	defer k.Close()
	for _, name := range []string{"Language Hotkey", "Layout Hotkey", "Hotkey"} {
		if err := k.SetStringValue(name, "3"); err != nil {
			return fmt.Errorf("setting %s: %w", name, err)
		}
	}
	return nil
}

// RestoreLanguageToggle puts the Windows defaults back.
func RestoreLanguageToggle() error {
	k, _, err := registry.CreateKey(registry.CURRENT_USER, toggleKey, registry.SET_VALUE)
	if err != nil {
		return err
	}
	defer k.Close()
	defaults := map[string]string{"Language Hotkey": "1", "Layout Hotkey": "2", "Hotkey": "1"}
	for name, v := range defaults {
		if err := k.SetStringValue(name, v); err != nil {
			return err
		}
	}
	return nil
}

// HebrewInputInstalled reports whether a Hebrew keyboard layout is present,
// so the Alt+Shift question can say whether it is precautionary.
func HebrewInputInstalled() bool {
	k, err := registry.OpenKey(registry.CURRENT_USER, `Keyboard Layout\Preload`, registry.QUERY_VALUE)
	if err != nil {
		return false
	}
	defer k.Close()
	names, err := k.ReadValueNames(0)
	if err != nil {
		return false
	}
	for _, n := range names {
		if v, _, err := k.GetStringValue(n); err == nil && strings.HasSuffix(strings.ToLower(v), "40d") {
			return true // 040D is Hebrew
		}
	}
	return false
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
