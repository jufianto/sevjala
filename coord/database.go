package coord

import (
	"context"
	"net/http"
	"slices"

	"github.com/jufianto/sevjala/config"
	"github.com/jufianto/sevjala/dump"
	"github.com/jufianto/sevjala/localapi"
	"github.com/jufianto/sevjala/logx"
)

const dbSecret = "db"

func (c *Controller) DetectDatabase(ctx context.Context) (localapi.Detection, error) {
	return c.detect(ctx, c.config().Controller.Database, nil)
}

// detect uses password when given, else the stored secret.
func (c *Controller) detect(ctx context.Context, db config.Database, password *string) (localapi.Detection, error) {
	tools, err := c.FindTools(db.Dumper)
	if err != nil {
		return localapi.Detection{}, localapi.Errorf(http.StatusBadRequest, "%v", err)
	}
	var pw string
	if password != nil {
		pw = *password
	} else if pw, err = config.Secret(c.DataDir, db.PasswordRef); err != nil {
		return localapi.Detection{}, localapi.Errorf(http.StatusInternalServerError, "database password: %v", err)
	}
	d, err := dump.Detect(ctx, tools, dump.Conn{Host: db.Host, Port: db.Port, User: db.User, Password: pw})
	if err != nil {
		return localapi.Detection{}, localapi.Errorf(http.StatusBadRequest, "%v", err)
	}
	out := localapi.Detection{Dumper: tools.Dumper, Client: tools.Client, ServerVersion: d.ServerVersion, Flavour: d.Flavour,
		Databases: make([]localapi.DBDatabase, len(d.Databases))}
	for i, x := range d.Databases {
		out.Databases[i] = localapi.DBDatabase{Name: x.Name, Tables: x.Tables, Consistency: x.Consistency(),
			NonInnoDB: x.NonInnoDB, Configured: slices.Contains(db.Databases, x.Name)}
	}
	return out, nil
}

// SetDatabase tests the new settings against the server before it saves
// anything: config.toml, and the password in the keyring.
func (c *Controller) SetDatabase(ctx context.Context, s localapi.DBSettings) (localapi.Detection, error) {
	c.cfgMu.Lock()
	defer c.cfgMu.Unlock()
	cfg := c.Config
	db := &cfg.Controller.Database
	if s.Host != nil {
		db.Host = *s.Host
	}
	if s.Port != nil {
		db.Port = *s.Port
	}
	if s.User != nil {
		db.User = *s.User
	}
	if s.Dumper != nil {
		db.Dumper = *s.Dumper
	}
	if s.Databases != nil {
		db.Databases = slices.Compact(slices.Sorted(slices.Values(s.Databases)))
	}
	if err := cfg.Validate(); err != nil {
		return localapi.Detection{}, localapi.Errorf(http.StatusBadRequest, "%v", err)
	}
	if db.Host == "" || db.User == "" {
		return localapi.Detection{}, localapi.Errorf(http.StatusBadRequest, "host and user must not be empty")
	}
	d, err := c.detect(ctx, *db, s.Password)
	if err != nil {
		return localapi.Detection{}, err
	}
	for _, name := range db.Databases {
		if !slices.ContainsFunc(d.Databases, func(x localapi.DBDatabase) bool { return x.Name == name }) {
			return localapi.Detection{}, localapi.Errorf(http.StatusBadRequest, "database %s does not exist on %s:%d", name, db.Host, db.Port)
		}
	}
	if s.Password != nil {
		if *s.Password == "" {
			if err := config.DeleteSecret(c.DataDir, dbSecret); err != nil {
				return localapi.Detection{}, err
			}
			db.PasswordRef = ""
		} else {
			fallback, err := config.SetSecret(c.DataDir, dbSecret, *s.Password)
			if err != nil {
				return localapi.Detection{}, err
			}
			if fallback {
				c.Log.Warn("no OS keyring; the database password is in an encrypted file", logx.Event(logx.EventKeyringFallback))
			}
			db.PasswordRef = config.KeyringPrefix + dbSecret
		}
	}
	if err := config.Write(config.Path(c.DataDir), cfg); err != nil {
		return localapi.Detection{}, err
	}
	c.Config = cfg
	for i := range d.Databases {
		d.Databases[i].Configured = slices.Contains(db.Databases, d.Databases[i].Name)
	}
	c.Log.Info("database configured", logx.Event(logx.EventDBConfigured), "host", db.Host, "port", db.Port, "user", db.User,
		"databases", db.Databases)
	return d, nil
}
