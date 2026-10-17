package localapi

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"time"

	"github.com/jufianto/sevjala/inventory"
)

type Client struct {
	hc     *http.Client
	prefix string // "/controller" sends every call through a manager link (AD-21)
}

// callTimeout covers discover (2 s wait) and a pairing round trip; callers pass
// shorter contexts where they want them.
var callTimeout = 15 * time.Second

func NewClient(endpoint string) *Client { return NewClientPrefix(endpoint, "") }

// NewClientPrefix puts prefix before every route. A relayed call may wait for
// a manager link first, so it gets longer.
func NewClientPrefix(endpoint, prefix string) *Client {
	timeout := callTimeout
	if prefix != "" {
		timeout = max(timeout, RelayTimeout)
	}
	return &Client{hc: &http.Client{Transport: NewTransport(endpoint), Timeout: timeout}, prefix: prefix}
}

// RelayTimeout is the least a call through a manager link is given.
const RelayTimeout = 30 * time.Second

// NewTransport carries HTTP to the agent at endpoint, whatever the URL says.
func NewTransport(endpoint string) *http.Transport {
	return &http.Transport{
		DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
			return dial(ctx, endpoint)
		},
	}
}

func (c *Client) Status(ctx context.Context) (Status, error) {
	var st Status
	return st, c.do(ctx, http.MethodGet, "/status", nil, &st)
}

func (c *Client) Code(ctx context.Context) (Code, error) {
	var code Code
	return code, c.do(ctx, http.MethodPost, "/code", nil, &code)
}

func (c *Client) Discover(ctx context.Context) ([]Discovered, error) {
	var l list[Discovered]
	return l.Agents, c.do(ctx, http.MethodPost, "/discover", nil, &l)
}

func (c *Client) AddAgent(ctx context.Context, addr string) (Discovered, error) {
	var d Discovered
	return d, c.do(ctx, http.MethodPost, "/agents", map[string]string{"addr": addr}, &d)
}

func (c *Client) Pair(ctx context.Context, agent, code, fp string) (Agent, error) {
	var a Agent
	return a, c.do(ctx, http.MethodPost, "/pair", map[string]string{"agent": agent, "code": code, "fp": fp}, &a)
}

func (c *Client) RemoveAgent(ctx context.Context, agent string, force bool, confirm string) (Agent, error) {
	var a Agent
	q := url.Values{}
	if force {
		q.Set("force", "true")
	}
	if confirm != "" {
		q.Set("confirm", confirm)
	}
	path := "/agents/" + url.PathEscape(agent)
	if len(q) > 0 {
		path += "?" + q.Encode()
	}
	return a, c.do(ctx, http.MethodDelete, path, nil, &a)
}

func (c *Client) SetManager(ctx context.Context, agent string, on bool) (Agent, error) {
	var a Agent
	return a, c.do(ctx, http.MethodPut, "/agents/"+url.PathEscape(agent)+"/manager", map[string]bool{"manager": on}, &a)
}

func (c *Client) Agents(ctx context.Context) ([]Agent, error) {
	var l list[Agent]
	return l.Agents, c.do(ctx, http.MethodGet, "/agents", nil, &l)
}

func (c *Client) Ping(ctx context.Context, agent string) (Ping, error) {
	var p Ping
	return p, c.do(ctx, http.MethodPost, "/agents/"+url.PathEscape(agent)+"/ping", nil, &p)
}

func (c *Client) do(ctx context.Context, method, path string, body, out any) error {
	var rd io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return err
		}
		rd = bytes.NewReader(b)
	}
	req, err := http.NewRequestWithContext(ctx, method, "http://sevjala"+c.prefix+path, rd)
	if err != nil {
		return err
	}
	if rd != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := c.hc.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return replyError(resp, method, path)
	}
	if err := json.NewDecoder(resp.Body).Decode(out); err != nil {
		return fmt.Errorf("%s %s: %w", method, path, err)
	}
	return nil
}

func (c *Client) DetectDatabase(ctx context.Context) (Detection, error) {
	var d Detection
	return d, c.do(ctx, http.MethodPost, "/db/detect", nil, &d)
}

func (c *Client) SetDatabase(ctx context.Context, s DBSettings) (Detection, error) {
	var d Detection
	return d, c.do(ctx, http.MethodPut, "/db", s, &d)
}

func (c *Client) BackupNow(ctx context.Context, db string, tags []string) ([]BackupJob, error) {
	var out backupJobs
	err := c.do(ctx, http.MethodPost, "/backup/now", map[string]any{"db": db, "tags": tags}, &out)
	return out.Backups, err
}

// Backups lists newest first; all includes expired backups.
func (c *Client) Backups(ctx context.Context, all bool) ([]Backup, error) {
	path := "/backups"
	if all {
		path += "?all=1"
	}
	var out backupList
	err := c.do(ctx, http.MethodGet, path, nil, &out)
	return out.Backups, err
}

func (c *Client) Backup(ctx context.Context, id string) (Backup, error) {
	var b Backup
	return b, c.do(ctx, http.MethodGet, "/backups/"+url.PathEscape(id), nil, &b)
}

func (c *Client) Policies(ctx context.Context) ([]Policy, error) {
	var out policyList
	err := c.do(ctx, http.MethodGet, "/policies", nil, &out)
	return out.Policies, err
}

func (c *Client) AddPolicy(ctx context.Context, ch PolicyChange) (Policy, error) {
	var p Policy
	return p, c.do(ctx, http.MethodPost, "/policies", ch, &p)
}

func (c *Client) SetPolicy(ctx context.Context, name string, ch PolicyChange) (Policy, error) {
	var p Policy
	return p, c.do(ctx, http.MethodPatch, "/policies/"+url.PathEscape(name), ch, &p)
}

func (c *Client) RemovePolicy(ctx context.Context, name string) error {
	var out struct{}
	return c.do(ctx, http.MethodDelete, "/policies/"+url.PathEscape(name), nil, &out)
}

// RunRetention has no ceiling of its own: it waits for deletes on the LAN, and
// the caller's context bounds it.
func (c *Client) RunRetention(ctx context.Context, dryRun bool) (RetentionPlan, error) {
	var p RetentionPlan
	long := &Client{hc: &http.Client{Transport: c.hc.Transport}}
	return p, long.do(ctx, http.MethodPost, "/retention/run", map[string]bool{"dry_run": dryRun}, &p)
}

// Held lists the backups this PC holds, oldest first.
func (c *Client) Held(ctx context.Context) ([]inventory.Manifest, error) {
	var out heldList
	err := c.do(ctx, http.MethodGet, "/held", nil, &out)
	return out.Backups, err
}

// OpenHeld streams one held backup; only ctx bounds it, and a stream shorter
// than the backup fails on read.
func (c *Client) OpenHeld(ctx context.Context, id string) (io.ReadCloser, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, "http://sevjala"+c.prefix+"/held/"+url.PathEscape(id), nil)
	if err != nil {
		return nil, err
	}
	resp, err := (&http.Client{Transport: c.hc.Transport}).Do(req)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode != http.StatusOK {
		defer resp.Body.Close()
		return nil, replyError(resp, http.MethodGet, "/held/"+id)
	}
	return resp.Body, nil
}

func replyError(resp *http.Response, method, path string) error {
	var e struct {
		Error   string   `json:"error"`
		Code    string   `json:"code"`
		Word    string   `json:"word"`
		Deletes int      `json:"deletes"`
		Hosts   []string `json:"hosts"`
	}
	if json.NewDecoder(io.LimitReader(resp.Body, maxBody)).Decode(&e) != nil || e.Error == "" {
		e.Error = fmt.Sprintf("%s %s: %s", method, path, resp.Status)
	}
	return &Error{Code: resp.StatusCode, Msg: e.Error, Reason: e.Code, Word: e.Word, Deletes: e.Deletes, Hosts: e.Hosts}
}

func (c *Client) Restore(ctx context.Context, req RestoreRequest) (Restore, error) {
	var r Restore
	return r, c.do(ctx, http.MethodPost, "/restore", req, &r)
}

func (c *Client) RestoreStatus(ctx context.Context) (Restore, error) {
	var r Restore
	return r, c.do(ctx, http.MethodGet, "/restore", nil, &r)
}

// Logs asks for the newest limit entries; 0 lets the agent choose (500).
func (c *Client) Logs(ctx context.Context, limit int) ([]json.RawMessage, error) {
	path := "/logs"
	if limit > 0 {
		path += "?limit=" + strconv.Itoa(limit)
	}
	var l logList
	return l.Entries, c.do(ctx, http.MethodGet, path, nil, &l)
}

func (c *Client) Database(ctx context.Context) (DBConfig, error) {
	var d DBConfig
	return d, c.do(ctx, http.MethodGet, "/db", nil, &d)
}
