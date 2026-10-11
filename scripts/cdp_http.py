"""JSON over HTTP for the verification scripts, on http.client.

The gateway and the browser's debugging port are local services reached by host and port, so the
scripts open the connection themselves instead of handing a computed URL to urllib (which will also
open file: and custom schemes). A status of 400 or more raises, like urllib's urlopen did.
"""
import http.client
import json
import os
import urllib.parse


def fetch_json(url, body=None, headers=None, timeout=10):
    """GET url (or POST body as JSON when body is not None) and return the decoded JSON answer."""
    parts = urllib.parse.urlsplit(url)
    if parts.scheme not in ("http", "https") or not parts.hostname:
        raise ValueError(f"not an http(s) URL: {url!r}")
    cls = http.client.HTTPSConnection if parts.scheme == "https" else http.client.HTTPConnection
    conn = cls(parts.hostname, parts.port, timeout=timeout)
    try:
        data = json.dumps(body).encode() if body is not None else None
        conn.request("POST" if body is not None else "GET",
                     parts.path + ("?" + parts.query if parts.query else ""),
                     body=data, headers=headers or {})
        r = conn.getresponse()
        raw = r.read()
        if r.status >= 400:
            raise OSError(f"HTTP Error {r.status}: {r.reason}")
        return json.loads(raw)
    finally:
        conn.close()


# The launcher takes the browser, the port and the profile from the environment, never from argv.
LAUNCHER = os.path.join(os.path.dirname(os.path.abspath(__file__)), "launch-chrome.sh")


def chrome_env(chrome, port, profile="", window=""):
    """The environment launch-chrome.sh reads, on top of the caller's own."""
    return {**os.environ, "CHROME": chrome, "CDP_PORT": str(port),
            "CHROME_PROFILE": profile, "CHROME_WINDOW": window}
