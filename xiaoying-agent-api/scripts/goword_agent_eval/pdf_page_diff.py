import argparse
import hashlib
import json
import sys
from pathlib import Path

try:
    import pypdfium2 as pdfium
except ImportError as exc:
    raise SystemExit("missing dependency: pypdfium2") from exc

try:
    from PIL import Image, ImageChops
except ImportError as exc:
    raise SystemExit("missing dependency: Pillow") from exc


def sha256_file(path: Path) -> str:
    digest = hashlib.sha256()
    with path.open("rb") as handle:
        for chunk in iter(lambda: handle.read(65536), b""):
            digest.update(chunk)
    return digest.hexdigest()


def render_pdf(pdf_path: Path, output_dir: Path, prefix: str) -> list[dict]:
    document = pdfium.PdfDocument(str(pdf_path))
    pages: list[dict] = []
    try:
        for page_index in range(len(document)):
            page = document[page_index]
            try:
                image = page.render(scale=2.0).to_pil()
            finally:
                page.close()
            image_path = output_dir / f"{prefix}-page-{page_index + 1:03d}.png"
            image.save(image_path)
            pages.append(
                {
                    "page_index": page_index,
                    "png_path": str(image_path),
                    "width": image.width,
                    "height": image.height,
                    "sha256": sha256_file(image_path),
                }
            )
    finally:
        document.close()
    return pages


def normalize_pair(left_path: Path, right_path: Path) -> tuple[Image.Image, Image.Image, int, int]:
    left = Image.open(left_path).convert("RGBA")
    right = Image.open(right_path).convert("RGBA")
    width = max(left.width, right.width)
    height = max(left.height, right.height)
    if left.size != (width, height):
        canvas = Image.new("RGBA", (width, height), (255, 255, 255, 255))
        canvas.paste(left, (0, 0))
        left = canvas
    if right.size != (width, height):
        canvas = Image.new("RGBA", (width, height), (255, 255, 255, 255))
        canvas.paste(right, (0, 0))
        right = canvas
    return left, right, width, height


def diff_pages(left_page: dict, right_page: dict, output_dir: Path) -> dict:
    left_image, right_image, width, height = normalize_pair(Path(left_page["png_path"]), Path(right_page["png_path"]))
    diff = ImageChops.difference(left_image, right_image)
    mask = diff.convert("L").point(lambda value: 255 if value else 0)
    histogram = mask.histogram()
    pixel_diff_count = int(sum(histogram[1:]))
    pixel_total = width * height if width > 0 and height > 0 else 0
    pixel_diff_ratio = float(pixel_diff_count) / float(pixel_total) if pixel_total else 0.0
    diff_path = ""
    if pixel_diff_count > 0:
        diff_file = output_dir / f"diff-page-{left_page['page_index'] + 1:03d}.png"
        diff.save(diff_file)
        diff_path = str(diff_file)
    return {
        "page_index": left_page["page_index"],
        "left_png_path": left_page["png_path"],
        "right_png_path": right_page["png_path"],
        "diff_png_path": diff_path,
        "width": width,
        "height": height,
        "left_sha256": left_page["sha256"],
        "right_sha256": right_page["sha256"],
        "pixel_diff_count": pixel_diff_count,
        "pixel_diff_ratio": pixel_diff_ratio,
        "images_identical": pixel_diff_count == 0,
    }


def main() -> int:
    parser = argparse.ArgumentParser(description="Render two PDFs to page images and diff them.")
    parser.add_argument("--left-pdf", required=True)
    parser.add_argument("--right-pdf", required=True)
    parser.add_argument("--output-dir", required=True)
    parser.add_argument("--report-json", required=True)
    args = parser.parse_args()

    output_dir = Path(args.output_dir)
    left_dir = output_dir / "left-pages"
    right_dir = output_dir / "right-pages"
    left_dir.mkdir(parents=True, exist_ok=True)
    right_dir.mkdir(parents=True, exist_ok=True)

    left_pdf = Path(args.left_pdf)
    right_pdf = Path(args.right_pdf)
    left_pages = render_pdf(left_pdf, left_dir, "left")
    right_pages = render_pdf(right_pdf, right_dir, "right")
    compared_count = min(len(left_pages), len(right_pages))
    page_diffs = [diff_pages(left_pages[index], right_pages[index], output_dir) for index in range(compared_count)]
    report = {
        "left_pdf_path": str(left_pdf),
        "right_pdf_path": str(right_pdf),
        "left_page_count": len(left_pages),
        "right_page_count": len(right_pages),
        "compared_page_count": compared_count,
        "all_pages_identical": len(left_pages) == len(right_pages) and all(page["images_identical"] for page in page_diffs),
        "left_pages": left_pages,
        "right_pages": right_pages,
        "pages": page_diffs,
    }

    report_path = Path(args.report_json)
    report_path.parent.mkdir(parents=True, exist_ok=True)
    report_path.write_text(json.dumps(report, ensure_ascii=False, indent=2), encoding="utf-8")
    return 0


if __name__ == "__main__":
    sys.exit(main())