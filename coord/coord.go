// Package coord is the role logic behind the local API: a node issues pairing
// codes, a controller discovers, pairs and pings nodes.
package coord

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"slices"

	"github.com/jufianto/sevjala/inventory"
	"github.com/jufianto/sevjala/localapi"
	"github.com/jufianto/sevjala/logx"
	"github.com/jufianto/sevjala/pairing"
)

// Service holds exactly one of Node or Controller, by role.
type Service struct {
	StatusFunc func() localapi.Status
	Node       *Node
	Controller *Controller
	LogDir     string // where the agent writes sevjala.log
}

var _ localapi.Backend = (*Service)(nil)

var (
	errNotNode       = localapi.Errorf(http.StatusConflict, "this agent is a controller; run this on a node")
	errNotController = localapi.Errorf(http.StatusConflict, "this agent is a node; run this on the controller")
)

func (s *Service) Status() localapi.Status { return s.StatusFunc() }

func (s *Service) Code(ctx context.Context) (localapi.Code, error) {
	if s.Node == nil {
		return localapi.Code{}, errNotNode
	}
	if !localapi.CallerIsAdmin(ctx) {
		return localapi.Code{}, localapi.AdminRequired("issuing a pairing code")
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

func (s *Service) RemoveAgent(ctx context.Context, agent string, force bool) (localapi.Agent, error) {
	if s.Controller == nil {
		return localapi.Agent{}, errNotController
	}
	if !localapi.CallerIsAdmin(ctx) {
		return localapi.Agent{}, localapi.AdminRequired("removing a backup PC")
	}
	return s.Controller.RemoveAgent(ctx, agent, force)
}

func (s *Service) Pair(ctx context.Context, agent, code, fp string) (localapi.Agent, error) {
	if s.Controller == nil {
		return localapi.Agent{}, errNotController
	}
	if !localapi.CallerIsAdmin(ctx) {
		return localapi.Agent{}, localapi.AdminRequired("pairing a backup PC")
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
	Store   *inventory.Store
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
	if set.Dumper != nil && !localapi.CallerIsAdmin(ctx) {
		return localapi.Detection{}, localapi.AdminRequired("choosing the dump program (--dumper)")
	}
	// The agent runs the mysql client as SYSTEM/root against whatever server is named.
	cur := s.Controller.config().Controller.Database
	if ((set.Host != nil && *set.Host != cur.Host) || (set.Port != nil && *set.Port != cur.Port)) && !localapi.CallerIsAdmin(ctx) {
		return localapi.Detection{}, localapi.AdminRequired("pointing Sevjala at another database server (--host, --port)")
	}
	return s.Controller.SetDatabase(ctx, set)
}

func (s *Service) BackupNow(ctx context.Context, db string, tags []string) ([]localapi.BackupJob, error) {
	if s.Controller == nil {
		return nil, errNotController
	}
	// Each backup with a policy's tag pushes that policy's oldest one out.
	if slices.ContainsFunc(tags, func(t string) bool { return t != defaultTag }) && !localapi.CallerIsAdmin(ctx) {
		return nil, localapi.AdminRequired("a backup tagged for a schedule (--tag) pushes that schedule's oldest backups out, so it")
	}
	return s.Controller.BackupNow(ctx, db, tags)
}

func (s *Service) Backups(ctx context.Context, all bool) ([]localapi.Backup, error) {
	if s.Controller == nil {
		return nil, errNotController
	}
	return s.Controller.Backups(ctx, all)
}

func (s *Service) RunRetention(ctx context.Context, dryRun bool) (localapi.RetentionPlan, error) {
	if s.Controller == nil {
		return localapi.RetentionPlan{}, errNotController
	}
	return s.Controller.Reconcile(ctx, dryRun)
}

func (s *Service) Backup(ctx context.Context, id string) (localapi.Backup, error) {
	if s.Controller == nil {
		return localapi.Backup{}, errNotController
	}
	return s.Controller.Backup(ctx, id)
}

func (s *Service) Policies(ctx context.Context) ([]localapi.Policy, error) {
	if s.Controller == nil {
		return nil, errNotController
	}
	return s.Controller.Policies(ctx)
}

func (s *Service) AddPolicy(ctx context.Context, p localapi.PolicyChange) (localapi.Policy, error) {
	if s.Controller == nil {
		return localapi.Policy{}, errNotController
	}
	if !localapi.CallerIsAdmin(ctx) {
		if used, err := s.Controller.tagInUse(ctx, p.Name); err != nil {
			return localapi.Policy{}, err
		} else if used {
			return localapi.Policy{}, localapi.AdminRequired("adding policy " + p.Name + " (existing backups carry that tag, and it would delete some)")
		}
	}
	return s.Controller.AddPolicy(ctx, p)
}

func (s *Service) SetPolicy(ctx context.Context, name string, p localapi.PolicyChange) (localapi.Policy, error) {
	if s.Controller == nil {
		return localapi.Policy{}, errNotController
	}
	if !localapi.CallerIsAdmin(ctx) {
		if p.Schedule != nil || p.Enabled != nil {
			return localapi.Policy{}, localapi.AdminRequired("changing when policy " + name + " runs (a faster schedule pushes old backups out)")
		}
		if del, err := s.Controller.policyDeletes(ctx, name, p); err != nil {
			return localapi.Policy{}, err
		} else if del {
			return localapi.Policy{}, localapi.AdminRequired("lowering keep or taking a backup PC out of policy " + name + " (it deletes backups)")
		}
	}
	return s.Controller.SetPolicy(ctx, name, p)
}

func (s *Service) RemovePolicy(ctx context.Context, name string) error {
	if s.Controller == nil {
		return errNotController
	}
	return s.Controller.RemovePolicy(ctx, name)
}

func (s *Service) Logs(_ context.Context, limit int) ([]json.RawMessage, error) {
	return logx.Tail(s.LogDir, limit)
}
