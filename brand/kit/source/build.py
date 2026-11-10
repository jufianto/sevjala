"""Sevjala brand-kit builder. Run from the repository root: make brand.

Reads brand/tokens.json and the hand-authored geometry, and regenerates every file in
brand/kit/ (SVGs, PNG previews, review sheets, the guide PDF). A second run without
edits rewrites the same bytes. After an artwork or guide change, render the PDF
(pdftoppm), inspect every page, then run with --record-review.
"""
from importlib.metadata import version
from pathlib import Path
import hashlib
import json
import subprocess
import sys
import xml.etree.ElementTree as ET
import re

from PIL import Image, ImageDraw, ImageFont
from fontTools.ttLib import TTFont
from fontTools.pens.svgPathPen import SVGPathPen
from pypdf import PdfReader
from reportlab.pdfgen import canvas
from reportlab.pdfbase import pdfmetrics
from reportlab.pdfbase.ttfonts import TTFont as RLFont
from reportlab.lib.colors import HexColor
from reportlab.lib.utils import ImageReader

from geometry import mark

SOURCE = Path(__file__).resolve().parent
KIT = SOURCE.parent
REPO = KIT.parent.parent
TEMP = KIT / '.build'
FONT = SOURCE / 'fonts/PlusJakartaSans-600.ttf'
REGULAR = SOURCE / 'fonts/PlusJakartaSans-400.ttf'
DATE = '2026-11-10'
REVIEW = KIT / 'reference/pdf-review.json'
jobs = []


def tokens():
    colors = json.loads((REPO / 'brand/tokens.json').read_text())['colors']

    def value(name):
        t = colors[name]
        return value(t['ref']) if 'ref' in t else t['value']
    return {n: value(f'brand.{n}') for n in ['navy', 'blue', 'white', 'black', 'mist', 'slate', 'rule']}


C = tokens()
NAVY, BLUE, WHITE, BLACK, MIST, MUTED, RULE = (C[n] for n in ['navy', 'blue', 'white', 'black', 'mist', 'slate', 'rule'])


def pinned():
    """Byte-identical output needs the pinned renderer and libraries."""
    for line in (SOURCE / 'requirements.txt').read_text().split():
        name, want = line.split('==')
        if version(name) != want:
            sys.exit(f'{name} {version(name)} is installed; requirements.txt pins {want}')
    want = json.loads((SOURCE / 'package.json').read_text())['dependencies']['@resvg/resvg-js']
    got = SOURCE / 'node_modules/@resvg/resvg-js/package.json'
    if not got.exists() or json.loads(got.read_text())['version'] != want:
        sys.exit(f'@resvg/resvg-js {want} is not installed: npm ci --prefix {SOURCE.relative_to(REPO)}')


def sha(path):
    return hashlib.sha256(path.read_bytes()).hexdigest()


def write(path, content):
    path = KIT / path
    path.parent.mkdir(parents=True, exist_ok=True)
    path.write_text(content, encoding='utf-8')
    return path


def svg(body, width=1000, height=1000, title='Sevjala'):
    return (f'<svg xmlns="http://www.w3.org/2000/svg" width="{width}" height="{height}" '
            f'viewBox="0 0 {width} {height}" role="img" aria-label="{title}">'
            f'<title>{title}</title>{body}</svg>')


def wordmark(color, size=150):
    font = TTFont(FONT)
    glyphs = font.getGlyphSet()
    cmap = font.getBestCmap()
    upm = font['head'].unitsPerEm
    scale = size / upm
    x, parts = 0, []
    for char in 'Sevjala':
        name = cmap[ord(char)]
        pen = SVGPathPen(glyphs)
        glyphs[name].draw(pen)
        parts.append(f'<path d="{pen.getCommands()}" transform="translate({x:.5f} {size}) scale({scale:.8f} {-scale:.8f})"/>')
        # The lockup tracks each letter 12 units (-0.012em) tighter than the font.
        x += (font['hmtx'][name][0] - 12) * scale
    return f'<g fill="{color}">{"".join(parts)}</g>', x


def export(name, body, width=1000, height=1000, pixels=2048, title=None):
    p = write(f'svg/{name}.svg', svg(body, width, height, title or name))
    out = KIT / f'png/{name}.png'
    out.parent.mkdir(parents=True, exist_ok=True)
    jobs.append({'input': str(p), 'output': str(out), 'width': pixels})


def vectors():
    for variant in ['full', 'compact', 'micro']:
        for label, color in [('navy', NAVY), ('white', WHITE), ('black', BLACK), ('blue', BLUE)]:
            export(f'sevjala-mark-{variant}-{label}', mark(color, variant), title=f'Sevjala {variant} mark - {label}')
    for label, color in [('navy', NAVY), ('white', WHITE), ('blue', BLUE)]:
        lettering, w = wordmark(color)
        export(f'sevjala-wordmark-{label}', f'<g transform="translate(20 0)">{lettering}</g>', int(w+40), 190, 2400)
        body = f'<g transform="translate(0 0) scale(.4)">{mark(color, prefix="horizontal")}</g>'
        body += f'<g transform="translate(440 111)">{lettering}</g>'
        export(f'sevjala-horizontal-{label}', body, int(w+470), 400, 2400)
        lettering, w = wordmark(color, 145)
        body = f'<g transform="translate(150 0) scale(.7)">{mark(color, prefix="stacked")}</g>'
        body += f'<g transform="translate({(1000-w)/2:.4f} 670)">{lettering}</g>'
        export(f'sevjala-stacked-{label}', body, 1000, 880, 2048)
    app = f'<rect width="1000" height="1000" rx="210" fill="{NAVY}"/>'
    app += f'<g transform="translate(110 100) scale(.8)">{mark(WHITE, "micro", prefix="app")}</g>'
    export('sevjala-app-icon', app, title='Sevjala app icon - derived proposal')


def render(batch, name):
    TEMP.mkdir(exist_ok=True)
    (TEMP / name).write_text(json.dumps(batch))
    subprocess.run(['node', str(SOURCE / 'render.mjs'), str(TEMP / name)], check=True)


def image_on(img, box, background=WHITE):
    im = img.convert('RGBA')
    im.thumbnail((box, box), Image.Resampling.LANCZOS)
    out = Image.new('RGBA', (box, box), background)
    out.alpha_composite(im, ((box-im.width)//2, (box-im.height)//2))
    return out.convert('RGB')


def tray_samples():
    """Transparent micro marks rendered at each target pixel width, for the review sheets."""
    icons = TEMP / 'icons'
    icons.mkdir(parents=True, exist_ok=True)
    render([{'input': str(KIT / f'svg/sevjala-mark-micro-{label}.svg'),
             'output': str(icons / f'sevjala-tray-{label}-{size}.png'), 'width': size}
            for label in ['navy', 'white', 'black'] for size in [16, 24, 32, 48, 64]], 'icons.json')
    return icons


def sheets(icons):
    font = lambda size: ImageFont.truetype(str(FONT), size)
    regular = lambda size: ImageFont.truetype(str(REGULAR), size)
    def text(draw, xy, value, size=24, fill=NAVY): draw.text(xy, value, font=regular(size), fill=fill)
    out = Image.new('RGB', (1800, 1300), MIST); d = ImageDraw.Draw(out)
    d.text((70, 50), 'Sevjala', font=font(62), fill=NAVY)
    text(d, (72, 132), 'Database backup. A safety net for your data.', 25)
    text(d, (72, 177), 'BRAND KIT / HAND-DRAWN RECONSTRUCTION / '+DATE, 17, MUTED)
    for x, y, w, h in [(70, 250, 1050, 660), (1150, 250, 580, 660), (70, 940, 1660, 285)]:
        d.rounded_rectangle((x, y, x+w, y+h), radius=24, fill=WHITE)
    out.paste(image_on(Image.open(KIT/'png/sevjala-mark-full-navy.png'), 590), (285, 270))
    text(d, (102, 855), 'FULL MARK / source-inspired composition', 20)
    dark = Image.new('RGBA', (500, 500), NAVY)
    im = Image.open(KIT/'png/sevjala-mark-micro-white.png').resize((450, 450))
    dark.alpha_composite(im, (25, 25)); out.paste(dark.convert('RGB'), (1190, 280))
    text(d, (1190, 820), 'MICRO MARK / small sizes', 20)
    lock = Image.open(KIT/'png/sevjala-horizontal-navy.png').convert('RGBA'); lock.thumbnail((1020, 230))
    bg = Image.new('RGBA', lock.size, WHITE); bg.alpha_composite(lock); out.paste(bg.convert('RGB'), (100, 960))
    for i, c in enumerate([NAVY, BLUE, WHITE]):
        xx = 1310+i*125; d.rounded_rectangle((xx, 1010, xx+95, 1105), radius=15, fill=c, outline=RULE)
        text(d, (xx, 1125), c, 15)
    out.save(KIT/'sevjala-overview.png')
    # Source fit is visually documented, not represented as pixel identity.
    compare = Image.new('RGB', (1800, 820), WHITE); d = ImageDraw.Draw(compare)
    panels = [('Supplied raster concept', SOURCE/'originals/sevjala.jpg'),
              ('Clean reconstructed full mark', KIT/'png/sevjala-mark-full-navy.png'),
              ('Derived compact mark', KIT/'png/sevjala-mark-compact-navy.png')]
    for i, (label, p) in enumerate(panels):
        compare.paste(image_on(Image.open(p), 570), (i*600+15, 65))
        text(d, (i*600+24, 660), label, 21)
    text(d, (24, 735), 'Geometry regularized; tiers, full net and USB rod retained. Reconstructed artwork is not pixel-identical.', 21, MUTED)
    compare.save(KIT/'reference/reconstruction-comparison.png')
    # Both actual-size and nearest-neighbor enlarged samples are included.
    check = Image.new('RGB', (1700, 960), MIST); d = ImageDraw.Draw(check)
    for row, label in enumerate(['navy', 'white']):
        y = 80+row*420; bg = WHITE if label == 'navy' else NAVY
        d.rounded_rectangle((30, y, 1670, y+380), radius=20, fill=bg)
        for i, size in enumerate([16, 24, 32, 48, 64]):
            xx = 65+i*320
            im = Image.open(icons/f'sevjala-tray-{label}-{size}.png')
            check.paste(image_on(im, size, bg), (xx, y+40))
            check.paste(image_on(im.resize((192, 192), Image.Resampling.NEAREST), 192, bg), (xx, y+110))
            text(d, (xx, y+320), f'{size}px / enlarged below', 18, NAVY if label == 'navy' else WHITE)
    text(d, (35, 24), 'MICRO MARK / actual pixels + nearest-neighbor enlargement', 22)
    text(d, (35, 920), '16px is an identification cue; the full motif becomes clearer at 32px and above. Review in the actual tray.', 20)
    check.save(KIT/'reference/small-size-check.png')
    # High zoom detail of actual rendered path geometry.
    detail = Image.new('RGB', (1800, 1000), WHITE); d = ImageDraw.Draw(detail)
    im = Image.open(KIT/'png/sevjala-mark-full-navy.png')
    for i, (label, box) in enumerate([('Loop and rod', (920, 120, 1260, 540)), ('Net crossings', (800, 920, 1240, 1380)), ('USB handle', (150, 1320, 420, 1840))]):
        crop = im.crop(box); detail.paste(image_on(crop, 540), (i*600+30, 120))
        text(d, (i*600+30, 720), label, 26)
    text(d, (30, 50), 'CURVE REVIEW / hand-authored cubic geometry', 30)
    text(d, (30, 880), 'Sparse editable curves; transparent mesh separators; rounded stroke joins. No automatic raster tracing.', 23, MUTED)
    detail.save(KIT/'reference/curve-detail-check.png')


def guide(icons):
    pdfmetrics.registerFont(RLFont('PJS', str(REGULAR)))
    pdfmetrics.registerFont(RLFont('PJS-Semi', str(FONT)))
    c = canvas.Canvas(str(KIT/'sevjala-brand-guide.pdf'), pagesize=(842, 595), invariant=1)
    c.setTitle('Sevjala - Brand guide'); c.setAuthor('Sevjala')
    def rect(x, y, w, h, col, r=0):
        c.setFillColor(HexColor(col))
        c.roundRect(x, y, w, h, r, stroke=0, fill=1)
    def txt(x, y, s, size=12, color=NAVY, bold=False):
        c.setFillColor(HexColor(color)); c.setFont('PJS-Semi' if bold else 'PJS', size); c.drawString(x, y, s)
    def paragraph(x, y, s, width=350, size=12, color=MUTED, leading=19):
        words = s.split(); line = ''
        for word in words:
            test = f'{line} {word}'.strip()
            if pdfmetrics.stringWidth(test, 'PJS', size) > width:
                txt(x, y, line, size, color); y -= leading; line = word
            else: line = test
        if line: txt(x, y, line, size, color); y -= leading
        return y
    def img(path, x, y, w, h):
        c.drawImage(ImageReader(str(path)), x, y, width=w, height=h, mask='auto', preserveAspectRatio=True, anchor='c')
    def page(n, kicker, title, subtitle=None):
        rect(0, 0, 842, 595, WHITE)
        txt(42, 556, 'SEVJALA / BRAND GUIDE', 10, MUTED, True); txt(680, 556, DATE, 10, MUTED)
        txt(42, 510, title, 30, NAVY, True)
        if subtitle: txt(42, 482, subtitle, 11, MUTED)
        txt(42, 25, kicker, 9, MUTED); txt(760, 25, f'{n:02d} / 07', 9, MUTED)
    page(1, 'IDENTITY', 'A safety net for your data.')
    txt(42, 433, 'Sevjala', 55, NAVY, True)
    paragraph(44, 385, 'LAN-first database backup and recovery for small offices running MySQL or MariaDB.', 330, 16, NAVY, 25)
    paragraph(44, 273, 'A database fully enclosed in a fishing net, gathered above and connected to a USB-inspired fishing rod. A visual metaphor for supported, recoverable data.', 315, 12)
    rect(440, 103, 360, 360, MIST, 24); img(KIT/'png/sevjala-mark-full-navy.png', 451, 110, 340, 340)
    paragraph(44, 141, 'Hand-drawn reconstruction of the owner-selected Flow concept. Mark, lettering, palette and usage rules approved on '+DATE+'.', 320, 10)
    txt(44, 74, 'Which file for which job: docs/reference/brand.md', 10, BLUE, True)
    c.showPage()
    page(2, 'PROVENANCE', 'From concept to clean geometry', 'The supplied image is the reference. The SVG is an editable reconstruction.')
    img(KIT/'reference/reconstruction-comparison.png', 30, 155, 782, 300)
    paragraph(42, 127, 'Preserved: the full enclosing net, stacked database, gathered loop and USB fishing rod. Regularized: mirrored mesh curves, tier spacing, stroke weight and the USB-to-rod connection.', 750, 12)
    paragraph(42, 72, 'The source JPEG in source/originals/ is unchanged. Every file in svg/ is vector shapes and local masks: no tracing, no embedded raster.', 750, 10)
    c.showPage()
    page(3, 'LOGO SYSTEM', 'One identity, three levels of detail', 'Use the most detailed version that stays clear at its final size.')
    for i, (v, label, body) in enumerate([
        ('full', 'Full mark', 'Main symbol. Four database tiers and detailed net; use at 128px or larger.'),
        ('compact', 'Compact mark', 'Three tiers, fewer net strands and a simpler fishing line; from 64px.'),
        ('micro', 'Micro mark', '32-unit drawing, no rod guides or USB contact dots. Below 64px, down to 16px icons.')]):
        x = 42+i*260; rect(x, 245, 240, 210, MIST, 14); img(KIT/f'png/sevjala-mark-{v}-navy.png', x+20, 250, 200, 200)
        txt(x, 220, label, 17, NAVY, True); paragraph(x, 196, body, 235, 11)
    img(KIT/'png/sevjala-horizontal-navy.png', 42, 63, 355, 112)
    paragraph(435, 137, 'Horizontal and stacked lockups pair the full symbol with outlined Plus Jakarta Sans Semibold lettering, tracked -0.012em. They are new lockups, not recovered historical artwork.', 350, 11)
    c.showPage()
    page(4, 'COLOR & TYPOGRAPHY', 'Calm, clear, dependable.', 'Navy follows the supplied concept. Blue is the dashboard accent. Values live in brand/tokens.json.')
    for i, (label, col, role) in enumerate([('Net Navy', NAVY, 'Primary logo ink'), ('Sevjala Blue', BLUE, 'Blue logo variant; light-theme UI accent'), ('White', WHITE, 'Background / reverse artwork')]):
        x = 42+i*260; rect(x, 310, 240, 140, MIST, 14); rect(x+12, 322, 216, 116, col, 9)
        txt(x, 282, label, 17, NAVY, True); txt(x, 258, col, 14, MUTED); txt(x, 232, role, 10, MUTED)
    txt(42, 179, 'Sevjala', 38, NAVY, True)
    paragraph(42, 132, 'Plus Jakarta Sans Semibold: the wordmark. Regular and Semibold are bundled under the SIL Open Font License; delivered SVG lettering is outlined.', 350, 11)
    txt(445, 178, 'Application typography', 17, NAVY, True)
    paragraph(445, 146, 'The dashboard keeps the system UI font for everything except the wordmark. Status green, yellow and red remain operational signals, separate from the brand mark.', 350, 11)
    c.showPage()
    page(5, 'USAGE', 'Give the net room to breathe.', 'Rules for placed artwork: logos and lockups shown as images.')
    rect(42, 155, 350, 290, MIST, 16)
    img(KIT/'png/sevjala-mark-full-navy.png', 120, 200, 200, 200)
    bbox = Image.open(KIT/'png/sevjala-mark-full-navy.png').getchannel('A').getbbox()
    left, top, right, bottom = [v*200/2048 for v in bbox]
    padding = (bottom-top)/4
    c.setStrokeColor(HexColor(BLUE)); c.setDash(3, 3)
    c.rect(120+left-padding, 200+200-bottom-padding, right-left+2*padding, bottom-top+2*padding, stroke=1, fill=0)
    c.setDash()
    txt(72, 160, 'Clear space: 1/4 of visible mark height', 10, MUTED)
    y = 429
    for title, body in [('Choose contrast', 'Navy or black on light backgrounds. White on dark backgrounds. Use the supplied transparent artwork.'),
                        ('Preserve the enclosure', 'Keep the net around the entire database and retain the USB rod. Use compact or micro files when details become small.'),
                        ('Preserve proportions', 'Scale uniformly. Avoid stretching, cropping, gradients, shadows, extra strands, or thinning individual strokes.')]:
        txt(435, y, title, 15, NAVY, True); y = paragraph(435, y-25, body, 355, 11)-22
    paragraph(42, 111, 'Minimum widths for placed artwork: full symbol 128px, compact 64px, horizontal lockup 400px; smaller marks use the micro symbol. At 16px the artwork is an identity cue. UI chrome, such as a 32px header mark beside live text, follows docs/reference/brand.md.', 750, 11)
    c.showPage()
    page(6, 'APPLICATION ICONS', 'Ready for desktop and browser.', 'Check each icon in the actual platform before shipping it.')
    img(KIT/'png/sevjala-app-icon.png', 42, 240, 210, 210)
    txt(280, 425, 'App icon and favicon', 17, NAVY, True)
    paragraph(280, 397, 'A white micro mark on a navy tile, used for the program icon, the browser tab and the tray.', 490, 12)
    txt(280, 302, 'Tray', 17, NAVY, True)
    paragraph(280, 275, 'The tray shows the app tile at 32px with the status drawn as a dot in its corner, ringed in white. Transparent micro marks, below, suit a placement on a known background.', 490, 11)
    for row, label in enumerate(['navy', 'white']):
        y = 143-row*72; bg = MIST if label == 'navy' else NAVY
        rect(42, y-12, 758, 68, bg, 8)
        for i, size in enumerate([16, 24, 32, 48, 64]):
            x = 70+i*140
            img(icons/f'sevjala-tray-{label}-{size}.png', x, y, 40, 40)
            txt(x+50, y+15, f'{size}px', 10, NAVY if label == 'navy' else WHITE)
    c.showPage()
    page(7, 'DELIVERY', 'One kit, rebuilt from the repository.', 'brand/kit/source/build.py regenerates every file in brand/kit from brand/tokens.json.')
    txt(42, 437, 'What is in the kit', 19, NAVY, True)
    y = paragraph(42, 405, 'svg/ - full, compact and micro marks; outlined wordmarks; horizontal and stacked lockups; app tile.', 355, 12)
    y = paragraph(42, y-14, 'png/ - transparent previews of every SVG, 2048px wide (2400px for wordmarks and horizontal lockups).', 355, 12)
    y = paragraph(42, y-14, 'source/ - editable geometry, the builder, pinned dependencies, licensed fonts and the original concept.', 355, 12)
    paragraph(42, y-14, 'reference/ - visual comparisons, curve details, small-size checks and the record of the last PDF review.', 355, 12)
    txt(445, 437, 'Using the identity', 19, NAVY, True)
    y = paragraph(445, 405, 'The product backs up databases to other PCs over the LAN. The USB rod is a visual metaphor; it does not imply that a USB disk is required or that USB backup is the primary workflow.', 350, 12)
    y = paragraph(445, y-20, 'Status colours stay separate from the mark: the tray and the dashboard draw the status as a dot beside it.', 350, 12)
    paragraph(445, y-20, 'Every colour in the code is a token in brand/tokens.json; make brand-check fails when the code drifts from it.', 350, 12)
    txt(42, 78, 'Source: owner-supplied sevjala.jpg (Flow raster concept) / approved '+DATE, 10, MUTED)
    c.showPage(); c.save()


def verify():
    """Structural checks; a stale PDF review record fails the build."""
    ns = '{http://www.w3.org/2000/svg}'
    svgs = sorted((KIT/'svg').glob('*.svg'))
    assert len(svgs) == 22, len(svgs)
    for p in svgs:
        doc = ET.parse(p).getroot(); raw = p.read_text()
        assert doc.tag == ns+'svg' and doc.find(ns+'title') is not None, p
        assert not list(doc.iter(ns+'image')) and not list(doc.iter(ns+'text')), p
        assert '<script' not in raw and '@font-face' not in raw, p
        ids = [e.attrib['id'] for e in doc.iter() if 'id' in e.attrib]
        assert len(ids) == len(set(ids)), p
        for target in re.findall(r'url\(#([^)]*)\)', raw): assert target in ids, (p, target)
        assert re.search(r'url\((?!#)', raw) is None, p
    pngs = sorted((KIT/'png').glob('*.png'))
    assert len(pngs) == 22, len(pngs)
    for p in pngs:
        with Image.open(p) as im:
            im.load(); assert im.mode == 'RGBA', p
            alpha = im.getchannel('A'); assert alpha.getextrema() == (0, 255), p
            left, top, right, bottom = alpha.getbbox()
            if p.stem != 'sevjala-app-icon':
                assert left > 0 and top > 0 and right < im.width and bottom < im.height, p
            assert im.width == (2400 if 'horizontal' in p.name or 'wordmark' in p.name else 2048), p
    # Reverse and monochrome marks paint one ink, leaving the gaps clear.
    for label, hexv in [('white', WHITE), ('navy', NAVY), ('black', BLACK)]:
        rgb = tuple(int(hexv[i:i+2], 16) for i in (1, 3, 5))
        im = Image.open(KIT/f'png/sevjala-mark-full-{label}.png')
        assert {px[:3] for px in im.get_flattened_data() if px[3] == 255} == {rgb}, label
    reader = PdfReader(KIT/'sevjala-brand-guide.pdf'); assert len(reader.pages) == 7
    for page in reader.pages:
        assert len(page.extract_text()) > 150
    review = json.loads(REVIEW.read_text())
    if review['sha256'] != sha(KIT/'sevjala-brand-guide.pdf'):
        sys.exit('The guide PDF changed: render it (pdftoppm), inspect all 7 pages, then run build.py --record-review')


def record_review():
    REVIEW.write_text(json.dumps({
        'date': DATE, 'sha256': sha(KIT/'sevjala-brand-guide.pdf'), 'pages_reviewed': list(range(1, 8)),
        'method': 'pdftoppm render of every page, checked for clipping, overlap, contrast and layout.'}, indent=2)+'\n')


def main():
    pinned()
    vectors(); render(jobs, 'render-jobs.json')
    icons = tray_samples(); sheets(icons); guide(icons)
    if '--record-review' in sys.argv:
        record_review()
    verify()
    print(f'Built {len(jobs)} SVG/PNG pairs, the review sheets and {KIT.relative_to(REPO)}/sevjala-brand-guide.pdf')


if __name__ == '__main__':
    main()
