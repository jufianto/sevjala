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
	"sync/atomic"
	"time"

	"github.com/kardianos/service"

	"github.com/jufianto/sevjala/config"
	"github.com/jufianto/sevjala/coord"
	"github.com/jufianto/sevjala/discovery"
	"github.com/jufianto/sevjala/dump"
	"github.com/jufianto/sevjala/identity"
	"github.com/jufianto/sevjala/inventory"
	"github.com/jufianto/sevjala/lanapi"
	"github.com/jufianto/sevjala/localapi"
	"github.com/jufianto/sevjala/logx"
	"github.com/jufianto/sevjala/pairing"
	"github.com/jufianto/sevjala/store"
	"github.com/jufianto/sevjala/transfer"
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
	lanUp   atomic.Pointer[lanapi.Server] // for status, which the local API may serve before lan starts
	host    string
	disco   *discovery.Responder
	inv     *inventory.Store
	pool    *transfer.Pool
	cancel  context.CancelFunc
	runDone chan struct{}
	ctrl    *coord.Controller
}

// The controller's unattended loops (blueprint: /status every 15 s,
// reconciliation every 5 min).
const (
	PollEvery      = 15 * time.Second
	ReconcileEvery = 5 * time.Minute
)

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
	a.host = hostname
	backend := &coord.Service{StatusFunc: a.status}
	var codes *pairing.Codes
	switch a.Config.Role {
	case config.RoleNode:
		if a.inv, err = inventory.Open(a.Config.StorePath(a.DataDir), a.Config.Node.Store.MinFreeBytes, a.log); err != nil {
			return err
		}
		codes = pairing.New(time.Now)
		backend.Node = &coord.Node{AgentID: id.AgentID, FP: id.FP, Codes: codes, Store: a.inv, Log: a.log}
	case config.RoleController:
		if a.store, err = store.Open(filepath.Join(a.DataDir, store.FileName)); err != nil {
			return err
		}
		staging := config.StagingDir(a.DataDir)
		if err := os.MkdirAll(staging, 0o700); err != nil {
			return err
		}
		removed, err := dump.CleanStaging(staging)
		if err != nil {
			return err
		}
		for _, name := range removed {
			a.log.Warn("removed unfinished dump", logx.Event(logx.EventPartRemoved), "file", name)
		}
		ctx, cancel := context.WithCancel(context.Background())
		a.cancel = cancel
		port := a.Config.DiscoveryPort
		ctrl := &coord.Controller{Self: id, Host: hostname, Store: a.store, LAN: lanapi.NewClient(id),
			Targets: func() ([]*net.UDPAddr, error) { return discovery.BroadcastTargets(port) },
			Wait:    discovery.DefaultWait, Log: a.log, DataDir: a.DataDir, Staging: staging, Config: a.Config,
			Ctx: ctx, FindTools: dump.FindTools, PollEvery: PollEvery, ReconcileEvery: ReconcileEvery}
		a.pool = transfer.NewPool(ctrl.Push, ctrl.Settle, transfer.DefaultDelays)
		ctrl.Pool = a.pool
		backend.Controller, a.ctrl = ctrl, ctrl
	}
	if a.api, err = localapi.Start(localapi.Endpoint(a.DataDir), localapi.NewHandler(backend), a.log); err != nil {
		return err
	}
	if a.lan, err = lanapi.Start(lanapi.Options{Addr: a.Config.Listen, DataDir: a.DataDir, Identity: id, Role: a.Config.Role,
		Version: a.Version, Hostname: hostname, Started: a.started, Codes: codes, Store: a.inv, Log: a.log}); err != nil {
		return err
	}
	a.lanUp.Store(a.lan)
	if ctrl := backend.Controller; ctrl != nil {
		if err := ctrl.Resume(context.Background()); err != nil {
			return err
		}
		a.runDone = make(chan struct{})
		go func() {
			defer close(a.runDone)
			ctrl.Run(ctrl.Ctx)
		}()
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
	if a.cancel != nil {
		a.cancel()
		a.cancel = nil
	}
	if a.runDone != nil {
		<-a.runDone // Run may still enqueue; the pool closes after it
		a.runDone = nil
	}
	if a.ctrl != nil {
		// A cancelled restore drops its half-made database and logs why before the store closes.
		if !a.ctrl.WaitRestore(20 * time.Second) {
			a.log.Warn("restore still running at shutdown")
		}
		a.ctrl = nil
	}
	if a.pool != nil {
		a.pool.Close()
		a.pool = nil
	}
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
	a.inv = nil
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
	st := localapi.Status{
		Version:  a.Version,
		Role:     a.Config.Role,
		PID:      os.Getpid(),
		UptimeS:  int64(time.Since(a.started).Seconds()),
		DataDir:  a.DataDir,
		AgentID:  a.id.AgentID,
		FP:       a.id.FP,
		Hostname: a.host,
	}
	if a.inv != nil {
		info := &localapi.StoreInfo{Path: a.inv.Dir(), Problems: []string{}}
		if inv, err := a.inv.Scan(); err != nil {
			info.Problems = append(info.Problems, err.Error())
		} else {
			info.Backups, info.Problems = len(inv.Backups), append(info.Problems, inv.Problems...)
		}
		var current string
		if lan := a.lanUp.Load(); lan != nil {
			if p := lan.Controller(); p != nil {
				current = p.AgentID
				st.Controller = &localapi.Peer{AgentID: p.AgentID, PairedAt: p.PairedAt}
			}
		}
		if ms, err := inventory.List(a.inv.Dir()); err == nil {
			info.Sources = inventory.Sources(ms, current)
			if st.Controller != nil && len(info.Sources) > 0 && info.Sources[0].Current {
				st.Controller.Hostname = info.Sources[0].Hostname
			}
		}
		if free, err := a.inv.FreeBytes(); err != nil {
			info.Problems = append(info.Problems, err.Error())
		} else {
			info.FreeBytes = free
		}
		st.Store = info
	}
	switch {
	case a.ctrl != nil:
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		h := a.ctrl.Health(ctx)
		st.Health = &h
	case st.Store != nil:
		var paired bool
		var contact time.Time
		if lan := a.lanUp.Load(); lan != nil {
			paired, contact = lan.Paired(), lan.ControllerContact()
		}
		h := coord.NodeHealth(st.Store, a.Config.Node.Store.MinFreeBytes, paired, a.started, contact)
		st.Health = &h
	}
	return st
}
