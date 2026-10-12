package svc

import (
	"bytes"
	"fmt"
	"os/exec"
	"runtime"
	"strconv"
	"strings"
)

const (
	FirewallRuleLAN       = "Sevjala-LAN-API"
	FirewallRuleDiscovery = "Sevjala-Discovery"
)

func firewallAddCommands(tcpPort, udpPort int) [][]string {
	rule := func(name, proto string, port int) []string {
		return []string{"netsh", "advfirewall", "firewall", "add", "rule", "name=" + name, "dir=in", "action=allow",
			"protocol=" + proto, "localport=" + strconv.Itoa(port), "profile=any"}
	}
	return [][]string{rule(FirewallRuleLAN, "TCP", tcpPort), rule(FirewallRuleDiscovery, "UDP", udpPort)}
}

func firewallDeleteCommands() [][]string {
	var out [][]string
	for _, name := range []string{FirewallRuleLAN, FirewallRuleDiscovery} {
		out = append(out, []string{"netsh", "advfirewall", "firewall", "delete", "rule", "name=" + name})
	}
	return out
}

// AddFirewallRules (re)creates the inbound rules on Windows; elsewhere the OS
// firewall is the operator's to manage.
func AddFirewallRules(tcpPort, udpPort int) error {
	if runtime.GOOS != "windows" {
		return nil
	}
	RemoveFirewallRules()
	for _, c := range firewallAddCommands(tcpPort, udpPort) {
		if out, err := exec.Command(c[0], c[1:]...).CombinedOutput(); err != nil {
			return fmt.Errorf("%s: %w: %s", strings.Join(c, " "), err, bytes.TrimSpace(out))
		}
	}
	return nil
}

// RemoveFirewallRules ignores errors: a missing rule is the goal.
func RemoveFirewallRules() {
	if runtime.GOOS != "windows" {
		return
	}
	for _, c := range firewallDeleteCommands() {
		_ = exec.Command(c[0], c[1:]...).Run()
	}
}

func FirewallRulesPresent() error {
	for _, name := range []string{FirewallRuleLAN, FirewallRuleDiscovery} {
		if err := exec.Command("netsh", "advfirewall", "firewall", "show", "rule", "name="+name).Run(); err != nil {
			return fmt.Errorf("rule %s missing; run 'sevjala service install' again", name)
		}
	}
	return nil
}
