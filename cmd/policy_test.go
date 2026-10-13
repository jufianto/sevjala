package cmd

import (
	"bytes"
	"errors"
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
	})
	want := "NAME      SCHEDULE      NEXT RUN          KEEP  TO         ENABLED\n" +
		"frequent  */10 * * * *  2026-10-13 10:20  12    all        yes\n" +
		"daily     0 1 * * *     -                 30    PC-1,PC-2  no\n" +
		"manual    -             -                 10    all        yes\n"
	if b.String() != want {
		t.Errorf("got\n%s\nwant\n%s", b.String(), want)
	}
}

func TestRenderPlan(t *testing.T) {
	var b bytes.Buffer
	renderPlan(&b, localapi.RetentionPlan{DryRun: true})
	if b.String() != "nothing to do\n" {
		t.Errorf("empty: %q", b.String())
	}
	b.Reset()
	renderPlan(&b, localapi.RetentionPlan{DryRun: true,
		Deletes:  []localapi.RetentionAction{{BackupID: "BK-1", Hostname: "PC-2", Reason: "retention: not among the newest 30 daily on this node"}},
		Sends:    []localapi.RetentionAction{{BackupID: "BK-2", Hostname: "PC-3", Reason: "backfill"}},
		Kept:     []localapi.RetentionAction{{Hostname: "PC-4", Reason: "no current inventory"}},
		Unstaged: []string{"BK-0"}, Expired: []string{"BK-0"}})
	want := "delete   BK-1  PC-2  retention: not among the newest 30 daily on this node\n" +
		"send     BK-2  PC-3  backfill\n" +
		"unstage  BK-0        \n" +
		"expire   BK-0        \n" +
		"keep     -     PC-4  no current inventory\n" +
		"dry run: nothing was changed\n"
	if b.String() != want {
		t.Errorf("got\n%q\nwant\n%q", b.String(), want)
	}
}
