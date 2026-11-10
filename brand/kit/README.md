# Sevjala brand kit

The approved artwork, the brand guide and the builder that makes them. Which file goes where
in the app, and every rule, is in [docs/reference/brand.md](../../docs/reference/brand.md).
Colours come from [brand/tokens.json](../tokens.json) only.

| Path | Contents |
|---|---|
| `sevjala-brand-guide.pdf` | The 7-page guide: marks, palette, type, usage, icons |
| `sevjala-overview.png` | One-page overview |
| `svg/` | 22 vector files: full, compact and micro marks (navy, white, black, blue), wordmarks, horizontal and stacked lockups (navy, white, blue), the app tile |
| `png/` | A transparent preview of each SVG, 2048 px wide (2400 px for wordmarks and horizontal lockups) |
| `reference/` | Source comparison, curve detail and small-size review sheets; `pdf-review.json`, the record of the last page-by-page PDF review; the [2026-11-09 delivery notes](reference/notes-2026-11-09.md) (historical) |
| `source/` | `geometry.py` (hand-drawn marks), `build.py`, the pinned renderer (`render.mjs`, `package.json`, `package-lock.json`), `requirements.txt`, Plus Jakarta Sans 400/600 with its SIL OFL licence, and the original concept `originals/sevjala.jpg` |

## Rebuild

Needs Python 3.11+ with the pinned packages and Node.js 20+:

```sh
python3 -m pip install -r brand/kit/source/requirements.txt
make brand            # npm ci for the renderer, then brand/kit/source/build.py
git status --short    # empty: a rebuild without edits writes the same bytes
```

`build.py` stops if an installed package differs from its pin, since other versions encode
different bytes. It also stops when the guide PDF changed since its last review: render it with
`pdftoppm -r 80 -png brand/kit/sevjala-brand-guide.pdf /tmp/guide`, look at all seven pages,
then run `python3 brand/kit/source/build.py --record-review`. That flag only records that the
pages were looked at; it is not a way past the gate.

The fonts are licensed under the SIL Open Font License 1.1 (`source/fonts/OFL.txt`). The
artwork follows the owner's rights in the original concept; this kit is not a trademark
clearance.
