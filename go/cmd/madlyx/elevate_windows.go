//go:build windows

package main

import "github.com/Neur0nz/MadLyXInstaller/go/internal/winsys"

func isAdmin() bool                              { return winsys.IsAdmin() }
func relaunchElevated(args []string) (int, error) { return winsys.Relaunch(args) }
