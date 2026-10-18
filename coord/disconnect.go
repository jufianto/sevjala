package coord

import (
	"cmp"
	"context"
	"fmt"
	"net/http"
	"slices"
	"strings"

	"github.com/jufianto/sevjala/inventory"
	"github.com/jufianto/sevjala/localapi"
	"github.com/jufianto/sevjala/logx"
)

const removedReason = "removed on this PC by its operator"

// Disconnect makes this backup PC forget its controller (blueprint AD-25);
// the backups it holds stay.
func (s *Service) Disconnect(ctx context.Context, confirm string) (localapi.Disconnected, error) {
	if s.Node == nil {
		return localapi.Disconnected{}, errNotNode
	}
	if !localapi.CallerIsAdmin(ctx) {
		return localapi.Disconnected{}, localapi.AdminRequired(ctx, "disconnecting this PC from its controller")
	}
	st := s.StatusFunc()
	if st.Controller == nil {
		return localapi.Disconnected{}, localapi.Errorf(http.StatusConflict, "this PC is not paired with a controller")
	}
	name := cmp.Or(st.Controller.Hostname, st.Controller.AgentID)
	ms, err := inventory.List(s.Node.Store.Dir())
	if err != nil {
		return localapi.Disconnected{}, err
	}
	if confirm != st.Hostname {
		return localapi.Disconnected{}, localapi.ConfirmRequired(fmt.Sprintf(
			"this PC forgets its controller %s: backups stop coming and %[1]s can no longer reach it; its %s stay",
			name, plural(len(ms), "backup", "backups")), st.Hostname, 0, []string{st.Hostname})
	}
	if err := s.Node.Forget(); err != nil {
		return localapi.Disconnected{}, err
	}
	s.Node.Log.InfoContext(ctx, "controller disconnected", logx.Event(logx.EventControllerDisconnected),
		"controller_id", st.Controller.AgentID, "controller", name)
	return localapi.Disconnected{Controller: name, Backups: len(ms)}, nil
}

// RemoveHeld deletes backups this backup PC holds, at its operator's request
// (blueprint AD-25). The paired controller sends its own ones again.
func (s *Service) RemoveHeld(ctx context.Context, r localapi.RemoveHeld) (localapi.HeldRemoved, error) {
	if s.Node == nil {
		return localapi.HeldRemoved{}, errNotNode
	}
	if !localapi.CallerIsAdmin(ctx) {
		return localapi.HeldRemoved{}, localapi.AdminRequired(ctx, "removing backups from this PC")
	}
	if modes := len(slices.DeleteFunc([]bool{len(r.IDs) > 0, r.From != "", r.All}, func(b bool) bool { return !b })); modes != 1 {
		return localapi.HeldRemoved{}, localapi.Errorf(http.StatusBadRequest, "give backup ids, a controller (--from) or --all")
	}
	ms, err := inventory.List(s.Node.Store.Dir())
	if err != nil {
		return localapi.HeldRemoved{}, err
	}
	st := s.StatusFunc()
	var current, currentName string
	if st.Controller != nil {
		current, currentName = st.Controller.AgentID, cmp.Or(st.Controller.Hostname, st.Controller.AgentID)
	}
	pick, err := pickHeld(ms, r, current)
	if err != nil {
		return localapi.HeldRemoved{}, err
	}
	var size int64
	again := 0
	for _, m := range pick {
		size += m.Size
		if m.SourceAgent == current {
			again++
		}
	}
	if r.Confirm != st.Hostname {
		msg := fmt.Sprintf("this removes %s (%s) from this PC", plural(len(pick), "backup", "backups"), inventory.HumanBytes(size))
		if again > 0 {
			msg += fmt.Sprintf("; %s, which this PC is paired with, sends %s again", currentName, plural(again, "of them", "of them"))
		}
		return localapi.HeldRemoved{}, localapi.ConfirmRequired(msg, st.Hostname, len(pick), []string{st.Hostname})
	}
	out := localapi.HeldRemoved{Removed: []string{}}
	for _, m := range pick {
		if _, err := s.Node.Store.Delete(m.BackupID); err != nil {
			out.Failed = append(out.Failed, m.BackupID+": "+err.Error())
			continue
		}
		s.Node.Log.InfoContext(ctx, "backup deleted", logx.Event(logx.EventBackupDeleted), "backup_id", m.BackupID, "reason", removedReason)
		out.Removed = append(out.Removed, m.BackupID)
		out.Bytes += m.Size
		if m.SourceAgent == current {
			out.Resend++
		}
	}
	return out, nil
}

func pickHeld(ms []inventory.Manifest, r localapi.RemoveHeld, current string) ([]inventory.Manifest, error) {
	switch {
	case r.All:
		return ms, nil
	case r.From != "":
		srcs := inventory.Sources(ms, current)
		names := make([]string, len(srcs))
		for i, src := range srcs {
			names[i] = cmp.Or(src.Hostname, src.AgentID)
		}
		slices.Sort(names)
		src, err := resolve(srcs, r.From, func(s inventory.Source) (string, string) { return s.AgentID, s.Hostname },
			"this PC holds backups from "+strings.Join(names, ", "))
		if err != nil {
			return nil, err
		}
		return slices.DeleteFunc(slices.Clone(ms), func(m inventory.Manifest) bool { return m.SourceAgent != src.AgentID }), nil
	}
	var out []inventory.Manifest
	for _, id := range r.IDs {
		if !inventory.ValidID(id) {
			return nil, localapi.Errorf(http.StatusBadRequest, "%q is not a backup id: they look like BK-20261014-002000-30D3", id)
		}
		i := slices.IndexFunc(ms, func(m inventory.Manifest) bool { return m.BackupID == id })
		if i < 0 {
			return nil, localapi.Errorf(http.StatusNotFound, "%s is not on this PC", id)
		}
		if !slices.ContainsFunc(out, func(m inventory.Manifest) bool { return m.BackupID == id }) {
			out = append(out, ms[i])
		}
	}
	return out, nil
}
