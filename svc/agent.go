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
	api, err := localapi.Start(endpoint, localapi.NewHandler(statusOnly(a.status)), log)
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

// statusOnly serves /status and answers every role route with 503 until the
// agent's role parts are running.
type statusOnly func() localapi.Status

func (f statusOnly) Status() localapi.Status                               { return f() }
func (statusOnly) Code() (localapi.Code, error)                            { return localapi.Code{}, errStarting }
func (statusOnly) Discover(context.Context) ([]localapi.Discovered, error) { return nil, errStarting }
func (statusOnly) AddAgent(context.Context, string) (localapi.Discovered, error) {
	return localapi.Discovered{}, errStarting
}
func (statusOnly) Pair(context.Context, string, string) (localapi.Agent, error) {
	return localapi.Agent{}, errStarting
}
func (statusOnly) Agents(context.Context) ([]localapi.Agent, error) { return nil, errStarting }
func (statusOnly) Ping(context.Context, string) (localapi.Ping, error) {
	return localapi.Ping{}, errStarting
}

var errStarting = localapi.Errorf(503, "agent is starting")
