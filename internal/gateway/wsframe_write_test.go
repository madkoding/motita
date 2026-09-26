package gateway

// The frame writer's two failure branches and the connection-error classification.
//
// The writer makes TWO writes - the header and then the payload - and a connection can fail on
// either. A net.Conn cannot be asked to fail one and not the other, so these drive the writer
// through a bare io.Writer that fails on a chosen call.

import (
	"bytes"
	"errors"
	"io"
	"net"
	"strings"
	"testing"
)

// failAtWriter fails on its Nth write, so the header and the payload can be failed separately.
type failAtWriter struct {
	failOn int // 1-based: the write that fails
	writes int
	buf    bytes.Buffer
}

func (w *failAtWriter) Write(p []byte) (int, error) {
	w.writes++
	if w.writes == w.failOn {
		return 0, errors.New("the connection went away")
	}
	return w.buf.Write(p)
}

// A frame whose HEADER cannot be written is reported as such.
func TestAFrameWhoseHeaderCannotBeWrittenIsReported(t *testing.T) {
	// A payload over 125 bytes forces the extended length, so the header is longer than two bytes -
	// still ONE write, which is what the writer makes. The failing call is the first.
	w := &failAtWriter{failOn: 1}
	err := wsWriteFrame(wrapAsConn(w), opText, bytes.Repeat([]byte("x"), 200))
	if err == nil {
		t.Fatal("a header that cannot be written must be reported")
	}
	if !strings.Contains(err.Error(), "header") {
		t.Errorf("err = %q, want it to name the header", err)
	}
}

// And a frame whose PAYLOAD cannot be written is reported as such, so the two are distinguishable
// in a log.
func TestAFrameWhosePayloadCannotBeWrittenIsReported(t *testing.T) {
	w := &failAtWriter{failOn: 2} // the header succeeds, the payload fails
	err := wsWriteFrame(wrapAsConn(w), opText, []byte("the payload"))
	if err == nil {
		t.Fatal("a payload that cannot be written must be reported")
	}
	if !strings.Contains(err.Error(), "payload") {
		t.Errorf("err = %q, want it to name the payload", err)
	}
}

// A frame with NO payload writes only the header, so a failure there is still reported and the
// writer does not try to write nothing.
func TestAHeaderOnlyFrameStillReportsAFailure(t *testing.T) {
	w := &failAtWriter{failOn: 1}
	if err := wsWriteFrame(wrapAsConn(w), opClose, nil); err == nil {
		t.Error("a header-only frame that cannot be written must be reported")
	}
}

// And a frame that writes cleanly reports no error and puts the bytes where they belong.
func TestAFrameWritesHeaderAndPayloadInOrder(t *testing.T) {
	w := &failAtWriter{failOn: 0} // nothing fails
	if err := wsWriteFrame(wrapAsConn(w), opText, []byte("hello")); err != nil {
		t.Fatalf("wsWriteFrame: %v", err)
	}
	got := w.buf.Bytes()
	if len(got) != 2+5 {
		t.Fatalf("wrote %d bytes, want the 2-byte header and the 5-byte payload", len(got))
	}
	if got[0] != 0x80|opText {
		t.Errorf("first byte = %#x, want FIN|opText", got[0])
	}
	if got[1] != 5 {
		t.Errorf("length byte = %d, want 5", got[1])
	}
	if string(got[2:]) != "hello" {
		t.Errorf("payload = %q, want hello", got[2:])
	}
}

// connWriter adapts an io.Writer to the net.Conn the frame writer takes. Only Write and Close are
// ever reached by the writer, and the rest panic rather than silently returning zero values that
// would make a broken test look like a passing one.
type connWriter struct {
	net.Conn
	w io.Writer
}

func (c *connWriter) Write(p []byte) (int, error) { return c.w.Write(p) }
func (c *connWriter) Close() error                { return nil }

// Read never blocks: this is a SINK standing in for a connection, so a read loop driven by it ends
// immediately instead of panicking on the embedded nil net.Conn.
func (c *connWriter) Read([]byte) (int, error) { return 0, io.EOF }

func wrapAsConn(w io.Writer) net.Conn { return &connWriter{w: w} }

// --- the connection-error classification --------------------------------------------------------

// A client that simply HUNG UP is not a failure to log. EOF is the most common end of a WebSocket
// connection there is - a browser closing a tab - and classifying it as a warning buries the
// failures that matter.
func TestAnEOFIsNotTreatedAsAFailure(t *testing.T) {
	cases := []struct {
		name string
		err  error
		want bool
	}{
		{"a bare EOF", io.EOF, true},
		{"a wrapped EOF", errors.New("could not read the frame: EOF"), true},
		{"a closed connection sentinel", net.ErrClosed, true},
		{"a use-of-closed-connection error", errors.New("use of closed network connection"), true},
		{"a reset connection", errors.New("connection reset by peer"), true},
		{"a broken pipe", errors.New("write: broken pipe"), true},
		{"a real failure", errors.New("the TLS handshake failed"), false},
		{"no error at all", nil, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := isClosedConnErr(tc.err); got != tc.want {
				t.Errorf("isClosedConnErr(%v) = %v, want %v", tc.err, got, tc.want)
			}
		})
	}
}
