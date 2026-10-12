// Package discovery finds agents by UDP subnet broadcast (blueprint:
// Coordination → Discovery). It is read-only metadata and authorises nothing.
package discovery

import (
	"cmp"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"maps"
	"net"
	"os"
	"slices"
	"strings"
	"time"
)

const (
	Prefix      = "SEVJALA/1 DISCOVER "
	DefaultWait = 2 * time.Second
	maxRequest  = 256
	nonceLen    = 16
)

type Reply struct {
	V        int    `json:"v"`
	Nonce    string `json:"nonce"`
	AgentID  string `json:"agent_id"`
	Hostname string `json:"hostname"`
	Port     int    `json:"port"`
	Role     string `json:"role"`
	Version  string `json:"version"`
	OS       string `json:"os"`
	FP       string `json:"fp"`
	// IP is the reply's source address, set by Probe.
	IP string `json:"-"`
}

type Responder struct {
	conn *net.UDPConn
	done chan struct{}
}

func Listen(addr string, info func() Reply, log *slog.Logger) (*Responder, error) {
	ua, err := net.ResolveUDPAddr("udp4", addr)
	if err != nil {
		return nil, fmt.Errorf("discovery %s: %w", addr, err)
	}
	conn, err := net.ListenUDP("udp4", ua)
	if err != nil {
		return nil, fmt.Errorf("discovery %s: %w", addr, err)
	}
	r := &Responder{conn: conn, done: make(chan struct{})}
	go r.serve(info, log)
	return r, nil
}

func (r *Responder) Addr() *net.UDPAddr { return r.conn.LocalAddr().(*net.UDPAddr) }

func (r *Responder) Close() error {
	err := r.conn.Close()
	<-r.done
	return err
}

func (r *Responder) serve(info func() Reply, log *slog.Logger) {
	defer close(r.done)
	buf := make([]byte, maxRequest+1)
	for {
		n, from, err := r.conn.ReadFromUDP(buf)
		if errors.Is(err, net.ErrClosed) {
			return
		}
		if err != nil {
			continue // Windows reports ICMP port-unreachable from earlier replies here
		}
		nonce, ok := parseRequest(buf[:n])
		if !ok {
			continue
		}
		rep := info()
		rep.V, rep.Nonce = 1, nonce
		b, err := json.Marshal(rep)
		if err != nil {
			continue
		}
		if _, err := r.conn.WriteToUDP(b, from); err != nil {
			log.Warn("discovery reply failed", "to", from.String(), "err", err)
		}
	}
}

func parseRequest(b []byte) (string, bool) {
	if len(b) > maxRequest {
		return "", false
	}
	nonce, ok := strings.CutPrefix(string(b), Prefix)
	if !ok || len(nonce) != nonceLen {
		return "", false
	}
	if _, err := hex.DecodeString(nonce); err != nil || strings.ToLower(nonce) != nonce {
		return "", false
	}
	return nonce, true
}

// Probe sends one request to each target and collects replies for wait.
func Probe(ctx context.Context, targets []*net.UDPAddr, wait time.Duration) ([]Reply, error) {
	if len(targets) == 0 {
		return nil, errors.New("no broadcast-capable IPv4 interface is up; check the network cable or add the node with 'sevjala agents add <host:port>'")
	}
	conn, err := net.ListenUDP("udp4", nil)
	if err != nil {
		return nil, fmt.Errorf("discovery: %w", err)
	}
	defer conn.Close()
	nb := make([]byte, nonceLen/2)
	if _, err := rand.Read(nb); err != nil {
		return nil, err
	}
	nonce := hex.EncodeToString(nb)
	msg := []byte(Prefix + nonce)
	var sendErr error
	sent := 0
	for _, t := range targets {
		if _, err := conn.WriteToUDP(msg, t); err != nil {
			sendErr = err
			continue
		}
		sent++
	}
	if sent == 0 {
		return nil, fmt.Errorf("discovery: %w", sendErr)
	}
	deadline := time.Now().Add(wait)
	if d, ok := ctx.Deadline(); ok && d.Before(deadline) {
		deadline = d
	}
	if err := conn.SetReadDeadline(deadline); err != nil {
		return nil, err
	}
	seen := map[string]Reply{}
	buf := make([]byte, 2048)
	for {
		n, from, err := conn.ReadFromUDP(buf)
		if errors.Is(err, os.ErrDeadlineExceeded) || ctx.Err() != nil {
			break
		}
		if err != nil {
			continue
		}
		var r Reply
		if json.Unmarshal(buf[:n], &r) != nil || r.V != 1 || r.Nonce != nonce || r.AgentID == "" {
			continue
		}
		r.IP = from.IP.String()
		seen[r.AgentID] = r
	}
	out := slices.Collect(maps.Values(seen))
	slices.SortFunc(out, func(a, b Reply) int {
		return cmp.Or(cmp.Compare(a.Hostname, b.Hostname), cmp.Compare(a.AgentID, b.AgentID))
	})
	return out, nil
}

// BroadcastTargets is the subnet broadcast address of every up, broadcast-
// capable, non-loopback IPv4 interface.
func BroadcastTargets(port int) ([]*net.UDPAddr, error) {
	ifs, err := net.Interfaces()
	if err != nil {
		return nil, err
	}
	var out []*net.UDPAddr
	for _, ifc := range ifs {
		if ifc.Flags&net.FlagUp == 0 || ifc.Flags&net.FlagBroadcast == 0 || ifc.Flags&net.FlagLoopback != 0 {
			continue
		}
		addrs, err := ifc.Addrs()
		if err != nil {
			continue
		}
		for _, a := range addrs {
			if n, ok := a.(*net.IPNet); ok {
				if b := broadcastAddr(n); b != nil {
					out = append(out, &net.UDPAddr{IP: b, Port: port})
				}
			}
		}
	}
	return out, nil
}

func broadcastAddr(n *net.IPNet) net.IP {
	ip := n.IP.To4()
	mask := n.Mask
	if len(mask) == net.IPv6len {
		mask = mask[12:]
	}
	if ip == nil || len(mask) != net.IPv4len {
		return nil
	}
	if ones, _ := mask.Size(); ones >= 31 {
		return nil
	}
	b := make(net.IP, net.IPv4len)
	for i := range b {
		b[i] = ip[i] | ^mask[i]
	}
	return b
}
