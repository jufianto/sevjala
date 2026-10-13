package config

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func writeFile(t *testing.T, content string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), FileName)
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestLoad(t *testing.T) {
	tests := []struct {
		name    string
		content string
		want    Config
		wantErr string
	}{
		{
			name:    "controller minimal gets defaults",
			content: `role = "controller"`,
			want:    Default(RoleController),
		},
		{
			name: "blueprint example",
			content: `role = "controller"
listen = "0.0.0.0:17891"
discovery_port = 17890

[controller.database]
host = "127.0.0.1"
port = 3306
user = "root"
password_ref = "keyring:db"
dumper = "C:\\xampp\\mysql\\bin\\mysqldump.exe"
databases = ["production_db"]

[controller.staging]
keep = 12

[node.store]
path = "D:\\SevjalaBackups"
min_free_bytes = 2147483648
`,
			want: Config{
				Role: RoleController, Listen: DefaultListen, DiscoveryPort: DefaultDiscoveryPort,
				Controller: Controller{
					Database: Database{Host: "127.0.0.1", Port: 3306, User: "root", PasswordRef: "keyring:db",
						Dumper: `C:\xampp\mysql\bin\mysqldump.exe`, Databases: []string{"production_db"}},
					Staging: Staging{Keep: 12},
				},
				Node: Node{Store: Store{Path: `D:\SevjalaBackups`, MinFreeBytes: 2147483648}},
			},
		},
		{name: "missing role", content: `listen = "0.0.0.0:1"`, wantErr: `role ""`},
		{name: "bad role", content: `role = "master"`, wantErr: `role "master"`},
		{name: "listen port out of range", content: "role = \"node\"\nlisten = \"0.0.0.0:99999\"", wantErr: "port must be 1-65535"},
		{name: "listen without port", content: "role = \"node\"\nlisten = \"0.0.0.0\"", wantErr: `listen "0.0.0.0"`},
		{name: "discovery port zero", content: "role = \"node\"\ndiscovery_port = 0", wantErr: "discovery_port 0"},
		{name: "unknown top key", content: "role = \"node\"\nrol = \"x\"", wantErr: `unknown key "rol"`},
		{name: "unknown nested key", content: "role = \"node\"\n[node.store]\npth = \"x\"", wantErr: `unknown key "node.store.pth"`},
		{name: "not toml", content: "role = ", wantErr: "read "},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := Load(writeFile(t, tt.content))
			if tt.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
					t.Fatalf("err = %v, want it to contain %q", err, tt.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if got.Role != tt.want.Role || got.Listen != tt.want.Listen || got.DiscoveryPort != tt.want.DiscoveryPort ||
				got.Controller.Database.Dumper != tt.want.Controller.Database.Dumper ||
				got.Controller.Staging.Keep != tt.want.Controller.Staging.Keep ||
				got.Node.Store != tt.want.Node.Store {
				t.Errorf("got %+v, want %+v", got, tt.want)
			}
		})
	}
}

func TestLoadMissingFile(t *testing.T) {
	_, err := Load(filepath.Join(t.TempDir(), FileName))
	if !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("err = %v, want fs.ErrNotExist", err)
	}
}

func TestWriteRoundTrip(t *testing.T) {
	path := filepath.Join(t.TempDir(), FileName)
	if err := Write(path, Default(RoleNode)); err != nil {
		t.Fatal(err)
	}
	got, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if got.Role != RoleNode || got.Listen != DefaultListen || got.DiscoveryPort != DefaultDiscoveryPort {
		t.Errorf("round trip = %+v", got)
	}
}

func TestListenPort(t *testing.T) {
	if got := Default(RoleNode).ListenPort(); got != DefaultLANPort {
		t.Errorf("ListenPort = %d, want %d", got, DefaultLANPort)
	}
	c := Default(RoleNode)
	c.Listen = "192.168.1.5:9000"
	if got := c.ListenPort(); got != 9000 {
		t.Errorf("ListenPort = %d, want 9000", got)
	}
}

func TestDefaultsForM2(t *testing.T) {
	c := Default(RoleController)
	if c.Controller.Database.Host != "127.0.0.1" || c.Controller.Database.Port != 3306 || c.Controller.Database.User != "root" {
		t.Errorf("database defaults %+v", c.Controller.Database)
	}
	if c.Node.Store.MinFreeBytes != 2<<30 {
		t.Errorf("min_free_bytes %d", c.Node.Store.MinFreeBytes)
	}
	if got := c.StorePath("/data"); got != filepath.Join("/data", "store") {
		t.Errorf("StorePath = %q", got)
	}
	c.Node.Store.Path = "/backups"
	if got := c.StorePath("/data"); got != "/backups" {
		t.Errorf("StorePath = %q", got)
	}
}

func TestWriteFullRoundTrip(t *testing.T) {
	path := filepath.Join(t.TempDir(), FileName)
	c := Default(RoleController)
	c.Controller.Database.PasswordRef = "keyring:db"
	c.Controller.Database.Dumper = `C:\xampp\mysql\bin\mysqldump.exe`
	c.Controller.Database.Databases = []string{"shop", "legacy"}
	c.Controller.Staging.Keep = 12
	if err := Write(path, c); err != nil {
		t.Fatal(err)
	}
	got, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got, c) {
		t.Errorf("round trip\n got %+v\nwant %+v", got, c)
	}
	if _, err := os.Stat(path + ".tmp"); !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("temp file left: %v", err)
	}
}

func TestValidateDatabasePort(t *testing.T) {
	_, err := Load(writeFile(t, "role = \"controller\"\n[controller.database]\nport = 0"))
	if err == nil || !strings.Contains(err.Error(), "controller.database.port") {
		t.Errorf("err = %v", err)
	}
}
