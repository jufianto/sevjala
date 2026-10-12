package svc

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"os"
	"time"

	"github.com/kardianos/service"

	"github.com/jufianto/sevjala/config"
	"github.com/jufianto/sevjala/localapi"
	"github.com/jufianto/sevjala/logx"
)

type Agent struct {
	DataDir    string
	Config     config.Config
	Version    string
	Foreground bool

	log     *slog.Logger
	logFile io.Closer
	api     *localapi.Server
	started time.Time
}

// Start must not block: kardianos/service calls it on the service thread.
func (a *Agent) Start(service.Service) error {
	log, closer, err := logx.Open(config.LogDir(a.DataDir), a.Foreground)
	if err != nil {
		return err
	}
	a.log, a.logFile, a.started = log, closer, time.Now()

	endpoint := localapi.Endpoint(a.DataDir)
	api, err := localapi.Start(endpoint, localapi.NewHandler(a.status), log)
	if err != nil {
		log.Error("agent failed to start", "err", err)
		closer.Close()
		return err
	}
	a.api = api
	log.Info("agent started", logx.Event(logx.EventAgentStarted),
		"role", a.Config.Role, "version", a.Version, "endpoint", endpoint, "data_dir", a.DataDir)
	return nil
}

func (a *Agent) Stop(service.Service) error {
	if a.api == nil {
		return nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	err := a.api.Shutdown(ctx)
	a.api = nil
	a.log.Info("agent stopped", logx.Event(logx.EventAgentStopped))
	return errors.Join(err, a.logFile.Close())
}

func (a *Agent) status() localapi.Status {
	return localapi.Status{
		Version: a.Version,
		Role:    a.Config.Role,
		PID:     os.Getpid(),
		UptimeS: int64(time.Since(a.started).Seconds()),
		DataDir: a.DataDir,
	}
}
