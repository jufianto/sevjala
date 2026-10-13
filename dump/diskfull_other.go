//go:build !windows

package dump

func osDiskFull(error) bool { return false }
