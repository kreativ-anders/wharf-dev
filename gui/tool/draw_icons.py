"""Draws Wharf's icons: the tray icon and the app icon, from one mark.

    python3 tool/draw_icons.py OUT_DIR

writes every size into OUT_DIR, plus sheet.png to look at them side by side.
Copy them into place as follows:

    tray_template_N.png -> assets/tray/template_N.png   (macOS: a template
                           image, which the menu bar tints for light and dark)
    tray_tile_N.png     -> assets/tray/icon_N.png, and assets/tray/icon.ico
                           (Windows and Linux, whose bars may be light or dark,
                           so the mark sits on its own dark tile)
    app_N.png           -> macos/Runner/Assets.xcassets/AppIcon.appiconset/
                           app_icon_N.png, and windows/runner/resources/app_icon.ico

Needs Pillow.
"""
from PIL import Image, ImageDraw
S = 1024  # drawing grid; everything is downsampled from here

def pier(draw, box, fill):
    """The Wharf mark: a deck on pilings, standing in a wave, fitted to box."""
    import math
    x0, y0, x1, y1 = box
    w, h = x1 - x0, y1 - y0
    u = lambda fx, fy: (x0 + fx * w, y0 + fy * h)
    t = 0.15  # stroke weight, as a fraction of the box — bold enough for 16 px
    # deck, reaching past the pilings like planks do
    draw.rectangle([u(0, 0.10), u(1, 0.10 + t)], fill=fill)
    # two pilings, going down into the water
    for fx in (0.16, 0.84 - t):
        draw.rectangle([u(fx, 0.10), u(fx + t, 0.62)], fill=fill)
    # the water: one full sine period, as a band of even thickness
    wave = lambda fx: 0.80 + 0.07 * math.sin(fx * 2 * math.pi)
    xs = [i / 200 for i in range(201)]
    top = [u(fx, wave(fx) - t / 2) for fx in xs]
    bottom = [u(fx, wave(fx) + t / 2) for fx in reversed(xs)]
    draw.polygon(top + bottom, fill=fill)

def render(size, tile):
    img = Image.new("RGBA", (S, S), (0, 0, 0, 0))
    d = ImageDraw.Draw(img)
    if tile:
        m = S * 0.10 if tile == "app" else 0
        d.rounded_rectangle([m, m, S - m, S - m], radius=(S - 2 * m) * 0.225, fill=(28, 28, 28, 255))
        pad = S * (0.28 if tile == "app" else 0.20)
        pier(d, (pad, pad, S - pad, S - pad), (255, 255, 255, 255))
    else:
        pad = S * 0.06
        pier(d, (pad, pad, S - pad, S - pad), (0, 0, 0, 255))
    return img.resize((size, size), Image.LANCZOS)

if __name__ == "__main__":
    import sys
    out = sys.argv[1]
    import os
    os.makedirs(out, exist_ok=True)
    for n in (16, 32, 64):
        render(n, None).save(f"{out}/tray_template_{n}.png")
        render(n, "tray").save(f"{out}/tray_tile_{n}.png")
    for n in (16, 32, 64, 128, 256, 512, 1024):
        render(n, "app").save(f"{out}/app_{n}.png")
    render(256, "tray").save(f"{out}/tray.ico", sizes=[(16, 16), (24, 24), (32, 32), (48, 48), (64, 64)])
    render(256, "app").save(f"{out}/app.ico", sizes=[(n, n) for n in (16, 24, 32, 48, 64, 128, 256)])
    # A contact sheet to look at: template on light and dark bars, tile, app.
    sheet = Image.new("RGBA", (900, 300), (255, 255, 255, 255))
    dark = Image.new("RGBA", (300, 150), (40, 40, 40, 255))
    sheet.paste(dark, (0, 150))
    for i, n in enumerate((16, 32, 64)):
        t = render(n, None)
        big = t.resize((n * 2, n * 2), Image.NEAREST)
        sheet.alpha_composite(big, (20 + i * 90, 20))
        white = Image.new("RGBA", t.size, (255, 255, 255, 255)); white.putalpha(t.getchannel("A"))
        sheet.alpha_composite(white.resize((n * 2, n * 2), Image.NEAREST), (20 + i * 90, 170))
        tile = render(n, "tray").resize((n * 2, n * 2), Image.NEAREST)
        sheet.alpha_composite(tile, (320 + i * 90, 170))
    sheet.alpha_composite(render(256, "app"), (620, 20))
    sheet.save(f"{out}/sheet.png")
