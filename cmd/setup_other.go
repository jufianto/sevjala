//go:build !windows

package cmd

const exeName = "sevjala"

func ownConsole() bool { return false }

func installDir() string { return "/usr/local/bin" }

// setPath has nothing to do: /usr/local/bin is already on the PATH.
func setPath(bool, string) error { return nil }
