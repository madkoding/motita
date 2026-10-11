#!/usr/bin/env python3
"""Forward every request to the simulated model and append its body to a file.

Usage: prompt_proxy.py <listen-port> <upstream-port> <log-file>

It exists so a browser check can assert on what the gateway actually SENT to the model.
"""
import http.client
import http.server
import sys

LISTEN, UPSTREAM, LOG = int(sys.argv[1]), int(sys.argv[2]), sys.argv[3]


class Proxy(http.server.BaseHTTPRequestHandler):
    def do_POST(self):
        body = self.rfile.read(int(self.headers.get("Content-Length", 0)))
        with open(LOG, "ab") as f:
            f.write(body + b"\n")
        try:
            # The upstream is the simulated model on loopback: plain HTTP by design, so the
            # connection is opened by host and port rather than from a URL string.
            conn = http.client.HTTPConnection("127.0.0.1", UPSTREAM, timeout=60)
            try:
                conn.request("POST", self.path, body=body, headers={
                    "Content-Type": self.headers.get("Content-Type", "application/json")})
                r = conn.getresponse()
                data, code, ctype = r.read(), r.status, r.headers.get("Content-Type", "application/json")
            finally:
                conn.close()
            if code >= 400:
                raise OSError(f"HTTP Error {code}: {r.reason}")
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
