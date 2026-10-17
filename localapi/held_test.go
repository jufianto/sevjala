package localapi

import (
	"context"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/jufianto/sevjala/inventory"
)

const heldID = "BK-20261014-002000-30D3"

var heldData = "-- dump\nINSERT INTO t VALUES (1);\n"

func (f *fakeBackend) Held(context.Context) ([]inventory.Manifest, error) {
	return []inventory.Manifest{{BackupID: heldID, Database: "shop", Size: int64(len(heldData))}}, f.err
}

// OpenHeld serves heldData; a "short" id claims one byte more than it sends.
func (f *fakeBackend) OpenHeld(_ context.Context, id string) (inventory.Manifest, io.ReadCloser, error) {
	if f.err != nil {
		return inventory.Manifest{}, nil, f.err
	}
	m := inventory.Manifest{BackupID: id, Database: "shop", CreatedAt: time.Date(2026, 10, 14, 0, 20, 0, 0, time.Local), Size: int64(len(heldData))}
	if id == "short" {
		m.Size++
	}
	return m, io.NopCloser(strings.NewReader(heldData)), nil
}

func TestHeldRoundTrip(t *testing.T) {
	c := NewClient(startBackend(t, &fakeBackend{}))
	ctx := context.Background()
	ms, err := c.Held(ctx)
	if err != nil || len(ms) != 1 || ms[0].BackupID != heldID || ms[0].Database != "shop" {
		t.Fatalf("Held: %+v %v", ms, err)
	}
	rc, err := c.OpenHeld(ctx, heldID)
	if err != nil {
		t.Fatal(err)
	}
	got, err := io.ReadAll(rc)
	rc.Close()
	if err != nil || string(got) != heldData {
		t.Errorf("OpenHeld read %q, %v", got, err)
	}

	rc, err = c.OpenHeld(ctx, "short")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := io.ReadAll(rc); err == nil {
		t.Error("a stream shorter than the backup must fail")
	}
	rc.Close()
}

func TestOpenHeldErrorTravels(t *testing.T) {
	c := NewClient(startBackend(t, &fakeBackend{err: Errorf(http.StatusNotFound, "no backup %s on this PC", heldID)}))
	_, err := c.OpenHeld(context.Background(), heldID)
	var e *Error
	if !errors.As(err, &e) || e.Code != http.StatusNotFound || !strings.Contains(e.Msg, heldID) {
		t.Errorf("err = %#v", err)
	}
}

func TestHeldDownloadName(t *testing.T) {
	hc := &http.Client{Transport: NewTransport(startBackend(t, &fakeBackend{}))}
	resp, err := hc.Get("http://sevjala/held/" + heldID)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if got := resp.Header.Get("Content-Disposition"); got != `attachment; filename=shop_2026-10-14_00-20-00.sql.gz` {
		t.Errorf("Content-Disposition %q", got)
	}
}
