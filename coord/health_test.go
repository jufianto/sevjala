package coord

import (
	"strings"
	"testing"
	"time"

	"github.com/jufianto/sevjala/localapi"
	"github.com/jufianto/sevjala/store"
)

var hNow = time.Date(2026, 10, 17, 10, 27, 0, 0, time.Local)

func hBackup(id, db string, ago time.Duration, size int64, state string, copies ...store.Copy) store.Backup {
	return store.Backup{BackupID: id, Database: db, CreatedAt: hNow.Add(-ago), Size: size, Tags: []string{"frequent"},
		State: state, Copies: copies}
}

func hCopy(agent, state, lastErr string) store.Copy {
	return store.Copy{AgentID: agent, State: state, LastError: lastErr}
}

func hAgent(id, host, state string, seenAgo time.Duration) localapi.Agent {
	a := localapi.Agent{AgentID: id, Hostname: host, State: state}
	if seenAgo >= 0 {
		a.LastSeen = hNow.Add(-seenAgo)
	}
	return a
}

// hGood is a protected controller: one database, one online node, a backup
// 7 min old verified there, a 10-minute schedule, plenty of disk.
func hGood() healthInput {
	return healthInput{
		now:       hNow,
		databases: []string{"shop"},
		agents:    []localapi.Agent{hAgent("n1", "PC-1", store.AgentOnline, time.Second)},
		backups: []store.Backup{hBackup("BK-2", "shop", 7*time.Minute, 10<<20, store.BackupDistributed,
			hCopy("n1", store.CopyVerified, ""))},
		staged:   func(string) bool { return true },
		failed:   map[string]failure{},
		policies: []store.Policy{{Name: "frequent", Cron: "*/10 * * * *", Keep: 12, Enabled: true}, {Name: "manual", Keep: 10, Enabled: true}},
		free:     50 << 30,
	}
}

func TestControllerHealth(t *testing.T) {
	tests := []struct {
		name     string
		change   func(*healthInput)
		level    string
		headline string
		item     string
	}{
		{"protected", func(*healthInput) {}, localapi.HealthOK, "Protected: last backup 10:20 (7 min ago) is on 1 backup PC", ""},
		{"daily schedule allows two days", func(in *healthInput) {
			in.policies = []store.Policy{{Name: "daily", Cron: "0 1 * * *", Keep: 30, Enabled: true}}
			in.backups[0].CreatedAt = hNow.Add(-30 * time.Hour)
		}, localapi.HealthOK, "Protected: last backup 2026-10-16 04:27 (30 h ago)", ""},
		{"no backup PC", func(in *healthInput) { in.agents = nil; in.backups[0].Copies = nil },
			localapi.HealthProblem, "Not protected: no backup PC is paired; backups exist only on this PC", ""},
		{"no good backup yet", func(in *healthInput) { in.backups[0].Copies[0].State = store.CopyPending },
			localapi.HealthProblem, "No backup of shop has reached a backup PC yet", ""},
		{"overdue", func(in *healthInput) { in.backups[0].CreatedAt = hNow.Add(-3 * time.Hour) },
			localapi.HealthProblem, "No backup of shop has reached a backup PC since 07:27 (3 h ago)", ""},
		{"last dump failed", func(in *healthInput) {
			in.failed["shop"] = failure{at: hNow.Add(-2 * time.Minute), err: "MySQL is not running"}
		}, localapi.HealthProblem, "The last backup of shop failed at 10:25: MySQL is not running", ""},
		{"half-loaded replace", func(in *healthInput) { in.incomplete = map[string]string{"shop": "BK-1"} },
			localapi.HealthProblem, "shop was left partly loaded by a failed restore; run: sevjala restore BK-1 --replace-production", ""},
		{"disk almost full", func(in *healthInput) { in.free = 512 << 20 },
			localapi.HealthProblem, "Disk almost full on this PC: 512.0 MiB left", ""},
		{"disk under twice the largest backup", func(in *healthInput) { in.backups[0].Size = 2 << 30; in.free = 3 << 30 },
			localapi.HealthProblem, "Disk almost full on this PC: 3.0 GiB left", ""},
		{"a backup PC is off", func(in *healthInput) {
			in.agents = append(in.agents, hAgent("n2", "PC-2", store.AgentOffline, 2*time.Hour+15*time.Minute))
			in.backups[0].Copies = append(in.backups[0].Copies, hCopy("n2", store.CopyOffline, ""))
			in.backups = append(in.backups, hBackup("BK-1", "shop", 17*time.Minute, 10<<20, store.BackupPartial,
				hCopy("n1", store.CopyVerified, ""), hCopy("n2", store.CopyPending, "")))
		}, localapi.HealthWarning, "PC-2 has not answered since 08:12 (2 h ago); 2 copies wait for it", ""},
		{"one copy waits", func(in *healthInput) {
			in.agents = append(in.agents, hAgent("n2", "PC-2", store.AgentOffline, time.Hour))
			in.backups[0].Copies = append(in.backups[0].Copies, hCopy("n2", store.CopyOffline, ""))
		}, localapi.HealthWarning, "PC-2 has not answered since 09:27 (1 h ago); 1 copy waits for it", ""},
		{"a backup PC never answered", func(in *healthInput) {
			in.agents = append(in.agents, hAgent("n2", "PC-2", store.AgentOffline, -1))
		}, localapi.HealthWarning, "PC-2 has not answered yet", ""},
		{"a backup PC warns", func(in *healthInput) {
			in.agents[0].State, in.agents[0].Problem = store.AgentWarning, "disk almost full: 1.2 GiB left"
		}, localapi.HealthWarning, "PC-1: disk almost full: 1.2 GiB left", ""},
		{"no schedule", func(in *healthInput) { in.policies[0].Enabled = false },
			localapi.HealthWarning, "No schedule is on: backups run only with 'sevjala backup now'", ""},
		{"no database", func(in *healthInput) { in.databases = nil },
			localapi.HealthWarning, "No database chosen: run sevjala db set", ""},
		{"newest backup stuck on an online PC", func(in *healthInput) {
			in.agents = append(in.agents, hAgent("n2", "PC-2", store.AgentOnline, time.Second))
			in.backups = append(in.backups, hBackup("BK-1", "shop", 40*time.Minute, 10<<20, store.BackupDistributed,
				hCopy("n1", store.CopyVerified, "")))
			in.backups[0].State = store.BackupPartial
			in.backups[0].CreatedAt = hNow.Add(-35 * time.Minute)
			in.backups[0].Copies = append(in.backups[0].Copies, hCopy("n2", store.CopyFailed, "disk full"))
		}, localapi.HealthWarning, "The newest backup of shop is not on PC-2 yet: disk full", ""},
		{"damaged copy that cannot be replaced", func(in *healthInput) {
			in.backups = append(in.backups, hBackup("BK-0", "shop", 26*time.Hour, 10<<20, store.BackupPartial,
				hCopy("n1", store.CopyCorrupt, store.ReasonHashMismatch)))
			in.staged = func(id string) bool { return id != "BK-0" }
		}, localapi.HealthWarning, "PC-1 holds 1 damaged copy that cannot be replaced (this PC no longer has that backup)", ""},
		{"problems come before warnings", func(in *healthInput) {
			in.policies[0].Enabled = false
			in.failed["shop"] = failure{at: hNow.Add(-2 * time.Minute), err: "boom"}
		}, localapi.HealthProblem, "The last backup of shop failed at 10:25: boom", "No schedule is on"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			in := hGood()
			tt.change(&in)
			h := controllerHealth(in)
			if h.Level != tt.level || !strings.HasPrefix(h.Headline, tt.headline) {
				t.Errorf("level %q headline %q\nwant %q %q\nitems %+v", h.Level, h.Headline, tt.level, tt.headline, h.Items)
			}
			if tt.level != localapi.HealthOK && (len(h.Items) == 0 || h.Items[0].Text != h.Headline) {
				t.Errorf("the headline is not the first item: %+v", h.Items)
			}
			if tt.item != "" && !hasItem(h, tt.item) {
				t.Errorf("no item %q in %+v", tt.item, h.Items)
			}
		})
	}
}

func hasItem(h localapi.Health, prefix string) bool {
	for _, it := range h.Items {
		if strings.HasPrefix(it.Text, prefix) {
			return true
		}
	}
	return false
}

func TestControllerHealthSummary(t *testing.T) {
	in := hGood()
	in.agents = append(in.agents, hAgent("n2", "PC-2", store.AgentOffline, time.Hour))
	in.backups[0].Copies = append(in.backups[0].Copies, hCopy("n2", store.CopyOffline, ""))
	in.failed["shop"] = failure{at: hNow.Add(-time.Minute), err: "boom"}
	h := controllerHealth(in)
	if len(h.Databases) != 1 {
		t.Fatalf("databases %+v", h.Databases)
	}
	d := h.Databases[0]
	if d.Name != "shop" || d.LastGood != "BK-2" || d.Copies != 1 || d.Planned != 2 || d.Size != 10<<20 || d.LastError != "boom" || d.LastFailedAt.IsZero() {
		t.Errorf("database %+v", d)
	}
	if h.NextPolicy != "frequent" || !h.NextBackupAt.Equal(time.Date(2026, 10, 17, 10, 30, 0, 0, time.Local)) {
		t.Errorf("next %s at %v", h.NextPolicy, h.NextBackupAt)
	}
	if len(h.Agents) != 2 || h.Agents[1].Hostname != "PC-2" || h.Agents[1].Waiting != 1 || h.Agents[1].State != store.AgentOffline {
		t.Errorf("agents %+v", h.Agents)
	}
	if h.FreeBytes != 50<<30 {
		t.Errorf("free %d", h.FreeBytes)
	}
}

func TestNodeHealth(t *testing.T) {
	good := func() nodeInput {
		return nodeInput{now: hNow, started: hNow.Add(-time.Hour), contact: hNow.Add(-15 * time.Second), paired: true,
			store: &localapi.StoreInfo{Backups: 34, FreeBytes: 120 << 30, Problems: []string{}}, minFree: 1 << 30}
	}
	tests := []struct {
		name     string
		change   func(*nodeInput)
		level    string
		headline string
	}{
		{"holding", func(*nodeInput) {}, localapi.HealthOK, "Holding 34 backups; the controller last checked in at 10:26 (just now)"},
		{"just started", func(in *nodeInput) { in.contact = time.Time{}; in.started = hNow.Add(-2 * time.Minute) },
			localapi.HealthOK, "Holding 34 backups; waiting for the controller to check in"},
		{"not paired", func(in *nodeInput) { in.paired = false },
			localapi.HealthWarning, "Not paired: run 'sevjala code' here and pair this PC from the controller"},
		{"controller silent", func(in *nodeInput) { in.contact = hNow.Add(-20 * time.Minute) },
			localapi.HealthWarning, "The controller has not contacted this PC since 10:07 (20 min ago)"},
		{"controller silent since start-up", func(in *nodeInput) { in.contact = time.Time{} },
			localapi.HealthWarning, "The controller has not contacted this PC since this agent started at 09:27 (1 h ago)"},
		{"disk almost full", func(in *nodeInput) { in.store.FreeBytes = 512 << 20 },
			localapi.HealthProblem, "Disk almost full on this PC: 512.0 MiB left"},
		{"store problems", func(in *nodeInput) { in.store.Problems = []string{"BK-1.sql.gz: size differs from its manifest"} },
			localapi.HealthWarning, "BK-1.sql.gz: size differs from its manifest"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			in := good()
			tt.change(&in)
			h := nodeHealth(in)
			if h.Level != tt.level || h.Headline != tt.headline {
				t.Errorf("level %q headline %q\nwant %q %q", h.Level, h.Headline, tt.level, tt.headline)
			}
			if h.FreeBytes != in.store.FreeBytes || !h.ControllerContactAt.Equal(in.contact) {
				t.Errorf("free %d contact %v", h.FreeBytes, h.ControllerContactAt)
			}
		})
	}
}

// Overdue is judged from the last good backup along its own schedule, so an
// uneven schedule (weekdays only, twice a day) is not red in its quiet hours.
func TestOverdueFollowsTheSchedule(t *testing.T) {
	at := func(day, hour int) time.Time { return time.Date(2026, 10, day, hour, 0, 0, 0, time.Local) }
	tests := []struct {
		name      string
		cron      string
		last, now time.Time
		overdue   bool
	}{
		{"weekdays, Friday's backup on Sunday", "0 8 * * 1-5", at(16, 8), at(18, 9), false},
		{"weekdays, Friday's backup on Tuesday noon", "0 8 * * 1-5", at(16, 8), at(20, 12), true},
		{"twice a day, overnight", "0 12,17 * * *", at(16, 17), at(17, 4), false},
		{"twice a day, two runs missed", "0 12,17 * * *", at(16, 17), at(17, 18), true},
		{"every 10 min, within the hour", "*/10 * * * *", at(17, 9), at(17, 9).Add(50 * time.Minute), false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			in := hGood()
			in.now = tt.now
			in.policies = []store.Policy{{Name: "p", Cron: tt.cron, Keep: 10, Enabled: true}}
			in.backups[0].CreatedAt = tt.last
			h := controllerHealth(in)
			if got := hasItem(h, "No backup of shop has reached a backup PC since"); got != tt.overdue {
				t.Errorf("overdue %v, want %v: %+v", got, tt.overdue, h.Items)
			}
		})
	}
}
