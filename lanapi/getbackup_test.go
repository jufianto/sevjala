package lanapi

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net/http"
	"testing"
)

func TestGetBackupRoundTrip(t *testing.T) {
	n, st := startStoreNode(t)
	c := controller(t)
	pair(t, c, n)
	ctx := context.Background()
	m, data := backupData(t, 1<<20)
	if err := c.PutBackup(ctx, n.srv.Addr(), n.id.FP, m, bytes.NewReader(data)); err != nil {
		t.Fatal(err)
	}
	got, body, err := c.GetBackup(ctx, n.srv.Addr(), n.id.FP, m.BackupID)
	if err != nil {
		t.Fatal(err)
	}
	if got.SHA256 != m.SHA256 || got.Size != m.Size || got.Database != "shop" {
		t.Errorf("manifest %+v", got)
	}
	if _, err := st.Delete(m.BackupID); err == nil {
		t.Error("a delete while the backup streams must wait")
	}
	raw, err := io.ReadAll(body)
	body.Close()
	if err != nil || !bytes.Equal(raw, data) {
		t.Errorf("body %d bytes, %v", len(raw), err)
	}
	if ok, err := st.Delete(m.BackupID); !ok || err != nil {
		t.Errorf("delete after the stream: %v, %v", ok, err)
	}

	if _, _, err := c.GetBackup(ctx, n.srv.Addr(), n.id.FP, m.BackupID); httpCode(err) != http.StatusNotFound {
		t.Errorf("deleted backup: %v", err)
	}
	if _, _, err := c.GetBackup(ctx, n.srv.Addr(), n.id.FP, "nope"); httpCode(err) != http.StatusBadRequest {
		t.Errorf("bad id: %v", err)
	}
	if _, _, err := controller(t).GetBackup(ctx, n.srv.Addr(), n.id.FP, m.BackupID); !errors.Is(err, ErrRejected) {
		t.Errorf("unpaired controller: %v", err)
	}
}

func TestControllerServesNoBackups(t *testing.T) {
	id, dir := newID(t)
	s := start(t, "controller", dir, id, nil)
	if _, _, err := controller(t).GetBackup(context.Background(), s.Addr(), id.FP, "BK-20261013-101500-A8F2"); err == nil {
		t.Error("a controller must not serve backups")
	}
}
