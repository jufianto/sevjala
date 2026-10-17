// Package tray is 'sevjala tray': an icon whose colour is the agent's health
// verdict, with the status lines in its menu (blueprint: Components, Tray).
package tray

import (
	"errors"
	"fmt"
	"io/fs"
	"time"

	"github.com/jufianto/sevjala/inventory"
	"github.com/jufianto/sevjala/localapi"
)

const (
	Green  = "green"
	Yellow = "yellow"
	Red    = "red"
	Grey   = "grey"
)

// MaxLines keeps the menu short enough to read at a glance.
const MaxLines = 7

// Menu is what the tray shows for one status poll.
type Menu struct {
	Color     string
	Tooltip   string
	Lines     []string // greyed-out information, the verdict first
	BackupNow bool     // a controller offers "Back up now"
}

// Build turns a status poll into the menu; err is the poll's failure.
func Build(st *localapi.Status, err error, now time.Time) Menu {
	if err != nil || st == nil {
		line := "Sevjala service is not running"
		if errors.Is(err, fs.ErrPermission) {
			line = "This account may not use Sevjala"
		}
		return finish(Menu{Color: Grey, Lines: []string{line}})
	}
	m := Menu{BackupNow: st.Role == "controller"}
	h := st.Health
	if h == nil {
		m.Color, m.Lines = Yellow, []string{"The agent is older than this tray; run: sevjala update"}
		return finish(m)
	}
	m.Color = map[string]string{localapi.HealthOK: Green, localapi.HealthWarning: Yellow, localapi.HealthProblem: Red}[h.Level]
	if m.Color == "" {
		m.Color = Yellow
	}
	m.Lines = append(m.Lines, h.Headline)
	for _, d := range h.Databases {
		if d.LastGood == "" {
			m.Lines = append(m.Lines, d.Name+": no backup on a backup PC yet")
			continue
		}
		m.Lines = append(m.Lines, fmt.Sprintf("%s: %s, on %d of %d backup PCs", d.Name, localapi.When(d.LastGoodAt, now), d.Copies, d.Planned))
	}
	if st.Store != nil {
		m.Lines = append(m.Lines, inventory.HumanBytes(st.Store.FreeBytes)+" free")
	}
	for i, it := range h.Items {
		if i == 0 && it.Text == h.Headline {
			continue
		}
		m.Lines = append(m.Lines, it.Text)
	}
	return finish(m)
}

func finish(m Menu) Menu {
	if len(m.Lines) > MaxLines {
		m.Lines = m.Lines[:MaxLines]
	}
	m.Tooltip = "Sevjala: " + m.Lines[0]
	if r := []rune(m.Tooltip); len(r) > 120 {
		m.Tooltip = string(r[:119]) + "…"
	}
	return m
}
