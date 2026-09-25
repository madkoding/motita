package gateway

import (
	"crypto/rand"
	"encoding/json"
	"fmt"
	"time"
)

// Message types. They are constants because both ends switch on them: a literal typed on
// both sides is a stream that stops being understood after one rename.
const (
	MsgAuth          = "auth"           // Client → Server: initial authentication.
	MsgAuthResponse  = "auth_response"  // Server → Client: authentication result.
	MsgQuery         = "query"          // Client → Server: a query or request.
	MsgQueryResponse = "query_response" // Server → Client: answer to a query.
	MsgHeartbeat     = "heartbeat"      // Bidirectional: keep-alive probe.
	MsgHeartbeatAck  = "heartbeat_ack"  // Bidirectional: keep-alive confirmation.
	MsgNotification  = "notification"   // Server → Client: push notification.
	MsgError         = "error"          // Server → Client: error message.
)

// Flags that travel in the flags array of a Message. They indicate state and conditions
// without adding fields to the payload, so a client can branch on a flag without parsing
// the payload.
const (
	FlagAuthenticated    = "AUTHENTICATED"     // The client is authenticated.
	FlagUnauthorized     = "UNAUTHORIZED"      // Authentication failed.
	FlagIdle             = "IDLE"              // The agent is idle / waiting.
	FlagBusy             = "BUSY"              // The agent is processing a task.
	FlagProcessing       = "PROCESSING"        // The query is being processed.
	FlagCompleted        = "COMPLETED"         // The task / query finished successfully.
	FlagPartial          = "PARTIAL"           // The response is partial (paginated or streaming).
	FlagErrorRecoverable = "ERROR_RECOVERABLE" // An error the connection can recover from.
	FlagFatalError       = "FATAL_ERROR"       // A critical error; the connection will close.
	FlagLowMemory        = "LOW_MEMORY"        // The agent is low on memory.
	FlagRateLimited      = "RATE_LIMITED"      // The client exceeded the rate limit.
	FlagCacheHit         = "CACHE_HIT"         // The response came from cache.
	FlagCacheMiss        = "CACHE_MISS"        // The response was generated in real time.
	FlagEncrypted        = "ENCRYPTED"         // The payload is encrypted.
)

// wsMessage is the envelope every WebSocket message wears, in both directions.
//
// It is the WIRE CONTRACT: every message — inbound and outbound — is a JSON object with
// exactly these five fields. A client that builds one constructs this struct; a handler
// that reads one decodes into this struct. Keeping it in one place is what keeps the two
// sides from drifting, the same way EventProgress and its constants do for the SSE stream.
type wsMessage struct {
	MsgID     string          `json:"msg_id"`
	Type      string          `json:"type"`
	Flags     []string        `json:"flags"`
	Timestamp string          `json:"timestamp"`
	Payload   json.RawMessage `json:"payload"`
}

// newMsg builds an outbound message with a fresh UUID v4, the given type and flags, and
// the payload marshalled to JSON. A nil payload becomes an empty object, which is the
// shape an empty notification wears — never a null, for the same reason every list in
// this gateway is never null: a client that has to tell "empty" from "missing" is a
// client with a bug waiting to happen.
func newMsg(msgType string, flags []string, payload any) wsMessage {
	if payload == nil {
		payload = map[string]any{}
	}
	data, _ := json.Marshal(payload)
	return wsMessage{
		MsgID:     newUUIDv4(),
		Type:      msgType,
		Flags:     flags,
		Timestamp: time.Now().UTC().Format(time.RFC3339Nano),
		Payload:   data,
	}
}

// newUUIDv4 returns a random UUID v4 string. It panics only when the system's random
// source is broken, which is the same stance newApprovalID takes: a predictable id on a
// probe that gates access is worse than a crash that names the cause.
func newUUIDv4() string {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		panic(fmt.Sprintf("could not generate a uuid: %v", err))
	}
	b[6] = (b[6] & 0x0f) | 0x40 // version 4
	b[8] = (b[8] & 0x3f) | 0x80 // variant 10
	return fmt.Sprintf("%x-%x-%x-%x-%x", b[0:4], b[4:6], b[6:8], b[8:10], b[10:16])
}

// hasFlag reports whether the message carries the given flag. It is a helper so the
// handler reads as a condition and not as a loop.
func (m wsMessage) hasFlag(flag string) bool {
	for _, f := range m.Flags {
		if f == flag {
			return true
		}
	}
	return false
}

// decodePayload unmarshals the message's payload into the destination. It returns false
// when the payload is not the JSON the caller expects, and the caller is expected to
// reply with an error message rather than proceeding.
func (m wsMessage) decodePayload(dst any) error {
	return json.Unmarshal(m.Payload, dst)
}
