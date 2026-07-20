from __future__ import annotations

import argparse
import http.server
import os
import socketserver
import threading
from pathlib import Path

from playwright.sync_api import sync_playwright


class SilentHandler(http.server.SimpleHTTPRequestHandler):
    def log_message(self, format: str, *args) -> None:
        return


def serve_directory(directory: Path, port: int) -> socketserver.TCPServer:
    handler = lambda *args, **kwargs: SilentHandler(*args, directory=str(directory), **kwargs)
    server = socketserver.TCPServer(("127.0.0.1", port), handler)
    thread = threading.Thread(target=server.serve_forever, daemon=True)
    thread.start()
    return server


def main() -> None:
    parser = argparse.ArgumentParser(description="Render an Excalidraw file to PNG")
    parser.add_argument("input", help="Path to the .excalidraw file")
    parser.add_argument("output", help="Path to the output PNG")
    parser.add_argument("--port", type=int, default=8765, help="Local HTTP port")
    args = parser.parse_args()

    input_path = Path(args.input).resolve()
    output_path = Path(args.output).resolve()
    template_path = Path(__file__).resolve().parent / "render_template.html"

    if not input_path.exists():
        raise SystemExit(f"Input file not found: {input_path}")

    staging_dir = Path(__file__).resolve().parent / ".render-staging"
    staging_dir.mkdir(exist_ok=True)

    staged_diagram = staging_dir / input_path.name
    staged_template = staging_dir / "render_template.html"
    staged_diagram.write_text(input_path.read_text(encoding="utf-8"), encoding="utf-8")
    staged_template.write_text(template_path.read_text(encoding="utf-8"), encoding="utf-8")

    server = serve_directory(staging_dir, args.port)
    try:
        with sync_playwright() as playwright:
            browser = playwright.chromium.launch()
            page = browser.new_page(viewport={"width": 1800, "height": 1200}, device_scale_factor=2)
            url = f"http://127.0.0.1:{args.port}/render_template.html?diagram=/{staged_diagram.name}"
            page.goto(url, wait_until="networkidle")
            page.wait_for_function("window.__EXCALIDRAW_READY__ === true")
            page.screenshot(path=str(output_path), full_page=True)
            browser.close()
    finally:
        server.shutdown()
        server.server_close()


if __name__ == "__main__":
    main()