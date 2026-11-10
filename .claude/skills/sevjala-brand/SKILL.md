---
name: sevjala-brand
description: Use when a Sevjala change touches anything visual or brand-related - a colour, the logo, mark or wordmark, an icon (exe, tray, browser tab), a font, the dashboard header, brand/kit, brand/tokens.json, or a new screen or surface that shows the product name.
---

# Sevjala brand

The rules, files and palette roles are in [docs/reference/brand.md](../../../docs/reference/brand.md).
Read it first; this skill is the per-surface recipe and the definition of done.

Three facts drive every recipe:

- `brand/tokens.json` is the only palette. Every colour in the code is mapped there to a token
  with a role; `make brand-check` fails on drift and on a colour with no token.
- `make brand` (`brand/kit/source/build.py`) writes the kit and every installed brand file:
  `winres/icon.png`, `tray/brand.png`, `ui/public/icon.png`, `ui/src/lib/mark.svg`,
  `ui/src/fonts/*.woff2`. Never hand-edit those; change the source and rebuild.
- Only the product name is the wordmark. Labels, hostnames and running text stay in the UI font.

## Recipes

**Dashboard colour (`ui/`).** Use an existing `var(--…)` from `ui/src/app.css`. For a new one:
add the token (with a role) to `brand/tokens.json`, add the custom property to both theme blocks
in `app.css`, map `--name` and `--name@dark` in `surfaces`, run `make brand-check`. Never write a
hex, `rgb()` or named colour in a component; the check reports it.

**Dashboard mark or wordmark.** The header imports `ui/src/lib/mark.svg?raw` and inlines it; it
paints in `currentColor`, so tint it with `color: var(--brand-ink)`, never with a baked image or a
CSS mask (a mask that fails to load hides the element). The wordmark is the `.wordmark` class:
put it only on the product name. The font holds only the letters of "Sevjala". Assets must stay
same-origin files: the CSP is `default-src 'self'`, and `ui/vite.config.js` keeps
`assetsInlineLimit: 0` so fonts are not turned into blocked `data:` URLs. Check in a real browser
with `document.fonts` (status `loaded`) and an empty console, in light and dark.

**Tray (`tray/`).** `tray/brand.png` is the 32 px app tile; `tray/icon.go` draws the status dot
over it. Status colours are the `tray.*` tokens, mapped by the Go key in `colors`. Keep the dot
and its white ring: the brand never carries the status. Run `go test ./tray/`, and render the
four states for a look (`tray.PNG(tray.Green)` and so on).

**Exe and Start menu (`winres/`).** `winres/icon.png` is the 256 px app tile; go-winres builds
the exe icon from it at release. Check that `go tool go-winres make` still accepts it (run it in a
scratch copy of `winres/`; the `.syso` output is never committed).

**Placed artwork (README, docs, slides, a site).** Use the SVG from `brand/kit/svg/` with the
right detail level and ink: full mark 128 px or more, compact from 64 px, micro below; horizontal
lockup 400 px or more; clear space a quarter of the mark height; navy on light, white on dark.
For GitHub, use a `<picture>` with the white file for `prefers-color-scheme: dark`.

**Artwork or guide change.** Edit `brand/kit/source/geometry.py` or `guide()` in `build.py`,
run `make brand`, render the PDF (`pdftoppm -r 80 -png brand/kit/sevjala-brand-guide.pdf
/tmp/guide`), look at all seven pages, then `python3 brand/kit/source/build.py --record-review`.

**A new surface that shows the brand.** Add its colours as tokens and `surfaces` entries, add any
installed file to `INSTALLED` (or beside `UI_MARK`) in `build.py`, and add its row to the "Which
file for which job" and "Per surface" tables in `docs/reference/brand.md`.

## Brand definition of done

- [ ] `make brand-check` passes, with no new entry in `unchecked` unless it names a reason the
      owner would accept.
- [ ] If anything under `brand/` or a "made by `make brand`" file changed: `make brand` ran, a
      second `make brand` leaves `git status` clean, and a changed PDF has a fresh
      `--record-review` after all seven pages were looked at.
- [ ] Marks inside the UI are tinted by CSS; only the exe, tray and tab icons bake colour.
- [ ] The wordmark is on the product name only; labels beside it use the UI font.
- [ ] Placed artwork follows the size, clear-space and contrast rules; UI chrome uses the micro mark.
- [ ] The tray still shows the status dot with its white ring (`go test ./tray/`).
- [ ] The dashboard was checked in a browser, light and dark, at desktop and phone width; the
      font is `loaded` and the console has no CSP error.
- [ ] `docs/reference/brand.md` still describes what the code and the check do; a new identity
      decision has a row in `docs/decisions.md`; Windows checks that need real PCs are listed in
      `docs/smoke-tests.md`.
