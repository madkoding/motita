package webpush

import (
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/ecdh"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"encoding/binary"
	"encoding/json"
	"errors"
	"io"
	"math/big"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// browser is the receiving end of a subscription: what a real browser keeps to decrypt.
type browser struct {
	priv *ecdh.PrivateKey
	auth []byte
	sub  Subscription
}

func newBrowser(t *testing.T, endpoint string) *browser {
	t.Helper()
	priv, err := ecdh.P256().GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	auth := make([]byte, 16)
	_, _ = rand.Read(auth)
	b := &browser{priv: priv, auth: auth}
	b.sub.Endpoint = endpoint
	b.sub.Keys.P256dh = b64.EncodeToString(priv.PublicKey().Bytes())
	b.sub.Keys.Auth = b64.EncodeToString(auth)
	return b
}

// decrypt is RFC 8291 from the browser's side, written apart from Encrypt so a mistake in one is
// not mirrored in the other.
func (b *browser) decrypt(t *testing.T, body []byte) []byte {
	t.Helper()
	salt := body[:16]
	if rs := binary.BigEndian.Uint32(body[16:20]); rs != recordSize {
		t.Fatalf("record size %d", rs)
	}
	idlen := int(body[20])
	asRaw := body[21 : 21+idlen]
	as, err := ecdh.P256().NewPublicKey(asRaw)
	if err != nil {
		t.Fatal(err)
	}
	secret, err := b.priv.ECDH(as)
	if err != nil {
		t.Fatal(err)
	}
	uaRaw := b.priv.PublicKey().Bytes()
	info := append(append([]byte("WebPush: info\x00"), uaRaw...), asRaw...)
	ikm := hkdf(b.auth, secret, info, 32)
	cek := hkdf(salt, ikm, []byte("Content-Encoding: aes128gcm\x00"), 16)
	nonce := hkdf(salt, ikm, []byte("Content-Encoding: nonce\x00"), 12)
	block, _ := aes.NewCipher(cek)
	gcm, _ := cipher.NewGCM(block)
	plain, err := gcm.Open(nil, nonce, body[21+idlen:], nil)
	if err != nil {
		t.Fatalf("the browser could not decrypt the message: %v", err)
	}
	if plain[len(plain)-1] != 0x02 {
		t.Fatalf("the last record delimiter is %x", plain[len(plain)-1])
	}
	return plain[:len(plain)-1]
}

const fcm = "https://fcm.googleapis.com/fcm/send/abc"

func TestHKDFMatchesRFC5869(t *testing.T) {
	// RFC 5869, test case 1, cut to 32 bytes: one block is all this package asks for.
	ikm := []byte{}
	for range 22 {
		ikm = append(ikm, 0x0b)
	}
	salt := []byte{0, 1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11, 12}
	info := []byte{0xf0, 0xf1, 0xf2, 0xf3, 0xf4, 0xf5, 0xf6, 0xf7, 0xf8, 0xf9}
	got := hkdf(salt, ikm, info, 32)
	want := "3cb25f25faacd57a90434f64d0362f2a2d2d0a90cf1a5a4c5db02d56ecc4c5bf"
	if hexOf(got) != want {
		t.Fatalf("hkdf = %s, want %s", hexOf(got), want)
	}
}

func hexOf(b []byte) string {
	const digits = "0123456789abcdef"
	var sb strings.Builder
	for _, c := range b {
		sb.WriteByte(digits[c>>4])
		sb.WriteByte(digits[c&15])
	}
	return sb.String()
}

func TestTheBrowserDecryptsWhatIsSent(t *testing.T) {
	b := newBrowser(t, fcm)
	body, err := Encrypt(b.sub, []byte(`{"title":"CI passed"}`))
	if err != nil {
		t.Fatal(err)
	}
	if got := string(b.decrypt(t, body)); got != `{"title":"CI passed"}` {
		t.Fatalf("decrypted %q", got)
	}
	again, _ := Encrypt(b.sub, []byte(`{"title":"CI passed"}`))
	if string(again) == string(body) {
		t.Fatal("two messages were encrypted the same: the salt and key must be new each time")
	}
}

func TestEncryptRefusesAnInvalidSubscription(t *testing.T) {
	if _, err := Encrypt(Subscription{Endpoint: "http://fcm.googleapis.com/x"}, nil); !errors.Is(err, ErrInvalid) {
		t.Fatalf("err = %v", err)
	}
}

func TestValidate(t *testing.T) {
	good := newBrowser(t, fcm).sub
	cases := map[string]func(*Subscription){
		"plain http":      func(s *Subscription) { s.Endpoint = "http://fcm.googleapis.com/x" },
		"unknown host":    func(s *Subscription) { s.Endpoint = "https://192.168.1.1/x" },
		"lookalike host":  func(s *Subscription) { s.Endpoint = "https://evilpush.apple.com.example/x" },
		"bad url":         func(s *Subscription) { s.Endpoint = "https://%zz" },
		"bad p256dh b64":  func(s *Subscription) { s.Keys.P256dh = "***" },
		"not a point":     func(s *Subscription) { s.Keys.P256dh = b64.EncodeToString(make([]byte, 65)) },
		"short auth":      func(s *Subscription) { s.Keys.Auth = b64.EncodeToString([]byte("short")) },
		"bad auth base64": func(s *Subscription) { s.Keys.Auth = "***" },
	}
	for name, mutate := range cases {
		s := good
		mutate(&s)
		if err := s.Validate(); !errors.Is(err, ErrInvalid) {
			t.Errorf("%s: err = %v", name, err)
		}
	}
	for _, ep := range []string{
		fcm,
		"https://web.push.apple.com/QGuQyavXutnMH",
		"https://updates.push.services.mozilla.com/wpush/v2/x",
		"https://wns2-par02p.notify.windows.com/w/?token=x",
	} {
		s := good
		s.Endpoint = ep
		if err := s.Validate(); err != nil {
			t.Errorf("%s: %v", ep, err)
		}
	}
}

func TestLoadKeysCreatesThenReadsTheSameKey(t *testing.T) {
	path := filepath.Join(t.TempDir(), "push", "vapid.key")
	k1, err := LoadKeys(path)
	if err != nil {
		t.Fatal(err)
	}
	if raw, _ := b64.DecodeString(k1.Public()); len(raw) != 65 || raw[0] != 4 {
		t.Fatalf("the public key is not an uncompressed P-256 point: %q", k1.Public())
	}
	if st, _ := os.Stat(path); st.Mode().Perm() != 0o600 {
		t.Fatalf("the key file mode is %v", st.Mode().Perm())
	}
	k2, err := LoadKeys(path)
	if err != nil {
		t.Fatal(err)
	}
	if k1.Public() != k2.Public() {
		t.Fatal("the key changed across loads: every browser would have to subscribe again")
	}
}

func TestLoadKeysFailures(t *testing.T) {
	dir := t.TempDir()
	if _, err := LoadKeys(dir); err == nil {
		t.Error("a directory was read as a key")
	}
	bad := filepath.Join(dir, "bad.key")
	_ = os.WriteFile(bad, []byte("***"), 0o600)
	if _, err := LoadKeys(bad); err == nil || !strings.Contains(err.Error(), "not valid") {
		t.Errorf("garbage: %v", err)
	}
	zero := filepath.Join(dir, "zero.key")
	_ = os.WriteFile(zero, []byte(b64.EncodeToString(make([]byte, 32))), 0o600)
	if _, err := LoadKeys(zero); err == nil {
		t.Error("the zero scalar was accepted")
	}
	file := filepath.Join(dir, "file")
	_ = os.WriteFile(file, nil, 0o600)
	if _, err := LoadKeys(filepath.Join(file, "vapid.key")); err == nil {
		t.Error("a key was read under a file")
	}
	// A link to a folder that does not exist reads as no key, and then cannot be written.
	dangling := filepath.Join(dir, "dangling.key")
	_ = os.Symlink(filepath.Join(dir, "missing", "vapid.key"), dangling)
	if _, err := LoadKeys(dangling); err == nil || !strings.Contains(err.Error(), "could not save") {
		t.Errorf("unsaved key: %v", err)
	}
}

func TestStoreAddReplacesCapsAndPersists(t *testing.T) {
	path := filepath.Join(t.TempDir(), "subs.json")
	st, err := OpenStore(path)
	if err != nil {
		t.Fatal(err)
	}
	b := newBrowser(t, fcm)
	if err := st.Add(b.sub); err != nil {
		t.Fatal(err)
	}
	again := b.sub
	again.Lang = "es"
	if err := st.Add(again); err != nil {
		t.Fatal(err)
	}
	if l := st.List(); len(l) != 1 || l[0].Lang != "es" {
		t.Fatalf("the same browser twice: %+v", l)
	}
	if st.List()[0].Lang != "es" {
		t.Fatal("lang")
	}
	for i := range MaxSubscriptions + 2 {
		s := newBrowser(t, fcm+"/"+string(rune('a'+i))).sub
		s.Lang = "fr"
		if err := st.Add(s); err != nil {
			t.Fatal(err)
		}
	}
	list := st.List()
	if len(list) != MaxSubscriptions {
		t.Fatalf("%d subscriptions kept, want %d", len(list), MaxSubscriptions)
	}
	if list[0].Endpoint == fcm || list[len(list)-1].Lang != "en" {
		t.Fatalf("the oldest was not dropped, or the language not normalized: %+v", list[0])
	}
	reopened, err := OpenStore(path)
	if err != nil || len(reopened.List()) != MaxSubscriptions {
		t.Fatalf("reopened: %v %d", err, len(reopened.List()))
	}
	if ok, err := st.Remove(list[0].Endpoint); !ok || err != nil {
		t.Fatalf("remove: %v %v", ok, err)
	}
	if ok, _ := st.Remove("https://nobody"); ok {
		t.Fatal("removed a subscription that was not there")
	}
	if err := st.Add(Subscription{Endpoint: "x"}); !errors.Is(err, ErrInvalid) {
		t.Fatalf("invalid add: %v", err)
	}
}

func TestStoreFailures(t *testing.T) {
	dir := t.TempDir()
	bad := filepath.Join(dir, "bad.json")
	_ = os.WriteFile(bad, []byte("{"), 0o600)
	if _, err := OpenStore(bad); err == nil {
		t.Error("garbage was read as subscriptions")
	}
	file := filepath.Join(dir, "file")
	_ = os.WriteFile(file, nil, 0o600)
	if _, err := OpenStore(filepath.Join(file, "subs.json")); err == nil {
		t.Error("a path under a file was read as subscriptions")
	}
	st := &Store{path: filepath.Join(file, "subs.json")}
	if err := st.Add(newBrowser(t, fcm).sub); err == nil {
		t.Fatal("saved under a file")
	}
	if len(st.List()) != 0 {
		t.Fatal("a subscription that was not saved is listed")
	}
}

// roundTrip is an http.RoundTripper made of a function.
type roundTrip func(*http.Request) (*http.Response, error)

func (f roundTrip) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func answer(code int) *http.Response {
	return &http.Response{StatusCode: code, Status: http.StatusText(code), Body: io.NopCloser(strings.NewReader("why"))}
}

func newSender(t *testing.T, rt roundTrip) (*Sender, *Store) {
	t.Helper()
	dir := t.TempDir()
	k, err := LoadKeys(filepath.Join(dir, "k"))
	if err != nil {
		t.Fatal(err)
	}
	st, _ := OpenStore(filepath.Join(dir, "s.json"))
	now := time.Unix(1_700_000_000, 0)
	return &Sender{Keys: k, Store: st, Client: &http.Client{Transport: rt}, Now: func() time.Time { return now }}, st
}

func TestSendToSignsAndEncrypts(t *testing.T) {
	b := newBrowser(t, fcm)
	var got *http.Request
	var body []byte
	s, _ := newSender(t, func(r *http.Request) (*http.Response, error) {
		got = r
		body, _ = io.ReadAll(r.Body)
		return answer(http.StatusCreated), nil
	})
	if err := s.SendTo(context.Background(), b.sub, Message{Title: "CI passed", URL: "/?session=x"}); err != nil {
		t.Fatal(err)
	}
	if got.Header.Get("Content-Encoding") != "aes128gcm" || got.Header.Get("TTL") != "86400" {
		t.Fatalf("headers: %v", got.Header)
	}
	var m Message
	if err := json.Unmarshal(b.decrypt(t, body), &m); err != nil || m.Title != "CI passed" || m.URL != "/?session=x" {
		t.Fatalf("message: %+v %v", m, err)
	}
	checkVAPID(t, got.Header.Get("Authorization"), s.Keys, "https://fcm.googleapis.com")
}

// checkVAPID verifies the token the way a push service does.
func checkVAPID(t *testing.T, header string, k *Keys, aud string) {
	t.Helper()
	rest, ok := strings.CutPrefix(header, "vapid t=")
	token, key, ok2 := strings.Cut(rest, ", k=")
	if !ok || !ok2 || key != k.Public() {
		t.Fatalf("authorization: %q", header)
	}
	parts := strings.Split(token, ".")
	claimsRaw, _ := b64.DecodeString(parts[1])
	var claims struct {
		Aud string `json:"aud"`
		Exp int64  `json:"exp"`
		Sub string `json:"sub"`
	}
	_ = json.Unmarshal(claimsRaw, &claims)
	if claims.Aud != aud || claims.Sub != Subject || claims.Exp != 1_700_000_000+12*3600 {
		t.Fatalf("claims: %+v", claims)
	}
	sig, _ := b64.DecodeString(parts[2])
	pubRaw, _ := b64.DecodeString(key)
	pub, err := ecdsa.ParseUncompressedPublicKey(elliptic.P256(), pubRaw)
	if err != nil {
		t.Fatal(err)
	}
	digest := sha256.Sum256([]byte(parts[0] + "." + parts[1]))
	r, s := new(big.Int).SetBytes(sig[:32]), new(big.Int).SetBytes(sig[32:])
	if !ecdsa.Verify(pub, digest[:], r, s) {
		t.Fatal("the VAPID signature does not verify")
	}
}

func TestSendToFailures(t *testing.T) {
	b := newBrowser(t, fcm)
	ctx := context.Background()
	s, _ := newSender(t, func(*http.Request) (*http.Response, error) { return nil, errors.New("offline") })
	if err := s.SendTo(ctx, b.sub, Message{Title: "x"}); err == nil || !strings.Contains(err.Error(), "did not answer") {
		t.Errorf("offline: %v", err)
	}
	if err := s.SendTo(ctx, b.sub, Message{Title: strings.Repeat("x", MaxPayload)}); err == nil {
		t.Error("an oversized message was sent")
	}
	if err := s.SendTo(ctx, Subscription{}, Message{}); !errors.Is(err, ErrInvalid) {
		t.Errorf("invalid: %v", err)
	}
	s, _ = newSender(t, func(*http.Request) (*http.Response, error) { return answer(http.StatusBadRequest), nil })
	if err := s.SendTo(ctx, b.sub, Message{}); err == nil || !strings.Contains(err.Error(), "refused") {
		t.Errorf("refused: %v", err)
	}
	s, _ = newSender(t, func(*http.Request) (*http.Response, error) { return answer(http.StatusGone), nil })
	if err := s.SendTo(ctx, b.sub, Message{}); !errors.Is(err, ErrGone) {
		t.Errorf("gone: %v", err)
	}
}

func TestSendWritesEachInItsLanguageAndForgetsTheGone(t *testing.T) {
	en := newBrowser(t, fcm+"/en")
	es := newBrowser(t, fcm+"/es")
	es.sub.Lang = "es"
	gone := newBrowser(t, fcm+"/gone")
	broken := newBrowser(t, fcm+"/broken")
	got := map[string]string{}
	s, st := newSender(t, func(r *http.Request) (*http.Response, error) {
		body, _ := io.ReadAll(r.Body)
		switch r.URL.Path {
		case "/fcm/send/abc/gone":
			return answer(http.StatusNotFound), nil
		case "/fcm/send/abc/broken":
			return answer(http.StatusInternalServerError), nil
		default: // a live endpoint: decrypted below
		}
		for _, b := range []*browser{en, es} {
			if b.sub.Endpoint == r.URL.String() {
				var m Message
				_ = json.Unmarshal(b.decrypt(t, body), &m)
				got[r.URL.Path] = m.Title
			}
		}
		return answer(http.StatusCreated), nil
	})
	for _, b := range []*browser{en, es, gone, broken} {
		if err := st.Add(b.sub); err != nil {
			t.Fatal(err)
		}
	}
	sent, err := s.Send(context.Background(), func(lang string) Message { return Message{Title: "hi " + lang} })
	if sent != 2 || err == nil || !strings.Contains(err.Error(), "refused") {
		t.Fatalf("sent %d, err %v", sent, err)
	}
	if got["/fcm/send/abc/en"] != "hi en" || got["/fcm/send/abc/es"] != "hi es" {
		t.Fatalf("titles: %v", got)
	}
	if len(st.List()) != 3 {
		t.Fatalf("the gone subscription was kept: %d", len(st.List()))
	}
}
