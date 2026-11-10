package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"testing/fstest"
)

// repo loads the real checkout into memory so a test can mutate one file.
func repo(t *testing.T) (fstest.MapFS, []string, palette) {
	t.Helper()
	root, files, err := repoFiles()
	if err != nil {
		t.Skip(err)
	}
	m := fstest.MapFS{}
	for _, f := range files {
		data, err := os.ReadFile(filepath.Join(root, f))
		if err != nil {
			t.Fatal(err)
		}
		m[f] = &fstest.MapFile{Data: data}
	}
	p, probs := loadPalette(m["brand/tokens.json"].Data)
	if len(probs) > 0 {
		t.Fatal(probs)
	}
	return m, files, p
}

func edit(t *testing.T, m fstest.MapFS, file, old, new string) {
	t.Helper()
	s := string(m[file].Data)
	if !strings.Contains(s, old) {
		t.Fatalf("%s does not contain %q", file, old)
	}
	m[file] = &fstest.MapFile{Data: []byte(strings.Replace(s, old, new, 1))}
}

func TestTheRepositoryMatchesItsTokens(t *testing.T) {
	m, files, p := repo(t)
	if probs, uses := check(m, files, p); len(probs) > 0 || uses == 0 {
		t.Fatalf("%d uses, problems:\n%s", uses, strings.Join(probs, "\n"))
	}
}

func TestMutationsFail(t *testing.T) {
	tests := []struct {
		name, file, old, new, want string
	}{
		{"changed light value", "ui/src/app.css", "--accent: #2457c5;", "--accent: #2457c6;", "--accent is #2457C6, token ui.light.accent is #2457C5"},
		{"changed dark-theme text", "ui/src/app.css", "--text: #e6e9ef;", "--text: #e6e9f0;", "--text@dark is #E6E9F0, token ui.dark.text is #E6E9EF"},
		{"unmapped new colour in a component", "ui/src/Dashboard.svelte", "<style>", "<style>\n  .new { color: #123456; }", "colour #123456 (#123456) has no token"},
		{"unmapped new custom property", "ui/src/app.css", "--accent: #2457c5;", "--accent: #2457c5;\n  --brand-new: #abcdef;", "#ABCDEF (--brand-new) has no token"},
		{"named colour", "ui/src/Verdict.svelte", "<style>", "<style>\n  .x { border-color: tomato; }", `named colour "tomato"`},
		{"rgb() colour with no token", "ui/src/Confirm.svelte", "rgb(0 0 0 / 0.45)", "rgb(0 0 0 / 0.5)", "#00000080"},
		{"changed tray dot", "tray/icon.go", "0x2e, 0xa0, 0x43", "0x2e, 0xa0, 0x44", "Green is #2EA044, token tray.green is #2EA043"},
		{"stale mapping", "ui/src/Confirm.svelte", "background: rgb(0 0 0 / 0.45);", "", "stale mapping: ui/src/Confirm.svelte #00000073"},
		{"hex in docs that is no token", "docs/new.md", "", "Accent #2457C6.", "#2457C6 is not a token value"},
		{"named colour after a comment", "ui/src/Verdict.svelte", "<style>", "<style>\n  .x { /* a; note: here */ color: tomato; }", `named colour "tomato"`},
		{"custom property after a comment", "ui/src/app.css", "--accent: #2457c5;", "/* the accent */ --accent: #2457c6;", "--accent is #2457C6, token ui.light.accent is #2457C5"},
		{"oklch() colour", "ui/src/Dashboard.svelte", "<style>", "<style>\n  .x { color: oklch(0.5 0.1 200); }", "oklch() colour"},
		{"stale unchecked entry", "brand/tokens.json", `"path": "testbed/"`, `"path": "testbed-gone/"`, "stale unchecked entry: testbed-gone/"},
		{"token value not a bare hex", "brand/tokens.json", `"value": "#8A8A8A"`, `"value": "x #8A8A8A"`, "token tray.grey: value"},
		{"token with no role", "brand/tokens.json", `"role": "Tray: unknown status dot (agent not reachable)"`, `"role": ""`, "token tray.grey has no role"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			m, files, _ := repo(t)
			if tt.old == "" {
				m[tt.file] = &fstest.MapFile{Data: []byte(tt.new)}
				files = append(files, tt.file)
			} else {
				edit(t, m, tt.file, tt.old, tt.new)
			}
			p, probs := loadPalette(m["brand/tokens.json"].Data)
			found, _ := check(m, files, p)
			probs = append(probs, found...)
			if !strings.Contains(strings.Join(probs, "\n"), tt.want) {
				t.Errorf("want a problem containing %q, got:\n%s", tt.want, strings.Join(probs, "\n"))
			}
		})
	}
}

func TestUncheckedPathsAreSkipped(t *testing.T) {
	m, files, p := repo(t)
	m["testbed/new.css"] = &fstest.MapFile{Data: []byte(":root { --x: #123456; }")}
	if probs, _ := check(m, append(files, "testbed/new.css"), p); len(probs) > 0 {
		t.Fatal(probs)
	}
}

func TestNormalize(t *testing.T) {
	for in, want := range map[string]string{
		"#fff": "#FFFFFF", "#ffff": "#FFFFFF", "#14233b": "#14233B", "#14233bff": "#14233B", "#0000": "#00000000",
	} {
		if got := normHex(in); got != want {
			t.Errorf("normHex(%s) = %s, want %s", in, got, want)
		}
	}
	for args, want := range map[string]string{
		"0 0 0 / 0.45": "#00000073", "255, 255, 255": "#FFFFFF", "20,35,59,1": "#14233B", "0 0 0 / 50%": "#00000080",
	} {
		if got, err := normFunc("rgb", args); err != nil || got != want {
			t.Errorf("rgb(%s) = %s %v, want %s", args, got, err, want)
		}
	}
	for _, kind := range []string{"hsl", "oklch", "color"} {
		if _, err := normFunc(kind, "0 0% 0%"); err == nil {
			t.Errorf("%s() is accepted", kind)
		}
	}
}
