# Sevjala brand

The identity the owner approved on 2026-11-10 ([D-108](../decisions.md)): a database fully
enclosed in a fishing net, gathered above and tied to a USB fishing rod, with the name set in
Plus Jakarta Sans Semibold. The rod is a metaphor for recoverable data; it does not mean a USB
disk is needed.

Three sources of truth, each the only one for its job:

| What | Where |
|---|---|
| Every colour, with its role, and which surface uses it under which name | [`brand/tokens.json`](../../brand/tokens.json) |
| The artwork (hand-drawn geometry), the guide PDF and the builder | [`brand/kit/`](../../brand/kit/README.md) |
| The rules, which file for which job, and how to change each | this page |

Recipes for agents, and the brand definition of done, are in the
[`sevjala-brand` skill](../../.claude/skills/sevjala-brand/SKILL.md).

## Logo forms

| Form | Files (`brand/kit/svg/`, PNG previews in `brand/kit/png/`) | Use it |
|---|---|---|
| Full mark | `sevjala-mark-full-{navy,white,black,blue}.svg` | Placed at 128 px wide or more |
| Compact mark | `sevjala-mark-compact-{…}.svg` | Placed at 64 to 127 px |
| Micro mark | `sevjala-mark-micro-{…}.svg` | Below 64 px, down to 16 px; also every UI-chrome mark |
| Horizontal lockup | `sevjala-horizontal-{navy,white,blue}.svg` | Placed at 400 px wide or more |
| Stacked lockup | `sevjala-stacked-{navy,white,blue}.svg` | Square placements, 183 px wide or more (its full mark is 70% of the width and needs 128 px) |
| Wordmark | `sevjala-wordmark-{navy,white,blue}.svg` | The name alone, as artwork |
| App tile | `sevjala-app-icon.svg` (white micro mark on a navy tile) | Program, tray and browser-tab icons |

The lettering in the SVGs is outlined: Plus Jakarta Sans Semibold with each letter 12 units
(-0.012 em) tighter than the font's own spacing and no kerning. `brand/kit/sevjala-brand-guide.pdf`
shows every form; `brand/kit/sevjala-overview.png` is the one-page summary.

## Rules

These apply to **placed artwork**: a logo, lockup or wordmark shown as an image (README, guide,
slides, a website, a printed page).

1. **Detail by size.** Full mark at 128 px wide or more, compact from 64 px, micro below that.
   At 16 px the micro mark is only an identity cue.
2. **Minimum widths.** Horizontal lockup 400 px, full mark 128 px, compact mark 64 px. The
   stacked lockup follows from its full mark: 183 px.
3. **Clear space.** Keep a quarter of the visible mark height free on every side. The files'
   own margins do not provide all of it.
4. **Contrast.** Navy (or black) on light backgrounds, white on dark. Blue only where the
   blue variant is wanted on a light background.
5. **Proportions.** Scale uniformly. No stretching, cropping, gradients, shadows, extra strands
   or thinned strokes. Keep the net around the whole database and keep the rod.

**UI chrome** (a mark beside live text inside the app) is exempt from the 400 px and 128 px
minimums: the dashboard header pairs a 32 px micro mark with the name as live text, which is not
the lockup image. Rule 1 still applies to chrome: below 64 px it is always the micro mark.

**Only the product name is the wordmark.** A label next to it (a hostname, a role, "Admin")
stays in the UI font. Running text that mentions Sevjala ("Sevjala dev, up 0m", the window
title, the old-browser fallback in `ui/index.html`, the `uiproxy` hand-off page) is text, not
the wordmark.

**Status is not brand.** Green, yellow, red and grey are operational signals. The tray and the
dashboard draw the status as a dot beside the mark, never by recolouring the mark.

## Which file for which job

| Job | File | Made by |
|---|---|---|
| Exe and Start-menu icon (Windows) | `winres/icon.png`, 256 px app tile; at release go-winres turns it into the exe's icon at 16, 32, 48, 64 and 256 px | `make brand` |
| Tray icon | `tray/brand.png`, 32 px app tile; `tray/icon.go` draws the status dot, ringed in white, in its bottom-right corner | `make brand` |
| Dashboard tab icon | `ui/public/icon.png`, 64 px app tile, linked from `ui/index.html` | `make brand` |
| Dashboard header mark | `ui/src/lib/mark.svg`, the micro mark painted in `currentColor`, inlined by `App.svelte` | `make brand` |
| Dashboard wordmark font | `ui/src/fonts/plus-jakarta-sans-600-wordmark.woff2` (only the letters S, e, v, j, a, l); its licence `ui/public/fonts/OFL.txt` | `make brand` |
| README heading | `brand/kit/svg/sevjala-horizontal-navy.svg`, and `-white` for GitHub's dark theme, at 400 px | the kit |
| Anything else (slides, a site, print) | the SVG of the right form and ink from `brand/kit/svg/`; the PNG preview if the tool cannot do SVG masks | the kit |

Never hand-edit a file in the "Made by `make brand`" rows; change the source and rebuild.

## Palette

The brand colours (all values are tokens in `brand/tokens.json`):

| Token | Value | Role |
|---|---|---|
| `brand.navy` (Net Navy) | #14233B | Logo ink on light backgrounds; the app tile |
| `brand.blue` (Sevjala Blue) | #2457C5 | The blue logo variant; also the dashboard's light accent (`ui.light.accent`) |
| `brand.white` | #FFFFFF | Reverse logo ink on dark; the mark on the app tile |
| `brand.black` | #000000 | Monochrome logo ink |
| `brand.mist`, `brand.slate`, `brand.rule` | #F3F6FB, #56657A, #DBE2EC | Guide PDF and review sheets only |

The other groups in `brand/tokens.json`:

- `ui.light.*` and `ui.dark.*`: the dashboard's CSS custom properties in `ui/src/app.css`, one
  token per property per theme. `ui.*.brand-ink` is the header mark and wordmark: Net Navy in
  the light theme, white in the dark one, as the lockups.
- `ui.on-fill` (text on filled buttons) and `ui.scrim` (the dialog backdrop, black at 45%).
- `tray.*`: the tray's status dots and their white ring. They are mid-tones that read on both
  light and dark taskbars, so they deliberately differ from the dashboard's status colours.

## Type

- **Wordmark:** Plus Jakarta Sans Semibold (600), -0.012 em tracking, kerning off, colour
  `var(--brand-ink)`. In the dashboard it is the `.wordmark` class in `ui/src/app.css`, used only
  on the header's `<h1>`. The font file holds only the letters of "Sevjala", so the class
  cannot set other text in the brand face; other letters would fall back to the system font.
- **Everything else:** the system UI stack, `system-ui, -apple-system, 'Segoe UI', Roboto,
  sans-serif`, as before. The brand face is not loaded for headings or body text.
- The font is self-hosted and embedded in the binary: no CDN, nothing fetched from the
  Internet. It is served as its own file from `/assets/`, because the dashboard's CSP
  (`default-src 'self'`) blocks `data:` fonts; `ui/vite.config.js` sets `assetsInlineLimit: 0`
  for that reason.
- Licence: SIL Open Font License 1.1, with no reserved font name. The full text sits beside
  the source fonts (`brand/kit/source/fonts/OFL.txt`) and ships in the binary as
  `ui/public/fonts/OFL.txt` (served at `/fonts/OFL.txt`); the subset also keeps the copyright
  and licence URL in its own name table. `uiproxy` serves the font as `font/woff2`.

## Per surface

| Surface | What it shows | Colours from |
|---|---|---|
| Dashboard (`ui/`) | Tab: the app tile. Header: micro mark (32 px, tinted `--brand-ink`) with the status dot, and the wordmark; the hostname line stays in the UI font | `ui/src/app.css` custom properties |
| Tray (`tray/`) | The 32 px app tile with the status dot | `tray/icon.go`'s `colors` table |
| Windows exe, Start menu | The app tile | baked into `winres/icon.png` |
| README | The horizontal lockup, 400 px | the kit SVGs |
| CLI | Text only: no colours, no logo | |

Icons (exe, tray, tab) are the one place the brand colours are baked into pixels; every mark
inside the dashboard is tinted by CSS.

## White-label limits

Sevjala has no white-label or brand-pack mechanism: nothing reads a brand from configuration.
The name and the artwork are the product's own, so they are written where they are used (the
`<h1>` and title in `ui/`, the tray tooltip, `winres/winres.json`, CLI text). If a white-label
build is ever wanted, it needs its own decision first; the colours would come from a pack that
replaces `brand/tokens.json`, and those places would read the name from it.

## How to change each

| Change | Steps |
|---|---|
| A dashboard or tray colour | Edit the token's value in `brand/tokens.json` and the mapped place in the code (the error from `make brand-check` names both), then `make brand-check`. |
| A brand colour (`brand.*`) | Edit the token, run `make brand` (the kit, icons and mark follow), review the PDF (below), check the screens, `make brand-check`. |
| A new colour anywhere | Add a token with a role, then a `surfaces` entry mapping the file and its name for it (a CSS custom property as `--name` or `--name@dark`; any other colour by its hex value; a tray colour by its Go key). `make brand-check` fails until both exist. |
| The artwork | Edit `brand/kit/source/geometry.py`, run `make brand`, render the PDF with `pdftoppm -r 80 -png brand/kit/sevjala-brand-guide.pdf /tmp/guide`, look at all seven pages, then `python3 brand/kit/source/build.py --record-review`. Check the tray (`go test ./tray/`) and the dashboard in a browser. |
| The guide's text | Edit `guide()` in `brand/kit/source/build.py`, then the same PDF review. |
| Where an icon is installed, or its size | `INSTALLED` (and `UI_MARK`, `UI_FONT`) in `brand/kit/source/build.py`, then `make brand`. |
| The wordmark letters (a new name) | `wordmark()` and the subset text in `install()` in `build.py`, then `make brand` and the PDF review. |

## Checks

**`make brand-check`** (`go run ./scripts/brandcheck`, stdlib only) runs in the `brand` CI
workflow on every pull request and every push to `master`, documentation included, and its
tests run with `go test ./...`. It reads tracked files plus new files git does not ignore, and
fails on:

- a colour that differs from its token (drift), naming the file, line, key and token;
- a colour that no `surfaces` entry maps (a colour with no token);
- a `surfaces` entry the code no longer has, or an `unchecked` entry that matches no file
  (both stale); a token with no role, a value that is not upper-case `#RRGGBB`/`#RRGGBBAA`, a
  broken `ref`;
- in Markdown, a hex colour that is not the value of some token;
- `hsl()`, `hwb()`, `lab()`, `lch()`, `oklab()`, `oklch()` and `color()` colours (write hex or
  `rgb()`), and a named CSS colour such as `white` in a colour property of a `.css` file or a
  `<style>` block, comments included.

What it reads: hex colours (3, 4, 6 or 8 digits) and `rgb()`/`rgba()` in `.css`, `.svelte`,
`.html`, `.php`, `.js`, `.mjs` and `.svg`; in `.css` and in `<style>` blocks, custom properties
by name, per theme (`@dark` inside `prefers-color-scheme: dark`); in `.go`,
`Name: {0xRR, 0xGG, 0xBB, 0xAA}` and `color.NRGBA{0x…}`/`color.RGBA{0x…}` literals plus any 6- or
8-digit hex; in `.py` and `.md`, 6- and 8-digit hex. `rgb()` built from `var(--…)` is skipped:
the custom properties are checked where they are set.

What it cannot see, so review it by eye:

- colours inside PNGs, and colours in other file types (JSON, YAML);
- named colours in markup: SVG attributes (`fill="red"`), inline `style` attributes, Svelte
  `style:` directives, JS strings;
- Go colours written in decimal or with field names (`color.RGBA{255, 0, 0, 255}`), the
  `color.White`/`color.Black` variables, and 3-digit hex in Go files;
- a second use of an already-mapped value in the same file: a colour mapped by its value
  (`#FFFFFF` in `Dashboard.svelte`) may appear any number of times in that file.

It can also report a hex lookalike that is not a colour: an id or URL fragment made only of the
letters a to f and digits, three, four, six or eight long (in `url(#…)`, say). Rename the id. The named exceptions are in `unchecked` in `brand/tokens.json`, each
with its reason:
the token file itself, the checker's own fixtures, the 2026-11-09 kit notes (a historical
snapshot) and `testbed/` (a stand-in customer site, not a Sevjala surface).

**`make brand`** rebuilds `brand/kit/` and every file in the "Made by `make brand`" rows. With
the pinned Python packages and renderer it writes the same bytes, so after a rebuild with no
edits `git status` stays clean; the builder stops if a package differs from its pin, and stops
when the guide PDF changed without a recorded review. `--record-review` only records that
someone looked at every page; run it after looking, never to get past the gate. The builder
needs Python and Node, so CI does not run it.
