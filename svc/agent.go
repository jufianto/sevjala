package svc

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"os"
	"path/filepath"
	"runtime"
	"time"

	"github.com/kardianos/service"

	"github.com/jufianto/sevjala/config"
	"github.com/jufianto/sevjala/coord"
	"github.com/jufianto/sevjala/discovery"
	"github.com/jufianto/sevjala/identity"
	"github.com/jufianto/sevjala/lanapi"
	"github.com/jufianto/sevjala/localapi"
	"github.com/jufianto/sevjala/logx"
	"github.com/jufianto/sevjala/pairing"
	"github.com/jufianto/sevjala/store"
)

type Agent struct {
	DataDir    string
	Config     config.Config
	Version    string
	Foreground bool

	log     *slog.Logger
	logFile io.Closer
	started time.Time
	id      *identity.Identity
	store   *store.Store
	api     *localapi.Server
	lan     *lanapi.Server
	disco   *discovery.Responder
}

// Start must not block: kardianos/service calls it on the service thread.
func (a *Agent) Start(service.Service) error {
	log, closer, err := logx.Open(config.LogDir(a.DataDir), a.Foreground)
	if err != nil {
		return err
	}
	a.log, a.logFile, a.started = log, closer, time.Now()
	if err := a.start(); err != nil {
		log.Error("agent failed to start", "err", err)
		_ = a.shutdown()
		closer.Close()
		a.logFile = nil
		return err
	}
	log.Info("agent started", logx.Event(logx.EventAgentStarted), "role", a.Config.Role, "version", a.Version,
		"agent_id", a.id.AgentID, "lan", a.lan.Addr(), "discovery", a.disco.Addr().String(), "data_dir", a.DataDir)
	return nil
}

func (a *Agent) start() error {
	id, created, err := identity.LoadOrCreate(a.DataDir)
	if err != nil {
		return err
	}
	a.id = id
	if created {
		a.log.Info("identity created", logx.Event(logx.EventIdentityCreated), "agent_id", id.AgentID, "fp", id.FP)
	}
	hostname, _ := os.Hostname()
	backend := &coord.Service{StatusFunc: a.status}
	var codes *pairing.Codes
	switch a.Config.Role {
	case config.RoleNode:
		codes = pairing.New(time.Now)
		backend.Node = &coord.Node{AgentID: id.AgentID, FP: id.FP, Codes: codes, Log: a.log}
	case config.RoleController:
		if a.store, err = store.Open(filepath.Join(a.DataDir, store.FileName)); err != nil {
			return err
		}
		port := a.Config.DiscoveryPort
		backend.Controller = &coord.Controller{Self: id, Store: a.store, LAN: lanapi.NewClient(id),
			Targets: func() ([]*net.UDPAddr, error) { return discovery.BroadcastTargets(port) },
			Wait:    discovery.DefaultWait, Log: a.log}
	}
	if a.api, err = localapi.Start(localapi.Endpoint(a.DataDir), localapi.NewHandler(backend), a.log); err != nil {
		return err
	}
	if a.lan, err = lanapi.Start(lanapi.Options{Addr: a.Config.Listen, DataDir: a.DataDir, Identity: id,
		Role: a.Config.Role, Version: a.Version, Hostname: hostname, Started: a.started, Codes: codes, Log: a.log}); err != nil {
		return err
	}
	info := discovery.Reply{AgentID: id.AgentID, Hostname: hostname, Port: a.lan.Port(), Role: a.Config.Role,
		Version: a.Version, OS: runtime.GOOS, FP: id.FP}
	a.disco, err = discovery.Listen(fmt.Sprintf("0.0.0.0:%d", a.Config.DiscoveryPort), func() discovery.Reply { return info }, a.log)
	return err
}

// shutdown stops whatever start got to, in reverse order.
func (a *Agent) shutdown() error {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	var errs []error
	if a.disco != nil {
		errs = append(errs, a.disco.Close())
		a.disco = nil
	}
	if a.lan != nil {
		errs = append(errs, a.lan.Shutdown(ctx))
		a.lan = nil
	}
	if a.api != nil {
		errs = append(errs, a.api.Shutdown(ctx))
		a.api = nil
	}
	if a.store != nil {
		errs = append(errs, a.store.Close())
		a.store = nil
	}
	return errors.Join(errs...)
}

func (a *Agent) Stop(service.Service) error {
	if a.logFile == nil {
		return nil
	}
	err := a.shutdown()
	a.log.Info("agent stopped", logx.Event(logx.EventAgentStopped))
	err = errors.Join(err, a.logFile.Close())
	a.logFile = nil
	return err
}

func (a *Agent) status() localapi.Status {
	return localapi.Status{
		Version: a.Version,
		Role:    a.Config.Role,
		PID:     os.Getpid(),
		UptimeS: int64(time.Since(a.started).Seconds()),
		DataDir: a.DataDir,
		AgentID: a.id.AgentID,
		FP:      a.id.FP,
	}
}
