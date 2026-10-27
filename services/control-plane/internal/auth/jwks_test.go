package auth

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

type jwksTestClock struct {
	mu  sync.Mutex
	now time.Time
}

func (c *jwksTestClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.now
}

func (c *jwksTestClock) Advance(d time.Duration) {
	c.mu.Lock()
	c.now = c.now.Add(d)
	c.mu.Unlock()
}

type jwksTestServer struct {
	mu             sync.RWMutex
	keys           []jwkKey
	status         int
	calls          atomic.Int32
	requestStarted chan struct{}
	releaseRequest chan struct{}
}

func (s *jwksTestServer) ServeHTTP(w http.ResponseWriter, _ *http.Request) {
	call := s.calls.Add(1)
	s.mu.RLock()
	keys := append([]jwkKey(nil), s.keys...)
	status := s.status
	requestStarted := s.requestStarted
	releaseRequest := s.releaseRequest
	s.mu.RUnlock()

	if call > 1 && requestStarted != nil && releaseRequest != nil {
		select {
		case requestStarted <- struct{}{}:
		default:
		}
		<-releaseRequest
	}
	if status != http.StatusOK {
		http.Error(w, http.StatusText(status), status)
		return
	}
	_ = json.NewEncoder(w).Encode(jwksDocument{Keys: keys})
}

func (s *jwksTestServer) Set(status int, keys ...jwkKey) {
	s.mu.Lock()
	s.status = status
	s.keys = append([]jwkKey(nil), keys...)
	s.mu.Unlock()
}

func (s *jwksTestServer) BlockRequests() (<-chan struct{}, chan struct{}) {
	s.mu.Lock()
	s.requestStarted = make(chan struct{}, 1)
	s.releaseRequest = make(chan struct{})
	requestStarted := s.requestStarted
	releaseRequest := s.releaseRequest
	s.mu.Unlock()
	return requestStarted, releaseRequest
}

func testJWKSKey(t *testing.T, kid string) (*ecdsa.PrivateKey, jwkKey) {
	t.Helper()
	privateKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	publicKey := privateKey.PublicKey
	return privateKey, jwkKey{
		Kty: "EC",
		Crv: "P-256",
		Kid: kid,
		X:   base64.RawURLEncoding.EncodeToString(publicKey.X.Bytes()),
		Y:   base64.RawURLEncoding.EncodeToString(publicKey.Y.Bytes()),
		Alg: "ES256",
		Use: "sig",
	}
}

func signedJWKSJWT(t *testing.T, privateKey *ecdsa.PrivateKey, kid string) string {
	t.Helper()
	token := jwt.New(jwt.SigningMethodES256)
	token.Header["kid"] = kid
	signed, err := token.SignedString(privateKey)
	if err != nil {
		t.Fatal(err)
	}
	return signed
}

func verifyJWKSJWT(provider *JWKSProvider, token string) error {
	parsed, err := jwt.Parse(token, provider.Keyfunc(context.Background()))
	if err != nil {
		return err
	}
	if !parsed.Valid {
		return fmt.Errorf("token is invalid")
	}
	return nil
}

func newJWKSFixture(t *testing.T, ttl time.Duration) (*JWKSProvider, *jwksTestServer, *jwksTestClock) {
	t.Helper()
	fixture := &jwksTestServer{}
	server := httptest.NewServer(fixture)
	t.Cleanup(server.Close)
	clock := &jwksTestClock{now: time.Date(2026, time.October, 27, 0, 0, 0, 0, time.UTC)}
	provider := NewJWKSProvider(server.URL, ttl, time.Second)
	provider.now = clock.Now
	return provider, fixture, clock
}

func TestJWKSConfiguredTTLCannotBypassHardMaxAge(t *testing.T) {
	provider, fixture, clock := newJWKSFixture(t, 24*time.Hour)
	privateKey, publicKey := testJWKSKey(t, "key-a")
	fixture.Set(http.StatusOK, publicKey)
	token := signedJWKSJWT(t, privateKey, publicKey.Kid)
	if err := verifyJWKSJWT(provider, token); err != nil {
		t.Fatalf("initial key rejected: %v", err)
	}

	fixture.Set(http.StatusServiceUnavailable)
	clock.Advance(hardMaxStaleTTL + time.Second)
	if err := verifyJWKSJWT(provider, token); err == nil {
		t.Fatal("cached key was accepted beyond the hard maximum age")
	}
	if got := fixture.calls.Load(); got != 2 {
		t.Fatalf("JWKS fetch count = %d, want refresh after hard maximum age", got)
	}
}

func TestJWKSRefreshAtHardAgeReplacesActiveKeys(t *testing.T) {
	provider, fixture, clock := newJWKSFixture(t, 24*time.Hour)
	oldPrivateKey, oldPublicKey := testJWKSKey(t, "old-key")
	newPrivateKey, newPublicKey := testJWKSKey(t, "new-key")
	fixture.Set(http.StatusOK, oldPublicKey)
	oldToken := signedJWKSJWT(t, oldPrivateKey, oldPublicKey.Kid)
	if err := verifyJWKSJWT(provider, oldToken); err != nil {
		t.Fatalf("initial key rejected: %v", err)
	}

	fixture.Set(http.StatusOK, newPublicKey)
	clock.Advance(hardMaxStaleTTL)
	if err := verifyJWKSJWT(provider, oldToken); err == nil {
		t.Fatal("old key remained active after a successful refresh")
	}
	newToken := signedJWKSJWT(t, newPrivateKey, newPublicKey.Kid)
	if err := verifyJWKSJWT(provider, newToken); err != nil {
		t.Fatalf("refreshed key rejected: %v", err)
	}
	if got := fixture.calls.Load(); got != 2 {
		t.Fatalf("JWKS fetch count = %d, want exactly one refresh", got)
	}
}

func TestJWKSRefreshFailureAtHardAgeFailsClosed(t *testing.T) {
	provider, fixture, clock := newJWKSFixture(t, time.Minute)
	privateKey, publicKey := testJWKSKey(t, "key-a")
	fixture.Set(http.StatusOK, publicKey)
	token := signedJWKSJWT(t, privateKey, publicKey.Kid)
	if err := verifyJWKSJWT(provider, token); err != nil {
		t.Fatalf("initial key rejected: %v", err)
	}

	fixture.Set(http.StatusServiceUnavailable)
	clock.Advance(hardMaxStaleTTL)
	if err := verifyJWKSJWT(provider, token); err == nil {
		t.Fatal("cached key was accepted at the hard maximum age after refresh failure")
	}
}

func TestJWKSStaleFallbackStopsAtHardMaxAge(t *testing.T) {
	provider, fixture, clock := newJWKSFixture(t, 2*time.Minute)
	privateKey, publicKey := testJWKSKey(t, "key-a")
	fixture.Set(http.StatusOK, publicKey)
	token := signedJWKSJWT(t, privateKey, publicKey.Kid)
	if err := verifyJWKSJWT(provider, token); err != nil {
		t.Fatalf("initial key rejected: %v", err)
	}

	fixture.Set(http.StatusServiceUnavailable)
	clock.Advance(2*time.Minute + time.Second)
	if err := verifyJWKSJWT(provider, token); err != nil {
		t.Fatalf("stale key rejected within the permitted stale window: %v", err)
	}

	clock.Advance(hardMaxStaleTTL - (2*time.Minute + time.Second))
	if err := verifyJWKSJWT(provider, token); err == nil {
		t.Fatal("stale key was accepted at the hard maximum age")
	}
}

func TestJWKSUnknownKidRejectsWithoutFallbackAndIsThrottled(t *testing.T) {
	provider, fixture, _ := newJWKSFixture(t, time.Minute)
	privateKey, publicKey := testJWKSKey(t, "known-key")
	fixture.Set(http.StatusOK, publicKey)
	knownToken := signedJWKSJWT(t, privateKey, publicKey.Kid)
	if err := verifyJWKSJWT(provider, knownToken); err != nil {
		t.Fatalf("initial key rejected: %v", err)
	}

	unknownToken := signedJWKSJWT(t, privateKey, "unknown-key")
	if err := verifyJWKSJWT(provider, unknownToken); err == nil {
		t.Fatal("unknown kid fell back to a different cached key")
	}
	if err := verifyJWKSJWT(provider, unknownToken); err == nil {
		t.Fatal("unknown kid was accepted during refresh cooldown")
	}
	if got := fixture.calls.Load(); got != 1 {
		t.Fatalf("JWKS fetch count = %d, want no refresh during cooldown", got)
	}
}

func TestJWKSConcurrentUnknownKidsShareRefresh(t *testing.T) {
	provider, fixture, clock := newJWKSFixture(t, time.Minute)
	privateKey, publicKey := testJWKSKey(t, "known-key")
	fixture.Set(http.StatusOK, publicKey)
	knownToken := signedJWKSJWT(t, privateKey, publicKey.Kid)
	if err := verifyJWKSJWT(provider, knownToken); err != nil {
		t.Fatalf("initial key rejected: %v", err)
	}

	fixture.Set(http.StatusServiceUnavailable)
	clock.Advance(defaultRefreshCD)
	requestStarted, releaseRequest := fixture.BlockRequests()
	var releaseOnce sync.Once
	release := func() {
		releaseOnce.Do(func() {
			close(releaseRequest)
		})
	}
	defer release()
	const callers = 8
	start := make(chan struct{})
	results := make(chan error, callers)
	var ready sync.WaitGroup
	ready.Add(callers)
	for range callers {
		go func() {
			ready.Done()
			<-start
			_, err := provider.lookup(context.Background(), "unknown-key")
			results <- err
		}()
	}
	ready.Wait()
	close(start)
	select {
	case <-requestStarted:
	case <-time.After(time.Second):
		t.Fatal("no refresh request started")
	}
	release()

	for range callers {
		if err := <-results; err == nil {
			t.Fatal("unknown kid lookup unexpectedly succeeded")
		}
	}
	if got := fixture.calls.Load(); got != 2 {
		t.Fatalf("JWKS fetch count = %d, want one shared refresh", got)
	}
}
