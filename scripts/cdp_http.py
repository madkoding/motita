"""JSON over HTTP for the verification scripts, on http.client.

The gateway and the browser's debugging port are local services reached by host and port, so the
scripts open the connection themselves instead of handing a computed URL to urllib (which will also
open file: and custom schemes). A status of 400 or more raises, like urllib's urlopen did.
"""
import http.client
import json
import os
import urllib.parse
from typing import Protocol

# A decoded JSON value: what the gateway's API answers and what a page evaluation returns.
type JSON = dict[str, JSON] | list[JSON] | str | int | float | bool | None


class AsyncSocket(Protocol):
    """The two calls the CDP helpers make on an asyncio websocket connection."""

    async def send(self, message: str) -> None:
        """Send one text frame."""

    async def recv(self) -> str | bytes:
        """Wait for the next frame."""


class SyncSocket(Protocol):
    """The two calls the CDP helpers make on a synchronous websocket connection."""

    def send(self, message: str) -> None:
        """Send one text frame."""

    def recv(self) -> str | bytes:
        """Wait for the next frame."""


def fetch_json(url: str, body: JSON = None, headers: dict[str, str] | None = None,
               timeout: float = 10) -> JSON:
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


def chrome_env(chrome: str, port: int | str, profile: str = "", window: str = "") -> dict[str, str]:
    """The environment launch-chrome.sh reads, on top of the caller's own."""
    return {**os.environ, "CHROME": chrome, "CDP_PORT": str(port),
            "CHROME_PROFILE": profile, "CHROME_WINDOW": window}
