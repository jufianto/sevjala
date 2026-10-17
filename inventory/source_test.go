package inventory

import (
	"reflect"
	"testing"
	"time"
)

func TestSourcesGroupTheBackupsByController(t *testing.T) {
	at := func(h int) time.Time { return time.Date(2026, 10, 17, h, 0, 0, 0, time.UTC) }
	ms := []Manifest{
		{BackupID: "BK-1", SourceAgent: "old00001", CreatedAt: at(1), SourceHost: "OLD-PC"},
		{BackupID: "BK-2", SourceAgent: "c960acf5", CreatedAt: at(2)},
		{BackupID: "BK-3", SourceAgent: "c960acf5", CreatedAt: at(3), SourceHost: "SERVER-01"},
		{BackupID: "BK-4", SourceAgent: "c960acf5", CreatedAt: at(4)},
		{BackupID: "BK-5", SourceAgent: "old00001", CreatedAt: at(5)},
		{BackupID: "BK-6", SourceAgent: "anon0001", CreatedAt: at(6)},
	}
	want := []Source{
		{AgentID: "c960acf5", Hostname: "SERVER-01", Backups: 3, Current: true},
		{AgentID: "old00001", Hostname: "OLD-PC", Backups: 2},
		{AgentID: "anon0001", Backups: 1},
	}
	if got := Sources(ms, "c960acf5"); !reflect.DeepEqual(got, want) {
		t.Errorf("got  %+v\nwant %+v", got, want)
	}
}

// The newest backup that carries a name wins: a controller may be renamed.
func TestASourceTakesItsNewestName(t *testing.T) {
	ms := []Manifest{
		{BackupID: "BK-1", SourceAgent: "c960acf5", CreatedAt: time.Unix(1, 0), SourceHost: "OLD-NAME"},
		{BackupID: "BK-2", SourceAgent: "c960acf5", CreatedAt: time.Unix(2, 0), SourceHost: "NEW-NAME"},
	}
	if got := Sources(ms, ""); len(got) != 1 || got[0].Hostname != "NEW-NAME" || got[0].Current {
		t.Errorf("%+v", got)
	}
}

func TestNoBackupsNoSources(t *testing.T) {
	if got := Sources(nil, "c960acf5"); len(got) != 0 {
		t.Errorf("%+v", got)
	}
}
