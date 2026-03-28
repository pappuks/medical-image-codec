#!/usr/bin/env python3
"""
Dev server for the MIC browser demo.

Serves the web/ directory on http://localhost:8080 with the
Cross-Origin-Opener-Policy and Cross-Origin-Embedder-Policy headers
required for SharedArrayBuffer (used by the JS Parallel decoder).

Usage:
  cd web/
  python3 serve.py [port]          # default port: 8080
"""

import http.server
import sys
import os

PORT = int(sys.argv[1]) if len(sys.argv) > 1 else 8080


class CORSHandler(http.server.SimpleHTTPRequestHandler):
    def end_headers(self):
        self.send_header("Cross-Origin-Opener-Policy",   "same-origin")
        self.send_header("Cross-Origin-Embedder-Policy", "require-corp")
        super().end_headers()

    def log_message(self, fmt, *args):
        # Suppress per-request noise; show only errors.
        if int(args[1]) >= 400:
            super().log_message(fmt, *args)


if __name__ == "__main__":
    os.chdir(os.path.dirname(os.path.abspath(__file__)))
    with http.server.HTTPServer(("", PORT), CORSHandler) as httpd:
        print(f"MIC demo server → http://localhost:{PORT}")
        print("Stop with Ctrl-C")
        httpd.serve_forever()
