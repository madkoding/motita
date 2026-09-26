package gateway

// The frame and handshake paths of wsframe.go that ws_test.go does not reach.
//
// This is the RFC 6455 layer, and it is where a mistake is a SECURITY mistake rather than a wrong
// answer: a length field read into the wrong width, a payload cap that does not hold, a mask that
// is not applied. The frames are built byte by byte here on purpose - a fixture that used
// wsWriteFrame to produce what wsReadFrame reads would agree with itself while both are wrong.

import (
	"bytes"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// wsFrameBytes builds one frame byte by byte. Masking is explicit: a client frame MUST be masked
// (RFC 6455 §5.3) and the server's MUST NOT be, so a helper that decided this for the caller could
// not build the illegal frame a test needs in order to prove the rule is enforced.
func wsFrameBytes(opcode byte, payload []byte, final, masked bool, mask [4]byte) []byte {
	var buf bytes.Buffer
	b0 := opcode
	if final {
		b0 |= 0x80
	}
	buf.WriteByte(b0)

	n := len(payload)
	maskBit := byte(0)
	if masked {
		maskBit = 0x80
	}
	switch {
	case n <= 125:
		buf.WriteByte(maskBit | byte(n))
	case n <= 65535:
		buf.WriteByte(maskBit | 126)
		buf.WriteByte(byte(n >> 8))
		buf.WriteByte(byte(n))
	default:
		buf.WriteByte(maskBit | 127)
		for shift := 56; shift >= 0; shift -= 8 {
			buf.WriteByte(byte(uint64(n) >> uint(shift)))
		}
	}
	if masked {
		buf.Write(mask[:])
	}
	for i, c := range payload {
		if masked {
			c ^= mask[i%4]
		}
		buf.WriteByte(c)
	}
	return buf.Bytes()
}

// A masked client frame comes back unmasked and intact, whatever its length. The three length
// encodings are the part worth pinning: a payload of 126 bytes takes the 16-bit form and one of
// 65536 takes the 64-bit form, and reading a field into the wrong width is how a frame parser
// starts corrupting the stream instead of failing.
func TestWsReadFrameHandlesEveryLengthForm(t *testing.T) {
	cases := []struct {
		name string
		size int
	}{
		{"the 7-bit form", 5},
		{"the 16-bit form", 300},
		{"the 64-bit form", 70000},
	}
	mask := [4]byte{0x37, 0xfa, 0x21, 0x3d}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			payload := bytes.Repeat([]byte{'z'}, tc.size)
			client, server := net.Pipe()
			defer client.Close()
			defer server.Close()
			go func() {
				_, _ = client.Write(wsFrameBytes(opText, payload, true, true, mask))
			}()

			server.SetReadDeadline(time.Now().Add(5 * time.Second))
			opcode, got, final, masked, err := wsReadFrame(server)
			if err != nil {
				t.Fatalf("wsReadFrame: %v", err)
			}
			if opcode != opText || !final || !masked {
				t.Errorf("opcode=%x final=%v masked=%v", opcode, final, masked)
			}
			if !bytes.Equal(got, payload) {
				t.Errorf("payload of %d bytes came back as %d", len(payload), len(got))
			}
		})
	}
}

// A frame whose declared payload exceeds the cap is refused BEFORE the allocation, with an error
// that names both numbers: this is the guard that stops a client from making the gateway allocate
// whatever it likes.
func TestWsReadFrameRefusesAnOversizedPayload(t *testing.T) {
	client, server := net.Pipe()
	defer client.Close()
	defer server.Close()

	// The header alone claims wsMaxPayload+1 bytes in the 64-bit form. Nothing else is written,
	// which is the point: the refusal must come from the declared length.
	hdr := []byte{0x81, 0x80 | 127}
	for shift := 56; shift >= 0; shift -= 8 {
		hdr = append(hdr, byte(uint64(wsMaxPayload+1)>>uint(shift)))
	}
	go func() { _, _ = client.Write(hdr) }()

	server.SetReadDeadline(time.Now().Add(5 * time.Second))
	_, _, _, _, err := wsReadFrame(server)
	if err == nil {
		t.Fatal("a payload beyond the cap must be refused")
	}
	if !strings.Contains(err.Error(), "exceeds") {
		t.Errorf("the refusal must say what was exceeded, got %q", err)
	}
	if !strings.Contains(err.Error(), "1048576") {
		t.Errorf("the refusal must name the limit, got %q", err)
	}
}

// An empty payload is legal (a close frame with no code) and must not be mistaken for end-of-stream
// or for a frame that was skipped.
func TestWsReadFrameHandlesAnEmptyPayload(t *testing.T) {
	client, server := net.Pipe()
	defer client.Close()
	defer server.Close()
	go func() { _, _ = client.Write(wsFrameBytes(opPing, nil, true, true, [4]byte{})) }()

	server.SetReadDeadline(time.Now().Add(2 * time.Second))
	opcode, payload, _, _, err := wsReadFrame(server)
	if err != nil {
		t.Fatalf("wsReadFrame: %v", err)
	}
	if opcode != opPing {
		t.Errorf("opcode = %x, want a ping", opcode)
	}
	if len(payload) != 0 {
		t.Errorf("payload = %q, want empty", payload)
	}
}

// A truncated frame is an error rather than a partial frame: a reader that trusted a half-sent
// header would treat the rest of the stream as payload and never recover.
func TestWsReadFrameRefusesATruncatedFrame(t *testing.T) {
	cases := map[string][]byte{
		"half a header":     {0x81},
		"a short length":    {0x81, 0x80 | 126, 0x00},          // claims 16-bit length, sends one byte
		"a missing mask":    {0x81, 0x80 | 126, 0x00, 0x05},    // length and no masking key
		"a short payload":   {0x81, 0x84, 1, 2, 3, 4, 'a'},     // says 4 bytes, sends 1
		"a short long-form": {0x81, 0x80 | 127, 0, 0, 0, 0, 0}, // claims 64-bit length, sends four bytes
	}
	for name, frame := range cases {
		t.Run(name, func(t *testing.T) {
			_, _, _, _, err := wsReadFrame(bytes.NewReader(frame))
			if err == nil {
				t.Fatal("a truncated frame must be an error")
			}
			if !errors.Is(err, io.ErrUnexpectedEOF) && !errors.Is(err, io.EOF) {
				t.Errorf("err = %v, want an end-of-stream error", err)
			}
		})
	}
}

// The accept key is a sha1 of the client key plus the RFC's GUID. Pinned against the value from
// RFC 6455 §1.3, because a handshake that computes the wrong key fails in every browser and in no
// unit test that computes it the same way twice.
func TestWsAcceptKeyMatchesTheRFCExample(t *testing.T) {
	const rfcKey = "dGhlIHNhbXBsZSBub25jZQ=="
	const rfcAccept = "s3pPLMBiTxaQ9kYGzzhZRbK+xOo="
	if got := wsAcceptKey(rfcKey); got != rfcAccept {
		t.Errorf("wsAcceptKey(%q) = %q, want %q", rfcKey, got, rfcAccept)
	}
}

// isWebSocketUpgrade demands every part of the upgrade, and a missing one is a plain HTTP request.
// Any of these gaps being tolerated is how an unauthenticated plain request reaches a handler that
// assumes a hijacked connection.
func TestIsWebSocketUpgradeDemandsEveryHeader(t *testing.T) {
	build := func(upgrade, connection, key, version string) *http.Request {
		r := httptest.NewRequest(http.MethodGet, "/v1/ws", nil)
		r.Header.Set("Upgrade", upgrade)
		r.Header.Set("Connection", connection)
		if key != "" {
			r.Header.Set("Sec-WebSocket-Key", key)
		}
		r.Header.Set("Sec-WebSocket-Version", version)
		return r
	}
	cases := []struct {
		name string
		req  *http.Request
		want bool
	}{
		{"a complete upgrade", build("websocket", "Upgrade", "abc", "13"), true},
		{"the header names are case-insensitive", build("WebSocket", "upgrade", "abc", "13"), true},
		{"Connection carries a list", build("websocket", "keep-alive, Upgrade", "abc", "13"), true},
		{"no key", build("websocket", "Upgrade", "", "13"), false},
		{"the wrong version", build("websocket", "Upgrade", "abc", "8"), false},
		{"no upgrade header", build("", "Upgrade", "abc", "13"), false},
		{"a plain request", build("", "keep-alive", "", "13"), false},
	}
	for _, tc := range cases {
		if got := isWebSocketUpgrade(tc.req); got != tc.want {
			t.Errorf("%s: isWebSocketUpgrade = %v, want %v", tc.name, got, tc.want)
		}
	}
}

// wsWriteFrame picks the length encoding from the payload it is given, and a frame read back must
// carry the same bytes: the sizes around each boundary are where an off-by-one lives.
func TestWsWriteFrameRoundTripsAcrossTheLengthBoundaries(t *testing.T) {
	for _, size := range []int{0, 1, 125, 126, 127, 65535, 65536} {
		payload := bytes.Repeat([]byte{'p'}, size)
		client, server := net.Pipe()
		go func() {
			if err := wsWriteFrame(server, opText, payload); err != nil {
				t.Errorf("wsWriteFrame(%d): %v", size, err)
			}
			_ = server.Close()
		}()

		// Read the frame as a client does: unmasked, with a real server on the other end.
		opcode, got, final, masked, err := wsReadFrame(client)
		_ = client.Close()
		if err != nil {
			t.Fatalf("size %d: wsReadFrame: %v", size, err)
		}
		if opcode != opText {
			t.Errorf("size %d: opcode = %x", size, opcode)
		}
		if !final {
			t.Errorf("size %d: outbound frames are always final", size)
		}
		if masked {
			t.Errorf("size %d: server-to-client frames must NOT be masked (RFC 6455 §5.3)", size)
		}
		if !bytes.Equal(got, payload) {
			t.Errorf("size %d came back as %d bytes", size, len(got))
		}
	}
}

// A close frame carries its code as a 16-bit big-endian number followed by an optional reason.
func TestWsWriteCloseCarriesTheCodeAndTheReason(t *testing.T) {
	client, server := net.Pipe()
	go func() {
		if err := wsWriteClose(server, CloseGoingAway, "bye"); err != nil {
			t.Errorf("wsWriteClose: %v", err)
		}
		_ = server.Close()
	}()

	opcode, payload, _, _, err := wsReadFrame(client)
	_ = client.Close()
	if err != nil {
		t.Fatalf("wsReadFrame: %v", err)
	}
	if opcode != opClose {
		t.Fatalf("opcode = %x, want a close frame", opcode)
	}
	if len(payload) < 2 {
		t.Fatalf("payload = %v, want a 2-byte code at least", payload)
	}
	if code := uint16(payload[0])<<8 | uint16(payload[1]); code != CloseGoingAway {
		t.Errorf("code = %d, want %d", code, CloseGoingAway)
	}
	if reason := string(payload[2:]); reason != "bye" {
		t.Errorf("reason = %q, want bye", reason)
	}
}

// A writer whose connection is gone reports it rather than silently dropping the frame: the caller
// uses that error to decide the client is gone.
func TestWsWriteFrameReportsADeadConnection(t *testing.T) {
	client, server := net.Pipe()
	_ = client.Close() // no reader on the other end
	defer server.Close()

	err := wsWriteFrame(server, opText, bytes.Repeat([]byte{'x'}, 4096))
	if err == nil {
		t.Error("writing to a closed connection must fail")
	}

	if err := wsWriteFrame(server, opClose, []byte{0x03, 0xe8}); err == nil {
		t.Error("a close frame to a dead connection must fail too")
	}
}

// A response writer that cannot be hijacked is reported instead of returning a nil connection the
// caller would then write to. httptest.ResponseRecorder is exactly such a writer.
func TestWsHandshakeRefusesAWriterItCannotHijack(t *testing.T) {
	r := httptest.NewRequest(http.MethodGet, "/v1/ws", nil)
	r.Header.Set("Sec-WebSocket-Key", "dGhlIHNhbXBsZSBub25jZQ==")
	conn, rc, err := wsHandshake(httptest.NewRecorder(), r)
	if err == nil {
		t.Fatal("a writer that cannot hijack must be refused")
	}
	if conn != nil || rc != nil {
		t.Errorf("conn = %v, rc = %v, want both nil on failure", conn, rc)
	}
	if !strings.Contains(err.Error(), "hijack") {
		t.Errorf("err = %q, want it to name the problem", err)
	}
}
