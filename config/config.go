package config

import (
	"fmt"
	"net"
	"os"
	"strconv"

	"github.com/BurntSushi/toml"
)

const (
	FileName             = "config.toml"
	RoleController       = "controller"
	RoleNode             = "node"
	DefaultListen        = "0.0.0.0:17891"
	DefaultDiscoveryPort = 17890
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
	return Config{Role: role, Listen: DefaultListen, DiscoveryPort: DefaultDiscoveryPort}
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
	return nil
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

// Write writes the top-level keys only; role sections are added by the
// milestones that use them.
func Write(path string, c Config) error {
	data := fmt.Sprintf("role = %q\nlisten = %q\ndiscovery_port = %d\n", c.Role, c.Listen, c.DiscoveryPort)
	return os.WriteFile(path, []byte(data), 0o644)
}
