package config

import (
	"bytes"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strconv"

	"github.com/BurntSushi/toml"
)

const (
	FileName                   = "config.toml"
	RoleController             = "controller"
	RoleNode                   = "node"
	DefaultListen              = "0.0.0.0:17891"
	DefaultDiscoveryPort       = 17890
	DefaultLANPort             = 17891
	DefaultDBHost              = "127.0.0.1"
	DefaultDBPort              = 3306
	DefaultDBUser              = "root"
	DefaultMinFreeBytes  int64 = 2 << 30
	DefaultStagingKeep         = 12
)

type Config struct {
	Role          string     `toml:"role"`
	Listen        string     `toml:"listen"`
	DiscoveryPort int        `toml:"discovery_port"`
	Controller    Controller `toml:"controller"`
	Node          Node       `toml:"node"`
}

type Controller struct {
	Database Database `toml:"database"`
	Staging  Staging  `toml:"staging"`
}

type Database struct {
	Host        string   `toml:"host"`
	Port        int      `toml:"port"`
	User        string   `toml:"user"`
	PasswordRef string   `toml:"password_ref"`
	Dumper      string   `toml:"dumper"`
	Databases   []string `toml:"databases"`
}

type Staging struct {
	Keep int `toml:"keep"`
}

type Node struct {
	Store Store `toml:"store"`
}

type Store struct {
	Path         string `toml:"path"`
	MinFreeBytes int64  `toml:"min_free_bytes"`
}

func Default(role string) Config {
	return Config{
		Role: role, Listen: DefaultListen, DiscoveryPort: DefaultDiscoveryPort,
		Controller: Controller{Database: Database{Host: DefaultDBHost, Port: DefaultDBPort, User: DefaultDBUser},
			Staging: Staging{Keep: DefaultStagingKeep}},
		Node: Node{Store: Store{MinFreeBytes: DefaultMinFreeBytes}},
	}
}

func ValidRole(role string) bool {
	return role == RoleController || role == RoleNode
}

func (c Config) Validate() error {
	if !ValidRole(c.Role) {
		return fmt.Errorf("role %q: must be %q or %q", c.Role, RoleController, RoleNode)
	}
	_, port, err := net.SplitHostPort(c.Listen)
	if err != nil {
		return fmt.Errorf("listen %q: %w", c.Listen, err)
	}
	if p, err := strconv.Atoi(port); err != nil || p < 1 || p > 65535 {
		return fmt.Errorf("listen %q: port must be 1-65535", c.Listen)
	}
	if c.DiscoveryPort < 1 || c.DiscoveryPort > 65535 {
		return fmt.Errorf("discovery_port %d: must be 1-65535", c.DiscoveryPort)
	}
	if p := c.Controller.Database.Port; p < 1 || p > 65535 {
		return fmt.Errorf("controller.database.port %d: must be 1-65535", p)
	}
	if c.Controller.Staging.Keep < 0 {
		return fmt.Errorf("controller.staging.keep %d: must not be negative", c.Controller.Staging.Keep)
	}
	if c.Node.Store.MinFreeBytes < 0 {
		return fmt.Errorf("node.store.min_free_bytes %d: must not be negative", c.Node.Store.MinFreeBytes)
	}
	return nil
}

// ListenPort is the TCP port of Listen; Validate guarantees it parses.
func (c Config) ListenPort() int {
	_, port, _ := net.SplitHostPort(c.Listen)
	p, _ := strconv.Atoi(port)
	return p
}

// StagingKeep is how many recent backups per database staging keeps; 0, as
// written by builds before M3, means the default.
func (c Config) StagingKeep() int {
	if c.Controller.Staging.Keep == 0 {
		return DefaultStagingKeep
	}
	return c.Controller.Staging.Keep
}

// StorePath is where a node keeps backups: [node.store] path, else
// <data-dir>/store, which inherits the locked data dir ACL.
func (c Config) StorePath(dataDir string) string {
	if c.Node.Store.Path != "" {
		return c.Node.Store.Path
	}
	return filepath.Join(dataDir, "store")
}

// Load reads and validates a config file; keys it omits keep their defaults.
func Load(path string) (Config, error) {
	c := Default("")
	md, err := toml.DecodeFile(path, &c)
	if err != nil {
		return Config{}, fmt.Errorf("read %s: %w", path, err)
	}
	if keys := md.Undecoded(); len(keys) > 0 {
		return Config{}, fmt.Errorf("%s: unknown key %q", path, keys[0].String())
	}
	if err := c.Validate(); err != nil {
		return Config{}, fmt.Errorf("%s: %w", path, err)
	}
	return c, nil
}

// Write replaces the whole file atomically; the agent is the only writer.
func Write(path string, c Config) error {
	var b bytes.Buffer
	if err := toml.NewEncoder(&b).Encode(c); err != nil {
		return err
	}
	return writeAtomic(path, b.Bytes(), 0o644)
}

// writeAtomic goes through a synced temp file and a rename, so a crash leaves
// the old file or the complete new one.
func writeAtomic(path string, data []byte, mode os.FileMode) error {
	tmp := path + ".tmp"
	f, err := os.OpenFile(tmp, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, mode)
	if err != nil {
		return err
	}
	_, err = f.Write(data)
	if err == nil {
		err = f.Sync()
	}
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		_ = os.Remove(tmp)
		return err
	}
	return os.Rename(tmp, path)
}
