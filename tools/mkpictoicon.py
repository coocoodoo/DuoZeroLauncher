"""PictoChat's icon: a 9 x 9 pixel smiley in a square (the user's design).

usage: py tools/mkpictoicon.py
Writes, all from the grid below:
  icons/pictochat.svg        the icon as an SVG (crisp edges)
  assets/apps/pictochat.png  the app icon, 9 x 9 (Duo scales it up pixel-crisp for the tiles)
  assets/tiles/pictochat.png a 24 x 24 alpha mask (title bars draw it in the bar's ink colour)"""
import os
from PIL import Image

GRID = [
    "#########",
    "#.......#",
    "#..#.#..#",
    "#..#.#..#",
    "#.......#",
    "#.#...#.#",
    "#..###..#",
    "#.......#",
    "#########",
]
N = len(GRID)
here = os.path.dirname(os.path.dirname(os.path.abspath(__file__)))

# SVG: a white face, then each black run of a row as one rectangle
rects = []
for y, row in enumerate(GRID):
    x = 0
    while x < N:
        if row[x] == "#":
            s = x
            while x < N and row[x] == "#":
                x += 1
            rects.append(f'<rect x="{s}" y="{y}" width="{x - s}" height="1"/>')
        else:
            x += 1
svg = (f'<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 {N} {N}" shape-rendering="crispEdges">'
       f'<rect x="1" y="1" width="{N - 2}" height="{N - 2}" fill="#fff"/>'
       f'<g fill="#000">{"".join(rects)}</g></svg>\n')
open(os.path.join(here, "icons", "pictochat.svg"), "w", newline="\n").write(svg)

# app icon: black on white inside, transparent outside, one image pixel per grid cell
icon = Image.new("RGBA", (N, N), (0, 0, 0, 0))
for y, row in enumerate(GRID):
    for x, c in enumerate(row):
        icon.putpixel((x, y), (0, 0, 0, 255) if c == "#" else (255, 255, 255, 255))
os.makedirs(os.path.join(here, "assets", "apps"), exist_ok=True)
icon.save(os.path.join(here, "assets", "apps", "pictochat.png"))

# title-bar mask: the black cells as alpha, drawn big and box-filtered down to 24 x 24
S = 24 * N
big = Image.new("L", (S, S), 0)
for y, row in enumerate(GRID):
    for x, c in enumerate(row):
        if c == "#":
            big.paste(255, (x * 24, y * 24, (x + 1) * 24, (y + 1) * 24))
mask = Image.new("RGBA", (24, 24), (0, 0, 0, 0))
mask.putalpha(big.resize((24, 24), Image.BOX))
mask.save(os.path.join(here, "assets", "tiles", "pictochat.png"))
print("wrote icons/pictochat.svg, assets/apps/pictochat.png, assets/tiles/pictochat.png")
