//go:build windows

package svc

import "golang.org/x/sys/windows"

const AdminHint = "run this from an Administrator prompt"

func IsAdmin() bool { return windows.GetCurrentProcessToken().IsElevated() }
