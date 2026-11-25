package gateway

import "testing"

// The status counts the messages waiting behind the run, so a list can show how many are queued.
func TestStatusReportsHowManyMessagesAreQueued(t *testing.T) {
	c := &conversation{id: "s1"}
	if got := c.status().Queued; got != 0 {
		t.Fatalf("nothing waits: queued = %d", got)
	}
	c.pushQueue("one")
	c.pushQueue("two")
	if got := c.status().Queued; got != 2 {
		t.Fatalf("two messages wait: queued = %d", got)
	}
}
