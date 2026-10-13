package webpush

import (
	"crypto/ecdh"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"net/url"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
)

// Subscription is one browser that asked to be told, as PushSubscription.toJSON() writes it, plus
// the language its messages are written in.
type Subscription struct {
	Endpoint string `json:"endpoint"`
	Keys     struct {
		P256dh string `json:"p256dh"`
		Auth   string `json:"auth"`
	} `json:"keys"`
	// Lang is the interface language of the page that subscribed: en or es.
	Lang string `json:"lang,omitempty"`
}

// MaxSubscriptions is how many browsers are kept: past it the oldest one is dropped. A person has
// a phone or two and a computer; a list that keeps growing is a page that keeps resubscribing.
const MaxSubscriptions = 16

// pushHosts are the push services of the browsers there are. The endpoint is where the gateway
// will send a request, so it must be one of them and not an address on the gateway's network.
var pushHosts = []string{
	"fcm.googleapis.com",         // Chrome, and the browsers built on it, Android's included
	".push.apple.com",            // Safari, and the apps installed on an iPhone
	".push.services.mozilla.com", // Firefox
	".notify.windows.com",        // Edge on Windows
}

// ErrInvalid is what a subscription that cannot be used is reported as.
var ErrInvalid = errors.New("not a usable push subscription")

// Validate checks that the subscription names a known push service over https and carries the
// keys a message is encrypted with.
func (s Subscription) Validate() error {
	u, err := url.Parse(s.Endpoint)
	if err != nil || u.Scheme != "https" || !knownHost(u.Hostname()) {
		return fmt.Errorf("%w: the endpoint is not a browser push service", ErrInvalid)
	}
	p, err := b64.DecodeString(s.Keys.P256dh)
	if err == nil {
		_, err = ecdh.P256().NewPublicKey(p)
	}
	if err != nil {
		return fmt.Errorf("%w: the p256dh key", ErrInvalid)
	}
	if a, err := b64.DecodeString(s.Keys.Auth); err != nil || len(a) != 16 {
		return fmt.Errorf("%w: the auth secret", ErrInvalid)
	}
	return nil
}

func knownHost(host string) bool {
	host = strings.ToLower(host)
	return slices.ContainsFunc(pushHosts, func(h string) bool {
		if strings.HasPrefix(h, ".") {
			return strings.HasSuffix(host, h)
		}
		return host == h
	})
}

// Store is the subscriptions, saved as one JSON file.
type Store struct {
	mu   sync.Mutex
	path string
	subs []Subscription
}

// OpenStore reads the subscriptions saved at path; a missing file is an empty list.
func OpenStore(path string) (*Store, error) {
	s := &Store{path: path}
	data, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		return s, nil
	}
	if err == nil {
		err = json.Unmarshal(data, &s.subs)
	}
	if err != nil {
		return nil, fmt.Errorf("could not read the push subscriptions in %s: %w", path, err)
	}
	return s, nil
}

// List is a copy of the subscriptions.
func (s *Store) List() []Subscription {
	s.mu.Lock()
	defer s.mu.Unlock()
	return slices.Clone(s.subs)
}

// Add saves a subscription, replacing the one with the same endpoint: a browser that subscribes
// again (a new language, a renewed key) is still one browser.
func (s *Store) Add(sub Subscription) error {
	if err := sub.Validate(); err != nil {
		return err
	}
	if sub.Lang != "es" {
		sub.Lang = "en"
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	next := slices.DeleteFunc(slices.Clone(s.subs), func(o Subscription) bool { return o.Endpoint == sub.Endpoint })
	next = append(next, sub)
	if len(next) > MaxSubscriptions {
		next = next[len(next)-MaxSubscriptions:]
	}
	return s.save(next)
}

// Remove forgets the subscription with this endpoint. It reports whether there was one.
func (s *Store) Remove(endpoint string) (bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	next := slices.DeleteFunc(slices.Clone(s.subs), func(o Subscription) bool { return o.Endpoint == endpoint })
	if len(next) == len(s.subs) {
		return false, nil
	}
	return true, s.save(next)
}

// save writes next and, once it is on disk, makes it the list. The file holds the browsers' keys,
// so it is readable by its owner only.
func (s *Store) save(next []Subscription) error {
	data, _ := json.MarshalIndent(next, "", "  ") // plain strings: cannot fail
	err := os.MkdirAll(filepath.Dir(s.path), 0o700)
	if err == nil {
		err = os.WriteFile(s.path, data, 0o600)
	}
	if err != nil {
		return fmt.Errorf("could not save the push subscriptions: %w", err)
	}
	s.subs = next
	return nil
}
