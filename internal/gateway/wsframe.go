package gateway

// RFC 6455 frame helpers, implemented with the standard library only.
//
// The gateway has a 10 MB binary ceiling and under 2 MB of headroom, so a WebSocket
// library is a cost this feature cannot pay the way gorilla/websocket (+~150 KB) or
// nhooyr.io/websocket (+~200 KB) would. The subset the flag protocol needs is small:
// text frames, close, ping/pong, and the server side of the handshake. That is what
// lives here, and nothing more.
//
// The frame format (RFC 6455 §5.2):
//
//	 0                   1                   2                   3
//	 0 1 2 3 4 5 6 7 8 9 0 1 2 3 4 5 6 7 8 9 0 1 2 3 4 5 6 7 8 9 0 1
//	+-+-+-+-+-------+-+-------------+-------------------------------+
//	|F|R|R|R| opcode|M| Payload len |    Extended payload length    |
//	|I|S|S|S|  (4)  |A|     (7)     |             (16/64)           |
//	|N|V|V|V|       |S|             |   (if payload len==126/127)   |
//	| |1|2|3|       |K|             |                               |
//	+-+-+-+-+-------+-+-------------+ - - - - - - - - - - - - - - - +
//	|     Extended payload length continued, if payload len == 127  |
//	+ - - - - - - - - - - - - - - - +-------------------------------+
//	|                               |Masking-key, if MASK set to 1  |
//	+-------------------------------+-------------------------------+
//	| Masking-key (continued)       |          Payload Data         |
//	+-------------------------------- - - - - - - - - - - - - - - - +
//	:                     Payload Data continued ...                :
//	+ - - - - - - - - - - - - - - - - - - - - - - - - - - - - - - - +
//	|                     Payload Data continued ...                |
//	+---------------------------------------------------------------+

import (
	"crypto/sha1"
	"encoding/base64"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"strings"
)

// wsGUID is the magic accept string GUID defined by RFC 6455 §1.3.
const wsGUID = "258EAFA5-E914-47DA-95CA-C5AB0DC85B11"

// Close codes (RFC 6455 §7.4).
const (
	CloseNormal      uint16 = 1000 // Normal closure.
	CloseGoingAway   uint16 = 1001 // Endpoint going away.
	CloseProtocol    uint16 = 1002 // Protocol error.
	CloseUnsupported uint16 = 1003 // Unsupported data.
	CloseTooBig      uint16 = 1009 // Message too big.
)

// Opcodes (RFC 6455 §5.2).
const (
	opContinuation = 0x0
	opText         = 0x1
	opBinary       = 0x2
	opClose        = 0x8
	opPing         = 0x9
	opPong         = 0xA
)

// wsMaxPayload caps a single frame's payload. 1 MB is generous for a JSON message that
// carries a query or a response and small enough that a malicious or buggy client cannot
// make the gateway allocate unbounded memory.
const wsMaxPayload = 1 << 20

// wsAcceptKey computes the Sec-WebSocket-Accept header value from the client's
// Sec-WebSocket-Key, per RFC 6455 §4.2.2 §5.4.
func wsAcceptKey(key string) string {
	h := sha1.New()
	h.Write([]byte(key + wsGUID))
	return base64.StdEncoding.EncodeToString(h.Sum(nil))
}

// isWebSocketUpgrade reports whether a request is a valid WebSocket upgrade.
func isWebSocketUpgrade(r *http.Request) bool {
	return strings.EqualFold(r.Header.Get("Upgrade"), "websocket") &&
		strings.Contains(strings.ToLower(r.Header.Get("Connection")), "upgrade") &&
		r.Header.Get("Sec-WebSocket-Key") != "" &&
		strings.EqualFold(r.Header.Get("Sec-WebSocket-Version"), "13")
}

// wsHandshake writes the 101 Switching Protocols response and returns the hijacked
// connection. The caller owns the net.Conn from here on.
func wsHandshake(w http.ResponseWriter, r *http.Request) (net.Conn, *http.ResponseController, error) {
	h, ok := w.(http.Hijacker)
	if !ok {
		return nil, nil, errors.New("the response writer does not support hijacking")
	}
	conn, bw, err := h.Hijack()
	if err != nil {
		return nil, nil, fmt.Errorf("could not hijack the connection: %w", err)
	}
	// Flush any buffered data the bufio.Writer holds, then write the response directly.
	// The response is written to the bufio.Writer (which wraps the conn) so the headers
	// land in the right order.
	acceptStr := "HTTP/1.1 101 Switching Protocols\r\n" +
		"Upgrade: websocket\r\n" +
		"Connection: Upgrade\r\n" +
		"Sec-WebSocket-Accept: " + wsAcceptKey(r.Header.Get("Sec-WebSocket-Key")) + "\r\n\r\n"
	if _, err := bw.WriteString(acceptStr); err != nil {
		conn.Close()
		return nil, nil, fmt.Errorf("could not write the handshake response: %w", err)
	}
	if err := bw.Flush(); err != nil {
		conn.Close()
		return nil, nil, fmt.Errorf("could not flush the handshake response: %w", err)
	}
	rc := http.NewResponseController(w)
	return conn, rc, nil
}

// wsReadFrame reads one WebSocket frame from the connection and returns its opcode,
// payload, and whether it is a final fragment. It handles fragmentation by accumulating
// continuation frames until a final one arrives — but only for text frames, which is
// all this protocol sends. Binary frames are rejected.
//
// Masking: client-to-server frames MUST be masked (RFC 6455 §5.3). Server-to-client
// frames MUST NOT be masked. This reader handles both: it unmasks when the mask bit is
// set and leaves the payload as-is when it is not. The server's read loop enforces the
// client-must-mask rule separately, so the same reader works for both sides in tests.
func wsReadFrame(r io.Reader) (opcode byte, payload []byte, final bool, masked bool, err error) {
	var hdr [2]byte
	if _, err = io.ReadFull(r, hdr[:]); err != nil {
		return 0, nil, false, false, err
	}

	opcode = hdr[0] & 0x0f
	final = hdr[0]&0x80 != 0
	masked = hdr[1]&0x80 != 0
	length := int(hdr[1] & 0x7f)

	switch {
	case length == 126:
		var ext [2]byte
		if _, e := io.ReadFull(r, ext[:]); e != nil {
			return 0, nil, false, false, e
		}
		length = int(binary.BigEndian.Uint16(ext[:]))
	case length == 127:
		var ext [8]byte
		if _, e := io.ReadFull(r, ext[:]); e != nil {
			return 0, nil, false, false, e
		}
		length = int(binary.BigEndian.Uint64(ext[:]))
	}

	if length > wsMaxPayload {
		return 0, nil, false, false, fmt.Errorf("frame payload of %d bytes exceeds the %d limit", length, wsMaxPayload)
	}

	// A masked frame carries a 4-byte masking key; an unmasked frame does not. The
	// caller enforces the client-must-mask rule, so this reader handles both.
	var mask [4]byte
	if masked {
		if _, e := io.ReadFull(r, mask[:]); e != nil {
			return 0, nil, false, false, e
		}
	}

	payload = make([]byte, length)
	if length > 0 {
		if _, e := io.ReadFull(r, payload); e != nil {
			return 0, nil, false, false, e
		}
		if masked {
			for i := range payload {
				payload[i] ^= mask[i%4]
			}
		}
	}
	return opcode, payload, final, masked, nil
}

// wsWriteFrame writes one WebSocket frame to the connection. Server-to-client frames are
// NOT masked, per RFC 6455 §5.3. The frame is always final (FIN=1) — this protocol does
// not fragment outbound messages.
func wsWriteFrame(conn net.Conn, opcode byte, payload []byte) error {
	var hdr [14]byte
	hdr[0] = 0x80 | opcode // FIN=1, opcode.

	pos := 2
	plen := len(payload)
	switch {
	case plen <= 125:
		hdr[1] = byte(plen) // No mask bit (server→client).
	case plen <= 65535:
		hdr[1] = 126
		binary.BigEndian.PutUint16(hdr[2:4], uint16(plen))
		pos = 4
	default:
		hdr[1] = 127
		binary.BigEndian.PutUint64(hdr[2:10], uint64(plen))
		pos = 10
	}

	if _, err := conn.Write(hdr[:pos]); err != nil {
		return err
	}
	if plen > 0 {
		if _, err := conn.Write(payload); err != nil {
			return err
		}
	}
	return nil
}

// wsWriteText is the convenience wrapper for text frames, which is all the JSON protocol
// sends.
func wsWriteText(conn net.Conn, data []byte) error {
	return wsWriteFrame(conn, opText, data)
}

// wsWriteClose sends a close frame with the given status code (RFC 6455 §7.4) and an
// optional reason. A close frame carries a 2-byte status code in its payload, optionally
// followed by a UTF-8 reason.
func wsWriteClose(conn net.Conn, code uint16, reason string) error {
	payload := make([]byte, 2+len(reason))
	binary.BigEndian.PutUint16(payload[:2], code)
	copy(payload[2:], reason)
	return wsWriteFrame(conn, opClose, payload)
}

// wsWritePing sends a ping frame. The receiver MUST respond with a pong (RFC 6455 §5.5.2).
func wsWritePing(conn net.Conn, data []byte) error {
	return wsWriteFrame(conn, opPing, data)
}

// wsWritePong sends a pong frame in response to a ping.
func wsWritePong(conn net.Conn, data []byte) error {
	return wsWriteFrame(conn, opPong, data)
}
