// Package coord is the role logic behind the local API: a node issues pairing
// codes, a controller discovers, pairs and pings nodes.
package coord

import (
	"context"
	"log/slog"
	"net/http"

	"github.com/jufianto/sevjala/localapi"
	"github.com/jufianto/sevjala/logx"
	"github.com/jufianto/sevjala/pairing"
)

// Service holds exactly one of Node or Controller, by role.
type Service struct {
	StatusFunc func() localapi.Status
	Node       *Node
	Controller *Controller
}

var _ localapi.Backend = (*Service)(nil)

var (
	errNotNode       = localapi.Errorf(http.StatusConflict, "this agent is a controller; run this on a node")
	errNotController = localapi.Errorf(http.StatusConflict, "this agent is a node; run this on the controller")
)

func (s *Service) Status() localapi.Status { return s.StatusFunc() }

func (s *Service) Code() (localapi.Code, error) {
	if s.Node == nil {
		return localapi.Code{}, errNotNode
	}
	return s.Node.Code()
}

func (s *Service) Discover(ctx context.Context) ([]localapi.Discovered, error) {
	if s.Controller == nil {
		return nil, errNotController
	}
	return s.Controller.Discover(ctx)
}

func (s *Service) AddAgent(ctx context.Context, addr string) (localapi.Discovered, error) {
	if s.Controller == nil {
		return localapi.Discovered{}, errNotController
	}
	return s.Controller.AddAgent(ctx, addr)
}

func (s *Service) Pair(ctx context.Context, agent, code, fp string) (localapi.Agent, error) {
	if s.Controller == nil {
		return localapi.Agent{}, errNotController
	}
	return s.Controller.Pair(ctx, agent, code, fp)
}

func (s *Service) Agents(ctx context.Context) ([]localapi.Agent, error) {
	if s.Controller == nil {
		return nil, errNotController
	}
	return s.Controller.Agents(ctx)
}

func (s *Service) Ping(ctx context.Context, agent string) (localapi.Ping, error) {
	if s.Controller == nil {
		return localapi.Ping{}, errNotController
	}
	return s.Controller.Ping(ctx, agent)
}

type Node struct {
	AgentID string
	FP      string
	Codes   *pairing.Codes
	Log     *slog.Logger
}

func (n *Node) Code() (localapi.Code, error) {
	code, exp, err := n.Codes.Issue()
	if err != nil {
		return localapi.Code{}, err
	}
	n.Log.Info("pairing code issued", logx.Event(logx.EventPairCodeIssued), "expires_at", exp)
	return localapi.Code{Code: code, ExpiresAt: exp, AgentID: n.AgentID, FP: n.FP}, nil
}

func (s *Service) DetectDatabase(ctx context.Context) (localapi.Detection, error) {
	if s.Controller == nil {
		return localapi.Detection{}, errNotController
	}
	return s.Controller.DetectDatabase(ctx)
}

func (s *Service) SetDatabase(ctx context.Context, set localapi.DBSettings) (localapi.Detection, error) {
	if s.Controller == nil {
		return localapi.Detection{}, errNotController
	}
	return s.Controller.SetDatabase(ctx, set)
}

func (s *Service) BackupNow(ctx context.Context, db string, tags []string) ([]localapi.BackupJob, error) {
	if s.Controller == nil {
		return nil, errNotController
	}
	return s.Controller.BackupNow(ctx, db, tags)
}

func (s *Service) Backups(ctx context.Context) ([]localapi.Backup, error) {
	if s.Controller == nil {
		return nil, errNotController
	}
	return s.Controller.Backups(ctx)
}

func (s *Service) Backup(ctx context.Context, id string) (localapi.Backup, error) {
	if s.Controller == nil {
		return localapi.Backup{}, errNotController
	}
	return s.Controller.Backup(ctx, id)
}
