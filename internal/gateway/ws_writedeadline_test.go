package gateway

import (
	"net"
	"testing"
	"time"
)

// A client that stops reading without closing the connection must not hold a writer forever:
// the write has a deadline, so the query that sent it, and the run slot it holds, are freed.
func TestWsWriteFrameGivesUpOnAClientThatStopsReading(t *testing.T) {
	client, server := net.Pipe() // nobody reads client: a write to server blocks
	defer client.Close()
	defer server.Close()
	cl := &wsClient{conn: server, writeLimit: 50 * time.Millisecond}

	done := make(chan bool, 1)
	go func() { done <- cl.writeFrame(opText, []byte("x")) }()
	select {
	case ok := <-done:
		if ok {
			t.Error("a write nobody reads reported success")
		}
	case <-time.After(3 * time.Second):
		t.Fatal("writeFrame blocked on a client that stopped reading")
	}

	closed := make(chan struct{})
	go func() { cl.close(CloseGoingAway, "bye"); close(closed) }()
	select {
	case <-closed:
	case <-time.After(3 * time.Second):
		t.Fatal("close blocked on a client that stopped reading")
	}
}

func TestWsWriteTimeoutDefault(t *testing.T) {
	if got := (&wsClient{}).writeTimeout(); got != wsWriteTimeout {
		t.Errorf("default write timeout = %v, want %v", got, wsWriteTimeout)
	}
}
