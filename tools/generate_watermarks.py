"""Generate the built-in transparent watermark badges used by the preview."""

from pathlib import Path

from PIL import Image, ImageDraw, ImageFont


ROOT = Path(__file__).resolve().parents[1]
OUTPUT = ROOT / "internal" / "web" / "assets" / "watermarks"
FONT = r"C:\Windows\Fonts\msyhbd.ttc"
SCALE = 3
BADGES = [
    ("sub.png", "字幕", "#35B84A"),
    ("ltx.png", "LTX", "#DF171D"),
    ("lada.png", "LADA", "#D46524"),
    ("jav.png", "JAV", "#B82454"),
    ("hack.png", "破解", "#B56C22"),
    ("wuma.png", "无码", "#7354DD"),
    ("youma.png", "有码", "#687383"),
    ("4k.png", "4K", "#35A8D1"),
    ("8k.png", "8K", "#35A8D1"),
]


def generate(name: str, label: str, color: str) -> None:
    font = ImageFont.truetype(FONT, 20 * SCALE)
    box = font.getbbox(label)
    width = 90 * SCALE
    height = 36 * SCALE
    image = Image.new("RGBA", (width, height), (0, 0, 0, 0))
    draw = ImageDraw.Draw(image)
    draw.rounded_rectangle((0, 0, width - 1, height - 1), radius=5 * SCALE, fill=color)
    text_x = (width - (box[2] - box[0])) // 2
    text_y = (height - (box[3] - box[1])) // 2 - box[1]
    draw.text((text_x, text_y), label, font=font, fill="white")
    image.save(OUTPUT / name, optimize=True)


if __name__ == "__main__":
    OUTPUT.mkdir(parents=True, exist_ok=True)
    for values in BADGES:
        generate(*values)
    print(f"Generated {len(BADGES)} watermarks in {OUTPUT}")
