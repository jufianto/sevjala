package svc

import (
	"runtime"

	"github.com/kardianos/service"
)

const Name = "sevjala"

func New(dataDir string, i service.Interface) (service.Service, error) {
	cfg := &service.Config{
		Name:        Name,
		DisplayName: "Sevjala Agent",
		Description: "Sevjala — Database Backup & Replication agent",
		Arguments:   []string{"agent", "--data-dir", dataDir},
		Option: service.KeyValue{
			"RunAtLoad":              true,
			"KeepAlive":              true,
			"Restart":                "on-failure",
			"OnFailure":              "restart",
			"OnFailureDelayDuration": "5s",
		},
	}
	// On Windows, Dependencies are service names, not systemd lines.
	if runtime.GOOS == "linux" {
		cfg.Dependencies = []string{"After=network-online.target", "Wants=network-online.target"}
	}
	return service.New(i, cfg)
}
