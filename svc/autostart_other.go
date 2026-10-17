//go:build !windows && !darwin && !linux

package svc

func InstallTrayAutostart(string) error { return nil }
func RemoveTrayAutostart() error        { return nil }
func TrayAutostartInstalled() bool      { return false }
