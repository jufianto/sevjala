//go:build !windows

package svc

import "os"

const AdminHint = "run with sudo"

func IsAdmin() bool { return os.Geteuid() == 0 }
