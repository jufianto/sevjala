package svc

import (
	"slices"
	"strings"
	"testing"
)

func TestFirewallCommands(t *testing.T) {
	add := firewallAddCommands(17891, 17890)
	if len(add) != 2 {
		t.Fatalf("%d add commands", len(add))
	}
	for _, want := range [][]string{
		{"name=" + FirewallRuleLAN, "protocol=TCP", "localport=17891", "dir=in", "action=allow"},
		{"name=" + FirewallRuleDiscovery, "protocol=UDP", "localport=17890", "dir=in", "action=allow"},
	} {
		found := false
		for _, c := range add {
			if c[0] == "netsh" && slices.Contains(c, "add") && containsAll(c, want) {
				found = true
			}
		}
		if !found {
			t.Errorf("no add command with %v in %v", want, add)
		}
	}
	for _, c := range firewallDeleteCommands() {
		if !slices.Contains(c, "delete") || !strings.HasPrefix(c[len(c)-1], "name=Sevjala-") {
			t.Errorf("delete command %v", c)
		}
	}
}

func containsAll(c, want []string) bool {
	for _, w := range want {
		if !slices.Contains(c, w) {
			return false
		}
	}
	return true
}
