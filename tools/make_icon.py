"""Regenerates the app icon (needs: pip install pillow numpy).
Run from the repository root:  python tools/make_icon.py
"""
from PIL import Image, ImageDraw, ImageFilter, ImageChops
import numpy as np, math

S = 2048          # supersampled canvas; downscaled at the end
k = S / 1024.0

def grad(size, top, bottom):
    w, h = size
    t = np.linspace(0, 1, h)[:, None, None]
    top, bottom = np.array(top, float), np.array(bottom, float)
    arr = (top * (1 - t) + bottom * t) * np.ones((1, w, 1))
    return Image.fromarray(arr.astype("uint8"), "RGB").convert("RGBA")

def rr_mask(box, r):
    m = Image.new("L", (S, S), 0)
    ImageDraw.Draw(m).rounded_rectangle([v * k for v in box], radius=r * k, fill=255)
    return m

def paste_grad(img, mask, top, bottom, box):
    x0, y0, x1, y1 = [int(v * k) for v in box]
    g = Image.new("RGBA", (S, S), (0, 0, 0, 0))
    g.paste(grad((x1 - x0, y1 - y0), top, bottom), (x0, y0))
    img.paste(g, (0, 0), mask)

# ---- background: deep indigo squircle with a soft glow ----
icon = Image.new("RGBA", (S, S), (0, 0, 0, 0))
bg_mask = rr_mask((0, 0, 1024, 1024), 232)
paste_grad(icon, bg_mask, (38, 47, 92), (11, 13, 28), (0, 0, 1024, 1024))
glow = Image.new("RGBA", (S, S), (0, 0, 0, 0))
ImageDraw.Draw(glow).ellipse([int(120 * k), int(-260 * k), int(900 * k), int(420 * k)], fill=(120, 150, 255, 70))
glow = glow.filter(ImageFilter.GaussianBlur(120 * k))
icon = Image.alpha_composite(icon, Image.composite(glow, Image.new("RGBA", (S, S), (0, 0, 0, 0)), bg_mask))

# ---- video lane: two clips separated by a slanted cut ----
vbox = (120, 290, 904, 520)
vmask = rr_mask(vbox, 46)
gap = Image.new("L", (S, S), 0)
ImageDraw.Draw(gap).polygon([(p[0] * k, p[1] * k) for p in
                             [(575, 270), (625, 270), (565, 540), (515, 540)]], fill=255)
vmask = ImageChops.subtract(vmask, gap)

# soft shadow under the lanes
sh = Image.new("L", (S, S), 0)
ImageDraw.Draw(sh).rounded_rectangle([int(130 * k), int(310 * k), int(914 * k), int(790 * k)], radius=int(50 * k), fill=150)
sh = sh.filter(ImageFilter.GaussianBlur(26 * k))
shadow = Image.new("RGBA", (S, S), (0, 0, 0, 0))
shadow.putalpha(sh)
icon = Image.alpha_composite(icon, shadow)

paste_grad(icon, vmask, (96, 160, 255), (46, 92, 168), vbox)

# film-strip style tick marks on the video clips
ticks = Image.new("L", (S, S), 0)
td = ImageDraw.Draw(ticks)
for x in range(150, 890, 44):
    td.rounded_rectangle([x * k, 312 * k, (x + 14) * k, 334 * k], radius=4 * k, fill=70)
    td.rounded_rectangle([x * k, 476 * k, (x + 14) * k, 498 * k], radius=4 * k, fill=70)
white = Image.new("RGBA", (S, S), (255, 255, 255, 255))
icon.paste(white, (0, 0), ImageChops.multiply(ticks, vmask))

# ---- audio lane with a waveform ----
abox = (120, 580, 904, 760)
amask = rr_mask(abox, 46)
paste_grad(icon, amask, (84, 214, 160), (36, 128, 92), abox)
wave = Image.new("L", (S, S), 0)
wd = ImageDraw.Draw(wave)
cy = (abox[1] + abox[3]) / 2
for i, x in enumerate(range(150, 880, 26)):
    h = 14 + 62 * abs(math.sin(i * 0.55)) * (0.55 + 0.45 * math.sin(i * 0.17 + 1.2) ** 2)
    wd.rounded_rectangle([x * k, (cy - h) * k, (x + 12) * k, (cy + h) * k], radius=6 * k, fill=215)
icon.paste(white, (0, 0), ImageChops.multiply(wave, amask))

# ---- playhead: coral line with a rounded handle, glowing ----
px = 610
glow2 = Image.new("RGBA", (S, S), (0, 0, 0, 0))
gd = ImageDraw.Draw(glow2)
gd.rounded_rectangle([(px - 16) * k, 200 * k, (px + 16) * k, 840 * k], radius=16 * k, fill=(255, 90, 79, 200))
gd.ellipse([(px - 62) * k, 150 * k, (px + 62) * k, 274 * k], fill=(255, 90, 79, 200))
glow2 = glow2.filter(ImageFilter.GaussianBlur(26 * k))
icon = Image.alpha_composite(icon, glow2)

ph = Image.new("L", (S, S), 0)
pd = ImageDraw.Draw(ph)
pd.rounded_rectangle([(px - 11) * k, 205 * k, (px + 11) * k, 835 * k], radius=11 * k, fill=255)
pd.rounded_rectangle([(px - 50) * k, 160 * k, (px + 50) * k, 244 * k], radius=26 * k, fill=255)
pd.polygon([((px - 36) * k, 232 * k), ((px + 36) * k, 232 * k), (px * k, 288 * k)], fill=255)
paste_grad(icon, ph, (255, 128, 112), (255, 70, 62), (px - 60, 150, px + 60, 840))
# little highlight on the handle
hl = Image.new("L", (S, S), 0)
ImageDraw.Draw(hl).rounded_rectangle([(px - 34) * k, 172 * k, (px + 20) * k, 190 * k], radius=9 * k, fill=110)
icon.paste(white, (0, 0), ImageChops.multiply(hl, ph))

# ---- subtle glass edge ----
edge = ImageChops.subtract(bg_mask, bg_mask.filter(ImageFilter.MinFilter(int(6 * k) | 1)))
rim = Image.new("RGBA", (S, S), (255, 255, 255, 38))
icon.paste(rim, (0, 0), edge)

out = icon.resize((1024, 1024), Image.LANCZOS)
out.resize((512, 512), Image.LANCZOS).save("cmd/minicut/icon.png")
out.save("assets/windows/icon.ico", format="ICO", sizes=[(16, 16), (24, 24), (32, 32), (48, 48), (64, 64), (128, 128), (256, 256)])
print("ok")
