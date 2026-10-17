package cmd

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/spf13/cobra"
	"golang.org/x/term"

	"github.com/jufianto/sevjala/config"
	"github.com/jufianto/sevjala/svc"
)

func newSetupCmd(g *globals) *cobra.Command {
	return &cobra.Command{
		Use:   "setup",
		Short: "Install, update or remove Sevjala step by step (what a double-click runs on Windows)",
		Long: `Asks whether this PC runs the database (controller) or keeps backup copies (node),
installs the service, and walks you through the database and pairing. Run it again
later to update to a newer sevjala.exe, pair another PC, or uninstall.`,
		Example: `  sevjala setup                 (or double-click sevjala.exe on Windows)
  sudo ./sevjala setup          (macOS/Linux)`,
		Args: cobra.NoArgs,
		RunE: func(c *cobra.Command, _ []string) error {
			if !svc.IsAdmin() {
				return &needsAdmin{why: "installing Sevjala needs administrator rights"}
			}
			self, err := os.Executable()
			if err != nil {
				return fail(err)
			}
			f, err := newSetupFlow(g, c, self)
			if err != nil {
				return fail(err)
			}
			if err := f.wizard(); err != nil {
				return fail(err)
			}
			return nil
		},
	}
}

// newSetupFlow installs from self, the file that replaces the installed one.
func newSetupFlow(g *globals, c *cobra.Command, self string) (*setupFlow, error) {
	dataDir, err := g.dataDir()
	if err != nil {
		return nil, err
	}
	s, err := svc.New(dataDir, &svc.Agent{})
	if err != nil {
		return nil, err
	}
	role, err := installedRole(s, dataDir)
	if err != nil {
		return nil, err
	}
	dir := installDir()
	target := filepath.Join(dir, exeName)
	return &setupFlow{in: bufio.NewReader(c.InOrStdin()), out: c.OutOrStdout(), self: self, dir: dir, target: target,
		role: role, fromInstall: sameFile(self, target), copyExe: copyFile, restoreExe: restoreExe, removeExe: os.Remove, onPath: setPath,
		run: func(stdin string, args ...string) error {
			if g.dataDirFlag != "" {
				args = append([]string{"--data-dir", g.dataDirFlag}, args...)
			}
			cmd := exec.Command(target, args...)
			cmd.Stdin, cmd.Stdout, cmd.Stderr = strings.NewReader(stdin), c.OutOrStdout(), c.ErrOrStderr()
			var exit *exec.ExitError
			if err := cmd.Run(); errors.As(err, &exit) {
				return errShown
			} else if err != nil {
				return err
			}
			return nil
		},
		password: func() (string, error) {
			b, err := term.ReadPassword(int(os.Stdin.Fd()))
			fmt.Fprintln(c.OutOrStdout())
			return string(b), err
		},
	}, nil
}

// errShown: the step ran and already printed why it failed.
var errShown = errors.New("see the message above")

// installedRole is the role of the installed service, or "" when none is
// installed. No config means a fresh install even when the service manager
// cannot say (a container without init); service install then decides.
func installedRole(s serviceStatuser, dataDir string) (string, error) {
	if checkNotInstalled(s) == nil {
		return "", nil
	}
	cfg, err := config.Load(config.Path(dataDir))
	switch {
	case errors.Is(err, fs.ErrNotExist):
		return "", nil
	case err != nil:
		return "", fmt.Errorf("the service is installed but its config cannot be read: %w", err)
	}
	return cfg.Role, nil
}

// setupFlow is the wizard; every side effect goes through a field so the
// steps can be tested without installing anything.
type setupFlow struct {
	in          *bufio.Reader
	out         io.Writer
	self        string                                   // the running executable
	dir, target string                                   // install folder and the executable in it
	role        string                                   // installed role, "" when not installed
	fromInstall bool                                     // self is target
	run         func(stdin string, args ...string) error // runs target
	copyExe     func(src, dst string) error              // keeps the replaced file as dst.old
	restoreExe  func(path string) error                  // puts path.old back
	removeExe   func(path string) error
	onPath      func(add bool, dir string) error
	password    func() (string, error)
}

func (s *setupFlow) wizard() error {
	fmt.Fprintf(s.out, "Sevjala setup %s\n\n", version)
	if s.role == "" {
		return s.install()
	}
	return s.menu()
}

func (s *setupFlow) install() error {
	fmt.Fprintln(s.out, "  1) This PC runs the database (XAMPP/MySQL)  → controller")
	fmt.Fprintln(s.out, "  2) This PC keeps backup copies              → node")
	n, err := s.choose(2)
	if err != nil {
		return err
	}
	role := []string{config.RoleController, config.RoleNode}[n-1]
	if !s.fromInstall {
		if err := s.copyExe(s.self, s.target); err != nil {
			return fmt.Errorf("copy to %s: %w", s.target, err)
		}
		fmt.Fprintf(s.out, "copied to %s\n", s.target)
	}
	if err := s.run("", "service", "install", "--role", role); err != nil {
		return fmt.Errorf("service install: %w", err)
	}
	if err := s.onPath(true, s.dir); err != nil {
		fmt.Fprintf(s.out, "could not add %s to PATH (%v); run sevjala from that folder\n", s.dir, err)
	} else {
		fmt.Fprintf(s.out, "%s is on the PATH: in a new Administrator prompt, type sevjala\n", s.dir)
	}
	fmt.Fprintln(s.out)
	if role == config.RoleNode {
		return s.showCode()
	}
	if err := s.run("", "db", "detect"); err != nil {
		fmt.Fprintf(s.out, "could not detect the database (%v); set it later with 'sevjala db detect' and 'sevjala db set'\n", err)
	} else if err := s.chooseDatabase(); err != nil {
		return err
	}
	return s.pairNodes()
}

func (s *setupFlow) chooseDatabase() error {
	for {
		dbs, err := s.ask("Database(s) to back up, comma separated (Enter to skip): ")
		if err != nil || dbs == "" {
			return err
		}
		fmt.Fprint(s.out, "MySQL password (Enter if none): ")
		pw, err := s.password()
		if err != nil {
			return fmt.Errorf("read the password: %w", err)
		}
		stdin, flag := pw+"\n", "--password-stdin"
		if pw == "" {
			stdin, flag = "", "--no-password"
		}
		if err := s.run(stdin, "db", "set", "--db", dbs, flag); err == nil {
			return nil
		}
		fmt.Fprintln(s.out, "not saved; check the name and password and try again")
	}
}

func (s *setupFlow) showCode() error {
	if err := s.run("", "code"); err != nil {
		return fmt.Errorf("pairing code: %w", err)
	}
	fmt.Fprintln(s.out, "\nOn the controller, run this setup again and choose \"Pair a node\", or type the pair line above.")
	return nil
}

func (s *setupFlow) pairNodes() error {
	fmt.Fprintln(s.out, "\nPair the backup PCs: run this setup on each one, choose 2, and type the line it shows here.")
	if err := s.run("", "discover"); err != nil {
		fmt.Fprintf(s.out, "discovery failed (%v); you can still pair a node by its address with 'sevjala agents add'\n", err)
	}
	for {
		line, err := s.ask("Pair line from a node (sevjala pair …), or Enter to finish: ")
		if err != nil || line == "" {
			return err
		}
		args := strings.Fields(line)
		for len(args) > 0 && (args[0] == "sevjala" || args[0] == "pair") {
			args = args[1:]
		}
		if err := s.run("", append([]string{"pair"}, args...)...); err != nil {
			fmt.Fprintln(s.out, "not paired; check the code (valid 10 minutes) and fingerprint")
		}
	}
}

func (s *setupFlow) menu() error {
	if err := s.run("", "service", "status"); err != nil && !errors.Is(err, errShown) {
		fmt.Fprintf(s.out, "(status: %v)\n", err)
	}
	type item struct {
		label string
		do    func() error
	}
	var items []item
	if !s.fromInstall {
		items = append(items, item{"Update the installed copy with this file (" + version + ")", s.update})
	}
	if s.role == config.RoleNode {
		items = append(items, item{"Show a pairing code", s.showCode})
	} else {
		items = append(items, item{"Pair a node", s.pairNodes})
	}
	items = append(items, item{"Uninstall (backups and settings are kept)", s.uninstall}, item{"Exit", func() error { return nil }})
	fmt.Fprintf(s.out, "\nInstalled as %s in %s\n", s.role, s.dir)
	for i, it := range items {
		fmt.Fprintf(s.out, "  %d) %s\n", i+1, it.label)
	}
	n, err := s.choose(len(items))
	if err != nil {
		return err
	}
	return items[n-1].do()
}

func (s *setupFlow) update() error {
	if err := s.run("", "service", "stop"); err != nil {
		return fmt.Errorf("service stop: %w", err)
	}
	if err := s.copyExe(s.self, s.target); err != nil {
		return fmt.Errorf("copy to %s: %w (the old version is still installed; run 'sevjala service start')", s.target, err)
	}
	fmt.Fprintf(s.out, "updated %s\n", s.target)
	if err := s.run("", "service", "start"); err == nil {
		return nil
	}
	fmt.Fprintln(s.out, "the new version did not start; putting the previous one back")
	if err := s.restoreExe(s.target); err != nil {
		return fmt.Errorf("the previous version could not be put back (%w); it is %s.old", err, s.target)
	}
	if err := s.run("", "service", "start"); err != nil {
		return fmt.Errorf("service start: %w", err)
	}
	return errors.New("the new version did not start; the previous version is running again")
}

func (s *setupFlow) uninstall() error {
	if err := s.run("", "service", "uninstall"); err != nil {
		return fmt.Errorf("service uninstall: %w", err)
	}
	if err := s.onPath(false, s.dir); err != nil {
		fmt.Fprintf(s.out, "could not remove %s from PATH: %v\n", s.dir, err)
	}
	exes := []string{s.target, s.target + ".old"}
	if s.fromInstall {
		fmt.Fprintf(s.out, "delete %s yourself once this window is closed\n", s.target)
		exes = exes[1:]
	}
	for _, exe := range exes {
		if err := s.removeExe(exe); err != nil && !errors.Is(err, os.ErrNotExist) {
			fmt.Fprintf(s.out, "could not delete %s: %v\n", exe, err)
		}
	}
	return nil
}

// choose asks until the answer is a number from 1 to n.
func (s *setupFlow) choose(n int) (int, error) {
	prompt := fmt.Sprintf("Choose 1-%d: ", n)
	if n == 2 {
		prompt = "Choose 1 or 2: "
	}
	for {
		a, err := s.ask(prompt)
		if err != nil {
			return 0, err
		}
		var i int
		if _, err := fmt.Sscan(a, &i); err == nil && i >= 1 && i <= n {
			return i, nil
		}
	}
}

func (s *setupFlow) ask(prompt string) (string, error) {
	fmt.Fprint(s.out, prompt)
	line, err := s.in.ReadString('\n')
	if err != nil && (line == "" || !errors.Is(err, io.EOF)) {
		return "", fmt.Errorf("no answer: %w", err)
	}
	return strings.TrimSpace(line), nil
}

// pathList adds dir to, or removes it from, a ;-separated PATH value; changed
// is false when nothing had to change.
func pathList(path, dir string, add bool) (string, bool) {
	same := func(e string) bool {
		return strings.EqualFold(strings.TrimRight(e, `\/`), strings.TrimRight(dir, `\/`))
	}
	var keep []string
	found := false
	for _, e := range strings.Split(path, ";") {
		switch {
		case e == "":
		case same(e):
			found = true
			if add {
				keep = append(keep, e)
			}
		default:
			keep = append(keep, e)
		}
	}
	if found == add {
		return path, false
	}
	if add {
		keep = append(keep, dir)
	}
	return strings.Join(keep, ";"), true
}

func sameFile(a, b string) bool {
	sa, err := os.Stat(a)
	if err != nil {
		return false
	}
	sb, err := os.Stat(b)
	return err == nil && os.SameFile(sa, sb)
}

// copyFile writes dst next to itself and renames it into place.
func copyFile(src, dst string) error {
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return err
	}
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	tmp := dst + ".new"
	out, err := os.OpenFile(tmp, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o755)
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		out.Close()
		os.Remove(tmp)
		return err
	}
	if err := out.Close(); err != nil {
		os.Remove(tmp)
		return err
	}
	// A running exe cannot be replaced on Windows but can be renamed.
	old := dst + ".old"
	_ = os.Remove(old)
	if err := os.Rename(dst, old); err != nil && !errors.Is(err, fs.ErrNotExist) {
		os.Remove(tmp)
		return err
	}
	if err := os.Rename(tmp, dst); err != nil {
		_ = os.Rename(old, dst)
		os.Remove(tmp)
		return err
	}
	return nil
}

func restoreExe(path string) error { return os.Rename(path+".old", path) }
