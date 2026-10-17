//go:build !windows

package svc

import "github.com/kardianos/service"

func withUserStatus(s service.Service) service.Service { return s }
