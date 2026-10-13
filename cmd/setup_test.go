package cmd

import (
	"bufio"
	"errors"
	"slices"
	"strings"
	"testing"

	"github.com/kardianos/service"

	"github.com/jufianto/sevjala/config"
)

// fakeSetup records what the wizard does instead of doing it.
type fakeSetup struct {
	calls    []string
	stdin    []string
	failRuns map[string]int // command prefix → failures left
	out      strings.Builder
}

func (f *fakeSetup) flow(input, role string, fromInstall bool, passwords ...string) *setupFlow {
	return &setupFlow{
		in: bufio.NewReader(strings.NewReader(input)), out: &f.out,
		self: `D:\Downloads\sevjala.exe`, dir: `C:\Program Files\Sevjala`, target: `C:\Program Files\Sevjala\sevjala.exe`,
		role: role, fromInstall: fromInstall,
		run: func(stdin string, args ...string) error {
			c := strings.Join(args, " ")
			f.calls = append(f.calls, "run "+c)
			f.stdin = append(f.stdin, stdin)
			for prefix, n := range f.failRuns {
				if n > 0 && strings.HasPrefix(c, prefix) {
					f.failRuns[prefix]--
					return errors.New("exit status 1")
				}
			}
			return nil
		},
		copyExe:   func(src, dst string) error { f.calls = append(f.calls, "copy "+src+" → "+dst); return nil },
		removeExe: func(p string) error { f.calls = append(f.calls, "remove "+p); return nil },
		onPath: func(add bool, dir string) error {
			f.calls = append(f.calls, map[bool]string{true: "path add ", false: "path remove "}[add]+dir)
			return nil
		},
		password: func() (string, error) {
			if len(passwords) == 0 {
				return "", errors.New("no password left")
			}
			p := passwords[0]
			passwords = passwords[1:]
			return p, nil
		},
	}
}

func TestSetupFreshNode(t *testing.T) {
	var f fakeSetup
	if err := f.flow("x\n2\n", "", false).wizard(); err != nil {
		t.Fatal(err)
	}
	want := []string{
		`copy D:\Downloads\sevjala.exe → C:\Program Files\Sevjala\sevjala.exe`,
		"run service install --role node",
		`path add C:\Program Files\Sevjala`,
		"run code",
	}
	if !slices.Equal(f.calls, want) {
		t.Errorf("calls\n got %q\nwant %q", f.calls, want)
	}
	if strings.Count(f.out.String(), "Choose 1 or 2") != 2 {
		t.Errorf("an invalid choice must ask again:\n%s", f.out.String())
	}
}

func TestSetupFreshController(t *testing.T) {
	var f fakeSetup
	f.failRuns = map[string]int{"db set": 1}
	in := "1\nshop\nshop,crm\n" + "sevjala pair 3d1d67b9 --code 941091 --fp ba6127dd4eb05989\n" + "\n"
	if err := f.flow(in, "", false, "wrong", "s3cret").wizard(); err != nil {
		t.Fatal(err)
	}
	want := []string{
		`copy D:\Downloads\sevjala.exe → C:\Program Files\Sevjala\sevjala.exe`,
		"run service install --role controller",
		`path add C:\Program Files\Sevjala`,
		"run db detect",
		"run db set --db shop --password-stdin",
		"run db set --db shop,crm --password-stdin",
		"run discover",
		"run pair 3d1d67b9 --code 941091 --fp ba6127dd4eb05989",
	}
	if !slices.Equal(f.calls, want) {
		t.Errorf("calls\n got %q\nwant %q", f.calls, want)
	}
	if f.stdin[2] != "wrong\n" || f.stdin[3] != "s3cret\n" {
		t.Errorf("password stdin %q", f.stdin)
	}
	if strings.Contains(f.out.String(), "s3cret") {
		t.Error("password printed")
	}
}

func TestSetupControllerWithoutPasswordOrDatabase(t *testing.T) {
	var f fakeSetup
	if err := f.flow("1\nshop\n\n", "", false, "").wizard(); err != nil {
		t.Fatal(err)
	}
	if !slices.Contains(f.calls, "run db set --db shop --no-password") {
		t.Errorf("calls %q", f.calls)
	}
	f = fakeSetup{}
	if err := f.flow("1\n\n\n", "", false).wizard(); err != nil {
		t.Fatal(err)
	}
	if slices.ContainsFunc(f.calls, func(c string) bool { return strings.HasPrefix(c, "run db set") }) {
		t.Errorf("Enter must skip the database: %q", f.calls)
	}
}

func TestSetupStopsWhenInstallFails(t *testing.T) {
	var f fakeSetup
	f.failRuns = map[string]int{"service install": 1}
	if err := f.flow("2\n", "", false).wizard(); err == nil {
		t.Fatal("want an error")
	}
	if slices.Contains(f.calls, "run code") {
		t.Errorf("went on after a failed install: %q", f.calls)
	}
}

func TestSetupInstalledMenu(t *testing.T) {
	tests := []struct {
		name, input, role string
		fromInstall       bool
		want              []string
	}{
		{"update", "1\n", "node", false, []string{"run service status", "run service stop",
			`copy D:\Downloads\sevjala.exe → C:\Program Files\Sevjala\sevjala.exe`, "run service start"}},
		{"node code", "2\n", "node", false, []string{"run service status", "run code"}},
		{"controller pairs", "2\nn1 --code 1 --fp ab\n\n", "controller", false,
			[]string{"run service status", "run discover", "run pair n1 --code 1 --fp ab"}},
		{"uninstall keeps the running file", "2\n", "node", true, []string{"run service status",
			"run service uninstall", `path remove C:\Program Files\Sevjala`}},
		{"uninstall removes the installed file", "3\n", "node", false, []string{"run service status",
			"run service uninstall", `path remove C:\Program Files\Sevjala`, `remove C:\Program Files\Sevjala\sevjala.exe`}},
		{"exit", "4\n", "node", false, []string{"run service status"}},
		{"no update from the installed file", "3\n", "node", true, []string{"run service status"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var f fakeSetup
			if err := f.flow(tt.input, tt.role, tt.fromInstall).wizard(); err != nil {
				t.Fatal(err)
			}
			if !slices.Equal(f.calls, tt.want) {
				t.Errorf("calls\n got %q\nwant %q\n%s", f.calls, tt.want, f.out.String())
			}
		})
	}
}

func TestSetupEndOfInput(t *testing.T) {
	var f fakeSetup
	if err := f.flow("", "", false).wizard(); err == nil {
		t.Error("closed input must end the wizard with an error, not loop")
	}
}

func TestPathList(t *testing.T) {
	dir := `C:\Program Files\Sevjala`
	tests := []struct {
		name, path string
		add        bool
		want       string
		changed    bool
	}{
		{"add", `C:\Windows;C:\Windows\system32`, true, `C:\Windows;C:\Windows\system32;` + dir, true},
		{"add to a trailing separator", `C:\Windows;`, true, `C:\Windows;` + dir, true},
		{"already there, other case", `C:\Windows;c:\program files\sevjala\`, true, `C:\Windows;c:\program files\sevjala\`, false},
		{"remove", `C:\Windows;` + dir + `;C:\Tools`, false, `C:\Windows;C:\Tools`, true},
		{"remove missing", `C:\Windows`, false, `C:\Windows`, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, changed := pathList(tt.path, dir, tt.add)
			if got != tt.want || changed != tt.changed {
				t.Errorf("got %q %v, want %q %v", got, changed, tt.want, tt.changed)
			}
		})
	}
}

func TestInstalledRole(t *testing.T) {
	dir := t.TempDir()
	unknown := fakeService{err: errors.New("exit status 1")} // no init system to ask
	if role, err := installedRole(unknown, dir); role != "" || err != nil {
		t.Errorf("no config: %q, %v; want a fresh install", role, err)
	}
	if err := config.Write(config.Path(dir), config.Default(config.RoleNode)); err != nil {
		t.Fatal(err)
	}
	if role, err := installedRole(fakeService{err: service.ErrNotInstalled}, dir); role != "" || err != nil {
		t.Errorf("uninstalled with data kept: %q, %v", role, err)
	}
	if role, err := installedRole(fakeService{st: service.StatusRunning}, dir); role != config.RoleNode || err != nil {
		t.Errorf("installed: %q, %v", role, err)
	}
}
