//go:build !windows

package cmd

import "github.com/jufianto/sevjala/svc"

const exeName = "sevjala"

func ownConsole() bool { return false }

func installDir() string { return "/usr/local/bin" }

func elevate(*globals) error { return failf("%s", svc.AdminHint) }

// setPath has nothing to do: /usr/local/bin is already on the PATH.
func setPath(bool, string) error { return nil }
