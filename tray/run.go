//go:build windows || linux || (darwin && cgo && tray)

package tray

import (
	"context"
	"runtime"
	"time"

	"fyne.io/systray"

	"github.com/jufianto/sevjala/localapi"
)

const pollEvery = 15 * time.Second

// Available is false in builds without a tray (macOS without cgo).
const Available = true

// Run shows the icon until Quit. exe is the running binary: when an update
// replaces it, the tray starts the new one and ends.
func Run(cl *localapi.Client, exe string, autostart func() bool) error {
	release, first, err := singleInstance()
	if err != nil {
		return err
	}
	if !first {
		return ErrAlreadyRunning
	}
	hideConsole()
	t := &tray{cl: cl, exe: exe, watch: watchBinary(exe), auto: watchAutostart(autostart)}
	systray.Run(t.ready, func() {})
	release()
	if t.restart {
		return spawn(exe, "tray")
	}
	return nil
}

type tray struct {
	cl      *localapi.Client
	exe     string
	watch   binaryWatch
	auto    autostartWatch
	restart bool

	lines                            [MaxLines]*systray.MenuItem
	backup, dash, ctrl, status, quit *systray.MenuItem
	jobs                             []string // backups started here and still dumping
	note                             string   // shown once, above the status lines
}

func (t *tray) ready() {
	systray.SetIcon(icon(Grey))
	systray.SetTooltip("Sevjala")
	for i := range t.lines {
		t.lines[i] = systray.AddMenuItem("", "")
		t.lines[i].Disable()
		t.lines[i].Hide()
	}
	systray.AddSeparator()
	t.backup = systray.AddMenuItem("Back up now", "Back up every database now")
	t.dash = systray.AddMenuItem("Open dashboard", "Show the dashboard in your browser")
	t.ctrl = systray.AddMenuItem("Open controller dashboard", "Show the controller's dashboard; this PC is its manager")
	t.ctrl.Hide()
	t.status = systray.AddMenuItem("Open status", "Show 'sevjala status' in a window")
	if !canOpenStatus() {
		t.status.Hide()
	}
	systray.AddSeparator()
	t.quit = systray.AddMenuItem("Quit tray", "Hide this icon until the next login")
	go t.loop()
}

// loop is the only goroutine that touches t after ready.
func (t *tray) loop() {
	tick := time.NewTicker(pollEvery)
	defer tick.Stop()
	t.refresh()
	for {
		select {
		case <-tick.C:
			t.refresh()
		case <-t.backup.ClickedCh:
			t.backupNow()
			t.refresh()
		case <-t.dash.ClickedCh:
			if err := spawn(t.exe, "ui"); err != nil {
				t.note = "Cannot open the dashboard: " + err.Error()
				t.refresh()
			}
		case <-t.ctrl.ClickedCh:
			if err := spawn(t.exe, "ui", "--controller"); err != nil {
				t.note = "Cannot open the controller's dashboard: " + err.Error()
				t.refresh()
			}
		case <-t.status.ClickedCh:
			if err := openStatus(); err != nil {
				t.note = "Cannot open the status window: " + err.Error()
				t.refresh()
			}
		case <-t.quit.ClickedCh:
			systray.Quit()
			return
		}
	}
}

func (t *tray) refresh() {
	if t.auto.removed() {
		systray.Quit()
		return
	}
	if t.watch.replaced() {
		t.restart = true
		systray.Quit()
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	st, err := t.cl.Status(ctx)
	var m Menu
	if err != nil {
		m = Build(nil, err, time.Now())
	} else {
		m = Build(&st, nil, time.Now())
	}
	busy := t.backingUp(ctx)
	if m.Manage {
		t.ctrl.Show()
	} else {
		t.ctrl.Hide()
	}
	systray.SetIcon(icon(m.Color))
	systray.SetTooltip(m.Tooltip)
	lines := m.Lines
	if t.note != "" {
		lines = append([]string{t.note}, lines...)
		t.note = ""
	}
	for i, it := range t.lines {
		if i < len(lines) {
			it.SetTitle(lines[i])
			it.Show()
		} else {
			it.Hide()
		}
	}
	switch {
	case !m.BackupNow:
		t.backup.Hide()
	case busy:
		t.backup.SetTitle("Backing up… (see Open status)")
		t.backup.Disable()
		t.backup.Show()
	default:
		t.backup.SetTitle("Back up now")
		t.backup.Enable()
		t.backup.Show()
	}
}

func (t *tray) backupNow() {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	jobs, err := t.cl.BackupNow(ctx, "", nil)
	if err != nil {
		t.note = "Back up now failed: " + err.Error()
		return
	}
	for _, j := range jobs {
		t.jobs = append(t.jobs, j.BackupID)
	}
}

func (t *tray) backingUp(ctx context.Context) bool {
	keep := t.jobs[:0]
	for _, id := range t.jobs {
		if b, err := t.cl.Backup(ctx, id); err == nil && b.State == localapi.BackupDumping {
			keep = append(keep, id)
		}
	}
	t.jobs = keep
	return len(keep) > 0
}

func icon(c string) []byte {
	if runtime.GOOS == "windows" {
		return ICO(c)
	}
	return PNG(c)
}

// Dashboard opens exe's dashboard in the browser.
func Dashboard(exe string) error { return spawn(exe, "ui") }

// Start runs exe's tray on its own, as the calling user.
func Start(exe string) error { return spawn(exe, "tray") }
