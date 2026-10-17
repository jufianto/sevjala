package coord

import (
	"context"
	"errors"
	"io"
	"io/fs"
	"net/http"

	"github.com/jufianto/sevjala/inventory"
	"github.com/jufianto/sevjala/localapi"
)

// Held lists what this PC holds: a node's store, or the controller's staging.
func (s *Service) Held(context.Context) ([]inventory.Manifest, error) {
	if s.Node != nil {
		return inventory.List(s.Node.Store.Dir())
	}
	return inventory.List(s.Controller.Staging)
}

func (s *Service) OpenHeld(ctx context.Context, id string) (inventory.Manifest, io.ReadCloser, error) {
	var m inventory.Manifest
	if !localapi.CallerIsAdmin(ctx) {
		return m, nil, localapi.AdminRequired("copying a backup out (export)")
	}
	var rc io.ReadCloser
	var err error
	where := "this PC"
	if s.Node != nil {
		m, rc, err = s.Node.Store.Open(id)
	} else {
		where = "this PC's staging (it keeps only the newest backups); export it on a backup PC that holds it"
		m, rc, err = inventory.OpenBackup(s.Controller.Staging, id)
	}
	switch {
	case errors.Is(err, inventory.ErrInvalid):
		return m, nil, localapi.Errorf(http.StatusBadRequest, "%q is not a backup id: they look like BK-20261014-002000-30D3", id)
	case errors.Is(err, fs.ErrNotExist):
		return m, nil, localapi.Errorf(http.StatusNotFound, "%s is not in %s", id, where)
	case errors.Is(err, inventory.ErrBusy):
		return m, nil, localapi.Errorf(http.StatusConflict, "%s is being received or deleted right now; try again in a minute", id)
	case errors.Is(err, inventory.ErrMismatch):
		return m, nil, localapi.Errorf(http.StatusConflict, "%s is damaged on this PC (not the size its manifest says); export it from another PC", id)
	}
	return m, rc, err
}
