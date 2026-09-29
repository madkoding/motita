#!/usr/bin/env python3
"""Forward every request to the simulated model and append its body to a file.

Usage: prompt_proxy.py <listen-port> <upstream-port> <log-file>

It exists so a browser check can assert on what the gateway actually SENT to the model.
"""
import http.server
import sys
import urllib.request

LISTEN, UPSTREAM, LOG = int(sys.argv[1]), int(sys.argv[2]), sys.argv[3]


class Proxy(http.server.BaseHTTPRequestHandler):
    def do_POST(self):
        body = self.rfile.read(int(self.headers.get("Content-Length", 0)))
        with open(LOG, "ab") as f:
            f.write(body + b"\n")
        req = urllib.request.Request(
            f"http://127.0.0.1:{UPSTREAM}{self.path}", data=body,
            headers={"Content-Type": self.headers.get("Content-Type", "application/json")})
        try:
            with urllib.request.urlopen(req, timeout=60) as r:
                data, code, ctype = r.read(), r.status, r.headers.get("Content-Type", "application/json")
        except Exception as e:  # the check reports a dead upstream as a failed run
            data, code, ctype = str(e).encode(), 502, "text/plain"
        self.send_response(code)
        self.send_header("Content-Type", ctype)
        self.send_header("Content-Length", str(len(data)))
        self.end_headers()
        self.wfile.write(data)

    def log_message(self, *a):
        pass


http.server.ThreadingHTTPServer(("127.0.0.1", LISTEN), Proxy).serve_forever()
