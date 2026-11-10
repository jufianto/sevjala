"""Editable hand-drawn geometry. Coordinates are design units, not traced pixels.

Full mark follows the supplied composition; compact/micro are derived proposals.
No raster tracing is used. Masks make mesh separations truly transparent.
Colours come from brand/tokens.json through build.py; the mask's black and white are
luminance, not paint.
"""


def path(d, color, width, extra=''):
    return f'<path d="{d}" fill="none" stroke="{color}" stroke-width="{width}" stroke-linecap="round" stroke-linejoin="round" {extra}/>'


def database(color, compact=False):
    if compact:
        x, right, top, step, height, ry, count = 350, 750, 355, 145, 65, 42, 3
    else:
        x, right, top, step, height, ry, count = 350, 750, 335, 125, 65, 40, 4
    parts = []
    for i in range(count):
        y = top + i * step
        bottom = y + height
        # Shared elliptical profiles regularize the generated tier shapes.
        top_control = y-ry*1.3333 if i == 0 else y+ry*1.3333
        d = (f'M{x} {y} C{x} {top_control:g} {right} {top_control:g} {right} {y} '
             f'V{bottom} C{right} {bottom+ry*1.3333:g} {x} {bottom+ry*1.3333:g} {x} {bottom} Z')
        parts.append(f'<path d="{d}" fill="{color}"/>')
    # Knock out the first tier's top with an ellipse; no white paint is used.
    return ''.join(parts), f'<ellipse cx="550" cy="{top}" rx="184" ry="29" fill="black"/>'


def mark(color, variant='full', prefix='mark'):
    compact = variant == 'compact'
    if variant == 'micro':
        return micro(color, prefix)
    db, top_hole = database(color, compact)
    outline = ('M550 190 C525 255 413 265 350 315 '
               'C337 410 312 461 277 550 C245 625 233 676 245 730 '
               'C265 830 390 910 550 910 C710 910 835 830 855 730 '
               'C867 676 855 625 823 550 C788 461 763 410 750 315 '
               'C687 265 575 255 550 190 Z')
    # Mirror each strand to retain a regular, balanced enclosing net.
    strand_ds = [
        'M550 190 C535 230 507 256 512 285 C520 360 634 455 745 530 C801 570 837 611 854 660',
        'M405 286 C365 370 371 478 465 587 C559 696 670 803 744 853',
        'M326 390 C296 482 323 603 407 707 C473 787 551 864 615 905',
        'M281 543 C256 639 296 726 362 788 C395 819 430 848 472 900',
    ]
    if compact:
        strand_ds = [
            'M550 190 C520 265 487 304 526 379 C594 510 757 599 854 686',
            'M346 331 C312 479 400 640 500 746 C552 803 608 858 669 894',
        ]
    strands = ''.join(path(d, color, 15 if not compact else 21) +
                      f'<g transform="translate(1100 0) scale(-1 1)">{path(d,color,15 if not compact else 21)}</g>'
                      for d in strand_ds)
    # The mesh crosses the dark database with a negative-space separator.
    cuts = ''.join(path(d, 'black', 30 if not compact else 40) +
                   f'<g transform="translate(1100 0) scale(-1 1)">{path(d,"black",30 if not compact else 40)}</g>'
                   for d in strand_ds)
    # Omit small isolated fill fragments where tier edges meet the mesh.
    if compact:
        cuts += '<path d="M515 305H585V335H515Z M340 548H362V580H340Z M738 548H760V580H738Z M535 448H565V467H535Z" fill="black"/>'
    else:
        cuts += '<path d="M515 289H585V320H515Z M340 645H360V667H340Z M740 645H760V667H740Z M450 741H465V748H450Z M635 741H650V748H635Z" fill="black"/>'
    rod = 'M135 690 C121 531 277 193 530 125'
    loop = 'M550 190 C518 145 508 120 531 104 C556 84 589 98 589 126 C589 147 568 177 550 190'
    handle = (f'<rect x="99" y="690" width="74" height="135" rx="19" fill="{color}"/>'
              f'<path d="M113 822 H159 V870 H113 Z" fill="none" stroke="{color}" stroke-width="9"/>'
              f'<path d="M126 846 H130 M143 846 H147" stroke="{color}" stroke-width="9"/>')
    guides = ''
    if not compact:
        guides = (path('M135 665 C160 630 162 611 144 589 C185 545 200 499 189 456 C239 415 275 359 268 320 C319 284 365 247 378 204 C430 178 482 146 530 125',color,5)
                  + ''.join(f'<ellipse cx="{x}" cy="{y}" rx="11" ry="4" transform="rotate(24 {x} {y})" fill="none" stroke="{color}" stroke-width="4"/>'
                            for x,y in [(144,589),(189,456),(268,320),(378,204)]))
    else:
        guides = path('M140 647 C230 430 342 231 530 125',color,9)
    return (f'<defs><mask id="{prefix}-db" maskUnits="userSpaceOnUse" x="0" y="0" width="1000" height="1000">'
            f'<rect width="1000" height="1000" fill="white"/>{top_hole}{cuts}</mask></defs>'
            f'<g mask="url(#{prefix}-db)">{db}</g>'
            + path(outline,color,18 if not compact else 23)
            + strands + path(loop,color,18 if not compact else 23)
            + path(rod,color,23 if not compact else 27) + guides + handle)


def micro(color, prefix):
    """Pixel-oriented 32-unit design, with no fine line or rod guide rings."""
    db = ('M11 12 C11 9.5 25 9.5 25 12 V15 C25 17.5 11 17.5 11 15 Z '
          'M11 18 C14 19.5 22 19.5 25 18 V21 C25 23.5 11 23.5 11 21 Z '
          'M11 24 C14 25.5 22 25.5 25 24 V26 C25 28.5 11 28.5 11 26 Z')
    net = ('M18 6 C16 8 12 8.5 10 11 C9 16 6 21 8 25 '
           'C11 31 25 31 28 25 C30 21 27 16 26 11 C24 8.5 20 8 18 6 Z')
    mesh = 'M18 6 C15 12 16 18 28 23 M18 6 C21 12 20 18 8 23 M9 18 C11 23 17 28 22 29 M27 18 C25 23 19 28 14 29'
    mask = (f'<defs><mask id="{prefix}-micro" maskUnits="userSpaceOnUse" x="0" y="0" width="32" height="32">'
            '<rect width="32" height="32" fill="white"/>'
            '<ellipse cx="18" cy="12" rx="5" ry="0.8" fill="black"/>'
            '<rect x="17.2" y="9.8" width="1.6" height="2.0" fill="black"/>'
            f'{path(mesh,"black",2.7)}</mask></defs>')
    art = (mask + f'<path d="{db}" fill="{color}" mask="url(#{prefix}-micro)"/>'
           + path(net,color,1.4) + path(mesh,color,1.2)
           + path('M18 6 C14.5 1 21.5 0.5 20 4 C19.5 5 18.7 5.7 18 6',color,1.4)
           + path('M3 23 C2.5 15 7 6 16 3',color,1.5)
           + f'<rect x="1.4" y="22.5" width="3.8" height="5.5" rx="0.8" fill="{color}"/>'
           + path('M2.3 28 V30 H4.3 V28',color,0.9))
    return f'<g transform="translate(0 0) scale(31.25)">{art}</g>'
