#!/usr/bin/env python3
"""Forward every request to the simulated model and append its body to a file.

Usage: prompt_proxy.py <listen-port> <upstream-port> <log-file>

It exists so a browser check can assert on what the gateway actually SENT to the model.
Both ends are loopback and plain HTTP by design (the simulated model has no certificate), so the
proxy speaks the protocol itself over asyncio streams instead of going through a server class.
"""
import asyncio
import http.client
import sys

LISTEN, UPSTREAM, LOG = int(sys.argv[1]), int(sys.argv[2]), sys.argv[3]
LOOPBACK = "127.0.0.1"


def forward(path, body, ctype):
    """One POST to the upstream; returns (data, status, content-type). Any failure is a 502."""
    try:
        conn = http.client.HTTPConnection(LOOPBACK, UPSTREAM, timeout=60)
        try:
            conn.request("POST", path, body=body, headers={"Content-Type": ctype})
            r = conn.getresponse()
            data, code = r.read(), r.status
            ctype = r.headers.get("Content-Type", "application/json")
            reason = r.reason
        finally:
            conn.close()
        if code >= 400:
            raise OSError(f"HTTP Error {code}: {reason}")
        return data, code, ctype
    except Exception as e:  # the check reports a dead upstream as a failed run
        return str(e).encode(), 502, "text/plain"


def append_log(body):
    with open(LOG, "ab") as f:
        f.write(body + b"\n")


async def handle(reader, writer):
    try:
        head = await reader.readuntil(b"\r\n\r\n")
        lines = head.decode("latin-1").split("\r\n")
        path = lines[0].split(" ")[1]
        headers = {}
        for line in lines[1:]:
            name, _, value = line.partition(":")
            if name:
                headers[name.strip().lower()] = value.strip()
        body = await reader.readexactly(int(headers.get("content-length", 0)))
        await asyncio.to_thread(append_log, body)
        data, code, ctype = await asyncio.to_thread(
            forward, path, body, headers.get("content-type", "application/json"))
        writer.write(
            f"HTTP/1.1 {code} {http.client.responses.get(code, 'Status')}\r\n"
            f"Content-Type: {ctype}\r\nContent-Length: {len(data)}\r\nConnection: close\r\n\r\n"
            .encode("latin-1") + data)
        await writer.drain()
    except (asyncio.IncompleteReadError, ConnectionError, IndexError, ValueError):
        pass  # a malformed or dropped request: nothing to answer
    finally:
        writer.close()


async def main():
    server = await asyncio.start_server(handle, LOOPBACK, LISTEN)
    async with server:
        await server.serve_forever()


asyncio.run(main())
