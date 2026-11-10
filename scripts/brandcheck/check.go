package main

import (
	"encoding/json"
	"fmt"
	"io/fs"
	"path"
	"regexp"
	"slices"
	"sort"
	"strconv"
	"strings"
)

type token struct {
	Value string `json:"value"`
	Ref   string `json:"ref"`
	Role  string `json:"role"`
}

type surface struct {
	File  string `json:"file"`
	Key   string `json:"key"`
	Token string `json:"token"`
}

type unchecked struct {
	Path   string `json:"path"`
	Reason string `json:"reason"`
}

type palette struct {
	Colors    map[string]token `json:"colors"`
	Surfaces  []surface        `json:"surfaces"`
	Unchecked []unchecked      `json:"unchecked"`
}

// use is one colour found in a file: key names it the way tokens.json maps it.
type use struct {
	file  string
	line  int
	key   string
	value string
}

var (
	hexRe     = regexp.MustCompile(`#[0-9A-Fa-f]{3,8}\b`)
	longHexRe = regexp.MustCompile(`#(?:[0-9A-Fa-f]{6}|[0-9A-Fa-f]{8})\b`)
	funcRe    = regexp.MustCompile(`\b(rgba?|hsla?)\(([^)]*)\)`)
	goNamedRe = regexp.MustCompile(`(\w+):\s*\{\s*0x([0-9A-Fa-f]{2}),\s*0x([0-9A-Fa-f]{2}),\s*0x([0-9A-Fa-f]{2}),\s*0x([0-9A-Fa-f]{2})\s*\}`)
	goColorRe = regexp.MustCompile(`color\.N?RGBA\{\s*0x([0-9A-Fa-f]{2}),\s*0x([0-9A-Fa-f]{2}),\s*0x([0-9A-Fa-f]{2}),\s*0x([0-9A-Fa-f]{2})\s*\}`)
	styleRe   = regexp.MustCompile(`(?s)<style[^>]*>(.*?)</style>`)
	darkRe    = regexp.MustCompile(`prefers-color-scheme\s*:\s*dark`)
	wordRe    = regexp.MustCompile(`[A-Za-z]+`)
	varRe     = regexp.MustCompile(`var\(\s*--[\w-]+\s*\)`)
)

// Named CSS colours; transparent, currentColor and inherit are not colours of their own.
var cssNames = func() map[string]bool {
	m := map[string]bool{}
	for n := range strings.FieldsSeq(`aliceblue antiquewhite aqua aquamarine azure beige bisque black
		blanchedalmond blue blueviolet brown burlywood cadetblue chartreuse chocolate coral cornflowerblue
		cornsilk crimson cyan darkblue darkcyan darkgoldenrod darkgray darkgreen darkgrey darkkhaki
		darkmagenta darkolivegreen darkorange darkorchid darkred darksalmon darkseagreen darkslateblue
		darkslategray darkslategrey darkturquoise darkviolet deeppink deepskyblue dimgray dimgrey dodgerblue
		firebrick floralwhite forestgreen fuchsia gainsboro ghostwhite gold goldenrod gray green greenyellow
		grey honeydew hotpink indianred indigo ivory khaki lavender lavenderblush lawngreen lemonchiffon
		lightblue lightcoral lightcyan lightgoldenrodyellow lightgray lightgreen lightgrey lightpink
		lightsalmon lightseagreen lightskyblue lightslategray lightslategrey lightsteelblue lightyellow lime
		limegreen linen magenta maroon mediumaquamarine mediumblue mediumorchid mediumpurple mediumseagreen
		mediumslateblue mediumspringgreen mediumturquoise mediumvioletred midnightblue mintcream mistyrose
		moccasin navajowhite navy oldlace olive olivedrab orange orangered orchid palegoldenrod palegreen
		paleturquoise palevioletred papayawhip peachpuff peru pink plum powderblue purple rebeccapurple red
		rosybrown royalblue saddlebrown salmon sandybrown seagreen seashell sienna silver skyblue slateblue
		slategray slategrey snow springgreen steelblue tan teal thistle tomato turquoise violet wheat white
		whitesmoke yellow yellowgreen`) {
		m[n] = true
	}
	return m
}()

func loadPalette(data []byte) (palette, []string) {
	var p palette
	if err := json.Unmarshal(data, &p); err != nil {
		return p, []string{"brand/tokens.json: " + err.Error()}
	}
	var probs []string
	for name, t := range p.Colors {
		switch {
		case t.Role == "":
			probs = append(probs, fmt.Sprintf("token %s has no role", name))
		case (t.Value == "") == (t.Ref == ""):
			probs = append(probs, fmt.Sprintf("token %s needs exactly one of value and ref", name))
		case t.Value != "" && !longHexRe.MatchString(t.Value):
			probs = append(probs, fmt.Sprintf("token %s: value %q is not #RRGGBB or #RRGGBBAA", name, t.Value))
		case t.Value != "" && t.Value != strings.ToUpper(t.Value):
			probs = append(probs, fmt.Sprintf("token %s: write %q in upper case", name, t.Value))
		}
		if _, err := p.resolve(name); err != nil {
			probs = append(probs, err.Error())
		}
	}
	for _, s := range p.Surfaces {
		if _, ok := p.Colors[s.Token]; !ok {
			probs = append(probs, fmt.Sprintf("surface %s %s: unknown token %s", s.File, s.Key, s.Token))
		}
	}
	for _, u := range p.Unchecked {
		if u.Reason == "" {
			probs = append(probs, fmt.Sprintf("unchecked %s has no reason", u.Path))
		}
	}
	return p, probs
}

// resolve follows refs to a token's colour, normalized like every scanned colour.
func (p palette) resolve(name string) (string, error) {
	seen := map[string]bool{}
	for {
		t, ok := p.Colors[name]
		switch {
		case !ok:
			return "", fmt.Errorf("unknown token %s", name)
		case seen[name]:
			return "", fmt.Errorf("token %s: ref loop", name)
		case t.Ref == "":
			return normHex(t.Value), nil
		}
		seen[name] = true
		name = t.Ref
	}
}

func (p palette) skipped(file string) bool {
	for _, u := range p.Unchecked {
		if file == u.Path || strings.HasSuffix(u.Path, "/") && strings.HasPrefix(file, u.Path) {
			return true
		}
	}
	return false
}

// check scans files and reports drift, colours with no token and stale mappings.
func check(fsys fs.FS, files []string, p palette) (probs []string, uses int) {
	values := map[string]bool{}
	for name := range p.Colors {
		if v, err := p.resolve(name); err == nil {
			values[v] = true
		}
	}
	used := make([]bool, len(p.Surfaces))
	for _, f := range files {
		if p.skipped(f) {
			continue
		}
		ext := path.Ext(f)
		if !scanned[ext] {
			continue
		}
		data, err := fs.ReadFile(fsys, f)
		if err != nil {
			probs = append(probs, err.Error())
			continue
		}
		found, bad := scan(f, string(data))
		probs = append(probs, bad...)
		for _, u := range found {
			uses++
			if ext == ".md" {
				if !values[u.value] {
					probs = append(probs, fmt.Sprintf("%s:%d: %s is not a token value", u.file, u.line, u.value))
				}
				continue
			}
			i := p.match(u)
			if i < 0 {
				probs = append(probs, fmt.Sprintf("%s:%d: colour %s (%s) has no token: map it in brand/tokens.json", u.file, u.line, u.value, u.key))
				continue
			}
			used[i] = true
			if want, _ := p.resolve(p.Surfaces[i].Token); want != u.value {
				probs = append(probs, fmt.Sprintf("%s:%d: %s is %s, token %s is %s", u.file, u.line, u.key, u.value, p.Surfaces[i].Token, want))
			}
		}
	}
	for i, s := range p.Surfaces {
		if !used[i] {
			probs = append(probs, fmt.Sprintf("stale mapping: %s %s (token %s) is not in the code", s.File, s.Key, s.Token))
		}
	}
	sort.Strings(probs)
	return slices.Compact(probs), uses
}

func (p palette) match(u use) int {
	for i, s := range p.Surfaces {
		if ok, _ := path.Match(s.File, u.file); !ok {
			continue
		}
		key := s.Key
		if strings.HasPrefix(key, "#") {
			key = normHex(key)
		}
		if key == u.key {
			return i
		}
	}
	return -1
}

var scanned = map[string]bool{
	".css": true, ".svelte": true, ".html": true, ".js": true, ".mjs": true, ".svg": true,
	".go": true, ".py": true, ".php": true, ".md": true,
}

func scan(file, src string) ([]use, []string) {
	switch path.Ext(file) {
	case ".css":
		return scanCSS(file, src, 0)
	case ".svelte", ".html", ".php":
		var found []use
		var probs []string
		rest := []byte(src)
		for _, m := range styleRe.FindAllStringSubmatchIndex(src, -1) {
			f, p := scanCSS(file, src[m[2]:m[3]], lineAt(src, m[2])-1)
			found, probs = append(found, f...), append(probs, p...)
			for i := m[2]; i < m[3]; i++ {
				if rest[i] != '\n' {
					rest[i] = ' '
				}
			}
		}
		f, p := scanLiterals(file, string(rest), hexRe, true)
		return append(found, f...), append(probs, p...)
	case ".js", ".mjs", ".svg":
		return scanLiterals(file, src, hexRe, true)
	case ".go":
		return scanGo(file, src)
	default: // .py, .md
		return scanLiterals(file, src, longHexRe, false)
	}
}

// scanLiterals finds hex colours (and rgb()/hsl() when funcs) keyed by their own value.
func scanLiterals(file, src string, re *regexp.Regexp, funcs bool) ([]use, []string) {
	var found []use
	var probs []string
	for _, m := range re.FindAllStringIndex(src, -1) {
		lit := src[m[0]:m[1]]
		if m[0] > 0 && src[m[0]-1] == '&' || !validHexLen(lit) {
			continue
		}
		v := normHex(lit)
		found = append(found, use{file, lineAt(src, m[0]), v, v})
	}
	if funcs {
		for _, m := range funcRe.FindAllStringSubmatchIndex(src, -1) {
			v, err := normFunc(src[m[2]:m[3]], src[m[4]:m[5]])
			if err != nil {
				probs = append(probs, fmt.Sprintf("%s:%d: %v", file, lineAt(src, m[0]), err))
				continue
			}
			found = append(found, use{file, lineAt(src, m[0]), v, v})
		}
	}
	return found, probs
}

func scanGo(file, src string) ([]use, []string) {
	var found []use
	for _, m := range goNamedRe.FindAllStringSubmatchIndex(src, -1) {
		v := "#" + src[m[4]:m[5]] + src[m[6]:m[7]] + src[m[8]:m[9]] + src[m[10]:m[11]]
		found = append(found, use{file, lineAt(src, m[0]), src[m[2]:m[3]], normHex(v)})
	}
	for _, m := range goColorRe.FindAllStringSubmatchIndex(src, -1) {
		v := normHex("#" + src[m[2]:m[3]] + src[m[4]:m[5]] + src[m[6]:m[7]] + src[m[8]:m[9]])
		found = append(found, use{file, lineAt(src, m[0]), v, v})
	}
	f, p := scanLiterals(file, src, longHexRe, false)
	return append(found, f...), p
}

// scanCSS keys a custom property by its name (with @dark inside a dark colour-scheme
// block) and any other colour by its value; named colours are always reported.
func scanCSS(file, css string, baseLine int) ([]use, []string) {
	var found []use
	var probs []string
	var stack []string
	start := 0
	for i := 0; i < len(css); i++ {
		switch css[i] {
		case '{':
			stack = append(stack, css[start:i])
			start = i + 1
		case '}', ';':
			decl := css[start:i]
			name, value, ok := strings.Cut(decl, ":")
			if ok && len(stack) > 0 {
				name = strings.TrimSpace(name)
				dark := false
				for _, h := range stack {
					dark = dark || darkRe.MatchString(h)
				}
				suffix := ""
				if dark {
					suffix = "@dark"
				}
				line := baseLine + lineAt(css, start+len(decl)-len(strings.TrimLeft(decl, " \t\n")))
				f, p := scanLiterals(file, value, hexRe, true)
				for _, u := range f {
					u.line = line
					if strings.HasPrefix(name, "--") {
						u.key = name + suffix
					} else {
						u.key += suffix
					}
					found = append(found, u)
				}
				probs = append(probs, p...)
				if colourProp(name) {
					for _, w := range wordRe.FindAllString(varRe.ReplaceAllString(value, ""), -1) {
						if cssNames[strings.ToLower(w)] {
							probs = append(probs, fmt.Sprintf("%s:%d: named colour %q in %s: use var(--…) or a hex mapped in brand/tokens.json", file, line, w, name))
						}
					}
				}
			}
			if css[i] == '}' && len(stack) > 0 {
				stack = stack[:len(stack)-1]
			}
			start = i + 1
		}
	}
	return found, probs
}

func colourProp(name string) bool {
	if strings.HasPrefix(name, "--") {
		return true
	}
	for _, p := range []string{"color", "background", "border", "outline", "fill", "stroke", "box-shadow",
		"text-shadow", "caret-color", "accent-color", "text-decoration", "column-rule"} {
		if strings.HasPrefix(name, p) {
			return true
		}
	}
	return false
}

func validHexLen(lit string) bool {
	switch len(lit) - 1 {
	case 3, 4, 6, 8:
		return true
	}
	return false
}

// normHex writes a hex colour as #RRGGBB, or #RRGGBBAA when it is not opaque.
func normHex(lit string) string {
	h := strings.ToUpper(strings.TrimPrefix(lit, "#"))
	if len(h) == 3 || len(h) == 4 {
		var b strings.Builder
		for _, c := range h {
			b.WriteRune(c)
			b.WriteRune(c)
		}
		h = b.String()
	}
	if len(h) == 8 && strings.HasSuffix(h, "FF") {
		h = h[:6]
	}
	return "#" + h
}

func normFunc(kind, args string) (string, error) {
	if strings.HasPrefix(kind, "hsl") {
		return "", fmt.Errorf("%s() colour: write it as hex or rgb() so brand/tokens.json can check it", kind)
	}
	parts := strings.FieldsFunc(args, func(r rune) bool { return r == ',' || r == '/' || r == ' ' || r == '\t' })
	if len(parts) != 3 && len(parts) != 4 {
		return "", fmt.Errorf("%s(%s): cannot read this colour", kind, args)
	}
	var b [4]int
	b[3] = 255
	for i, s := range parts {
		pct := strings.HasSuffix(s, "%")
		f, err := strconv.ParseFloat(strings.TrimSuffix(s, "%"), 64)
		if err != nil {
			return "", fmt.Errorf("%s(%s): cannot read this colour", kind, args)
		}
		switch {
		case i == 3 && pct:
			f = f / 100 * 255
		case i == 3:
			f *= 255
		case pct:
			f = f / 100 * 255
		}
		b[i] = int(f + 0.5)
	}
	return normHex(fmt.Sprintf("#%02X%02X%02X%02X", b[0], b[1], b[2], b[3])), nil
}

func lineAt(s string, i int) int {
	return strings.Count(s[:i], "\n") + 1
}
