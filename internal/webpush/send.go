package webpush

import (
	"bytes"
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/ecdh"
	"crypto/ecdsa"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"time"

	"github.com/madkoding/motita/internal/httpbody"
)

// Message is what a notification shows, and where a tap on it leads.
type Message struct {
	Title string `json:"title"`
	Body  string `json:"body,omitempty"`
	// Tag makes a newer notification replace an older one with the same tag instead of stacking.
	Tag string `json:"tag,omitempty"`
	// URL is the page a tap opens, relative to the gateway.
	URL string `json:"url,omitempty"`
}

// Subject is who the push services can write to about this sender. Apple's service refuses a
// message without one; the project's page is a stable, public answer.
const Subject = "https://github.com/madkoding/motita"

// TTL is how long a push service keeps a message for a phone that is off: a CI that ended a day
// ago is no longer news.
const TTL = 24 * time.Hour

// recordSize is the aes128gcm record size: one record holds any message this sends.
const recordSize = 4096

// MaxPayload is the largest message a push service has to accept (RFC 8030 says 4096 bytes of
// ciphertext); the record's own overhead takes the rest.
const MaxPayload = 3800

// Sender delivers messages to every saved subscription.
type Sender struct {
	Keys   *Keys
	Store  *Store
	Client *http.Client
	// Now is the clock the VAPID token is dated with.
	Now func() time.Time
}

// Send delivers one message to every subscription, written by build in the subscription's
// language. A subscription the push service says is gone (404, 410) is forgotten. It returns how
// many were delivered and what went wrong with the others.
func (s *Sender) Send(ctx context.Context, build func(lang string) Message) (int, error) {
	sent := 0
	var errs []error
	for _, sub := range s.Store.List() {
		err := s.SendTo(ctx, sub, build(sub.Lang))
		if errors.Is(err, ErrGone) {
			_, err = s.Store.Remove(sub.Endpoint)
			errs = append(errs, err)
			continue
		}
		if err != nil {
			errs = append(errs, err)
			continue
		}
		sent++
	}
	return sent, errors.Join(errs...)
}

// ErrGone is a subscription the browser dropped: the push service will never deliver to it again.
var ErrGone = errors.New("the push subscription is gone")

// SendTo delivers one message to one subscription.
func (s *Sender) SendTo(ctx context.Context, sub Subscription, m Message) error {
	payload, _ := json.Marshal(m) // plain strings: cannot fail
	if len(payload) > MaxPayload {
		return fmt.Errorf("the push message is %d bytes, more than the %d a push service takes", len(payload), MaxPayload)
	}
	body, err := Encrypt(sub, payload)
	if err != nil {
		return err
	}
	// Encrypt validated the endpoint, so neither can fail.
	u, _ := url.Parse(sub.Endpoint)
	req, _ := http.NewRequestWithContext(ctx, http.MethodPost, sub.Endpoint, bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/octet-stream")
	req.Header.Set("Content-Encoding", "aes128gcm")
	req.Header.Set("TTL", strconv.Itoa(int(TTL.Seconds())))
	req.Header.Set("Urgency", "normal")
	req.Header.Set("Authorization", s.Keys.vapid(u.Scheme+"://"+u.Host, s.Now()))
	res, err := s.Client.Do(req)
	if err != nil {
		return fmt.Errorf("the push service did not answer: %w", err)
	}
	defer res.Body.Close()
	detail, _ := io.ReadAll(io.LimitReader(res.Body, 512))
	switch {
	case res.StatusCode == http.StatusNotFound || res.StatusCode == http.StatusGone:
		return ErrGone
	case res.StatusCode >= 300:
		if note := httpbody.Note(detail, 200); note != "" {
			return fmt.Errorf("the push service refused the message: %s %s", res.Status, note)
		}
		return fmt.Errorf("the push service refused the message: %s", res.Status)
	default:
		return nil
	}
}

// vapid is the Authorization header that proves the message comes from the holder of the key the
// browser subscribed with: a short-lived ES256 token for the push service's origin.
func (k *Keys) vapid(audience string, now time.Time) string {
	header := b64.EncodeToString([]byte(`{"typ":"JWT","alg":"ES256"}`))
	claims, _ := json.Marshal(map[string]any{
		"aud": audience,
		"exp": now.Add(12 * time.Hour).Unix(),
		"sub": Subject,
	})
	unsigned := header + "." + b64.EncodeToString(claims)
	digest := sha256.Sum256([]byte(unsigned))
	// The signature's randomness comes from the system's secure source and cannot fail.
	r, sig, _ := ecdsa.Sign(rand.Reader, k.priv, digest[:])
	raw := make([]byte, 64)
	r.FillBytes(raw[:32])
	sig.FillBytes(raw[32:])
	return "vapid t=" + unsigned + "." + b64.EncodeToString(raw) + ", k=" + k.Public()
}

// Encrypt is the message as the browser decrypts it: RFC 8291's aes128gcm, one record, with a new
// key and salt for every message.
func Encrypt(sub Subscription, plain []byte) ([]byte, error) {
	if err := sub.Validate(); err != nil {
		return nil, err
	}
	uaRaw, _ := b64.DecodeString(sub.Keys.P256dh) // Validate checked both
	auth, _ := b64.DecodeString(sub.Keys.Auth)
	ua, _ := ecdh.P256().NewPublicKey(uaRaw)

	as, _ := ecdh.P256().GenerateKey(rand.Reader) // cannot fail since Go 1.26
	asRaw := as.PublicKey().Bytes()
	// Both keys are valid points on the curve, so the exchange cannot fail.
	secret, _ := as.ECDH(ua)
	salt := make([]byte, 16)
	_, _ = rand.Read(salt) // never fails

	keyInfo := append(append([]byte("WebPush: info\x00"), uaRaw...), asRaw...)
	ikm := hkdf(auth, secret, keyInfo, 32)
	cek := hkdf(salt, ikm, []byte("Content-Encoding: aes128gcm\x00"), 16)
	nonce := hkdf(salt, ikm, []byte("Content-Encoding: nonce\x00"), 12)

	block, _ := aes.NewCipher(cek) // a 16-byte key
	gcm, _ := cipher.NewGCM(block)
	// The 0x02 after the message marks the last (and only) record.
	sealed := gcm.Seal(nil, nonce, append(plain, 0x02), nil)

	out := make([]byte, 0, 16+4+1+len(asRaw)+len(sealed))
	out = append(out, salt...)
	out = binary.BigEndian.AppendUint32(out, recordSize)
	out = append(out, byte(len(asRaw)))
	out = append(out, asRaw...)
	return append(out, sealed...), nil
}

// hkdf is RFC 5869 for an output of one hash block or less, which is all aes128gcm asks for.
func hkdf(salt, secret, info []byte, length int) []byte {
	ext := hmac.New(sha256.New, salt)
	ext.Write(secret)
	exp := hmac.New(sha256.New, ext.Sum(nil))
	exp.Write(info)
	exp.Write([]byte{1})
	return exp.Sum(nil)[:length]
}
