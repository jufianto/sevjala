package cmd

import (
	"bytes"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/jufianto/sevjala/localapi"
)

func parsePolicy(t *testing.T, adding bool, args []string) (localapi.PolicyChange, error) {
	t.Helper()
	c, f := policyEditCmd(&globals{}, adding)
	if err := c.ParseFlags(args); err != nil {
		t.Fatal(err)
	}
	return f.change(c.Flags(), adding)
}

func TestPolicyFlags(t *testing.T) {
	ch, err := parsePolicy(t, true, []string{"--keep", "4", "--schedule", "@weekly", "--to", "PC-1,PC-2"})
	if err != nil || *ch.Keep != 4 || *ch.Schedule != "@weekly" || len(ch.To) != 2 || ch.Enabled != nil {
		t.Errorf("add %+v, %v", ch, err)
	}
	ch, err = parsePolicy(t, false, []string{"--disable"})
	if err != nil || ch.Keep != nil || ch.Schedule != nil || ch.To != nil || ch.Enabled == nil || *ch.Enabled {
		t.Errorf("set %+v, %v", ch, err)
	}
	ch, err = parsePolicy(t, false, []string{"--keep", "1", "--confirm", "daily"})
	if err != nil || ch.Confirm != "daily" {
		t.Errorf("set --confirm %+v, %v", ch, err)
	}
	ch, err = parsePolicy(t, false, []string{"--schedule", "", "--to", "all"})
	if err != nil || *ch.Schedule != "" || len(ch.To) != 1 || ch.To[0] != "all" {
		t.Errorf("set %+v, %v", ch, err)
	}
	for _, tt := range []struct {
		adding bool
		args   []string
	}{{true, nil}, {false, nil}, {false, []string{"--enable", "--disable"}}} {
		var ee *exitError
		if _, err := parsePolicy(t, tt.adding, tt.args); !errors.As(err, &ee) || ee.code != 2 {
			t.Errorf("%v %v: err = %v", tt.adding, tt.args, err)
		}
	}
}

func TestRenderPolicies(t *testing.T) {
	var b bytes.Buffer
	renderPolicies(&b, []localapi.Policy{
		{Name: "frequent", Schedule: "*/10 * * * *", Keep: 12, To: []localapi.PolicyTarget{}, Enabled: true,
			NextRun: time.Date(2026, 10, 13, 10, 20, 0, 0, time.Local)},
		{Name: "daily", Schedule: "0 1 * * *", Keep: 30, Enabled: false,
			To: []localapi.PolicyTarget{{Hostname: "PC-1"}, {Hostname: "PC-2"}}},
		{Name: "manual", Keep: 10, To: []localapi.PolicyTarget{}, Enabled: true},
		{Name: "pre-restore", Keep: 5, To: []localapi.PolicyTarget{}, Enabled: true},
	})
	want := "NAME         WHEN                                      NEXT RUN          KEEP  TO         ENABLED\n" +
		"frequent     every 10 minutes                          2026-10-13 10:20  12    all        yes\n" +
		"daily        every day at 01:00                        -                 30    PC-1,PC-2  no\n" +
		"manual       only when run by hand (backup now --tag)  -                 10    all        yes\n" +
		"pre-restore  before a restore replaces the database    -                 5     all        yes\n"
	if b.String() != want {
		t.Errorf("got\n%s\nwant\n%s", b.String(), want)
	}
}

func TestRenderPlan(t *testing.T) {
	var b bytes.Buffer
	renderPlan(&b, localapi.RetentionPlan{DryRun: true}, map[string]int{"PC-2": 12, "PC-1": 40})
	if want := "nothing to do: every backup PC holds what the policies keep (PC-1: 40 backups, PC-2: 12 backups)\n"; b.String() != want {
		t.Errorf("empty:\n got %q\nwant %q", b.String(), want)
	}
	b.Reset()
	renderPlan(&b, localapi.RetentionPlan{}, nil)
	if b.String() != "nothing to do\n" {
		t.Errorf("empty, no PCs: %q", b.String())
	}
	b.Reset()
	renderPlan(&b, localapi.RetentionPlan{DryRun: true,
		Deletes:  []localapi.RetentionAction{{BackupID: "BK-1", Hostname: "PC-2", Reason: "daily keeps only the newest 30"}},
		Sends:    []localapi.RetentionAction{{BackupID: "BK-2", Hostname: "PC-3", Reason: "missing on this PC"}, {BackupID: "BK-3", Hostname: "PC-2", Reason: "missing on this PC"}},
		Kept:     []localapi.RetentionAction{{Hostname: "PC-4", Reason: "this PC did not report what it holds"}},
		Unstaged: []string{"BK-0"}, Expired: []string{"BK-0"}}, nil)
	want := "PC-2\n" +
		"  delete               BK-1  daily keeps only the newest 30\n" +
		"  send                 BK-3  missing on this PC\n" +
		"PC-3\n" +
		"  send                 BK-2  missing on this PC\n" +
		"PC-4\n" +
		"  keep                 -     this PC did not report what it holds\n" +
		"controller\n" +
		"  remove from staging  BK-0  not among the newest kept on the controller\n" +
		"  forget               BK-0  no copy left anywhere\n" +
		"dry run: nothing was changed. Run it without --dry-run, or wait: it runs by itself every 5 minutes.\n"
	if b.String() != want {
		t.Errorf("got\n%s\nwant\n%s", b.String(), want)
	}
	b.Reset()
	renderPlan(&b, localapi.RetentionPlan{Sends: []localapi.RetentionAction{{BackupID: "BK-2", Hostname: "PC-3", Reason: "missing on this PC"}}}, nil)
	if !strings.HasSuffix(b.String(), "done\n") {
		t.Errorf("a real run ends with done: %q", b.String())
	}
}

func TestVerifiedPerHost(t *testing.T) {
	got := verifiedPerHost([]localapi.Backup{
		{Copies: []localapi.BackupCopy{{Hostname: "PC-1", State: "verified"}, {Hostname: "PC-2", State: "pending"}}},
		{Copies: []localapi.BackupCopy{{Hostname: "PC-1", State: "verified"}, {Hostname: "PC-2", State: "verified"}}},
	})
	if got["PC-1"] != 2 || got["PC-2"] != 1 || len(got) != 2 {
		t.Errorf("got %v", got)
	}
}
