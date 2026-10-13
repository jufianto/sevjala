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
	"time"
)

type Client struct {
	hc *http.Client
}

// NewClient's 15 s ceiling covers discover (2 s wait) and a pairing round trip;
// callers pass shorter contexts where they want them.
func NewClient(endpoint string) *Client {
	tr := &http.Transport{
		DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
			return dial(ctx, endpoint)
		},
	}
	return &Client{hc: &http.Client{Transport: tr, Timeout: 15 * time.Second}}
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
	req, err := http.NewRequestWithContext(ctx, method, "http://sevjala"+path, rd)
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
		var e struct {
			Error string `json:"error"`
		}
		if json.NewDecoder(io.LimitReader(resp.Body, maxBody)).Decode(&e) != nil || e.Error == "" {
			e.Error = fmt.Sprintf("%s %s: %s", method, path, resp.Status)
		}
		return &Error{Code: resp.StatusCode, Msg: e.Error}
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

func (c *Client) Backups(ctx context.Context) ([]Backup, error) {
	var out backupList
	err := c.do(ctx, http.MethodGet, "/backups", nil, &out)
	return out.Backups, err
}

func (c *Client) Backup(ctx context.Context, id string) (Backup, error) {
	var b Backup
	return b, c.do(ctx, http.MethodGet, "/backups/"+url.PathEscape(id), nil, &b)
}
