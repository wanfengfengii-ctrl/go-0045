// Package credential implements one-time credential issuance and verification.
//
// A credential is a high-entropy token returned to the reader exactly once at
// issue time. Only an irreversible SHA-256 hash of the token is persisted, so a
// database leak never yields a usable credential. The token binds to a specific
// order and slot and carries a hard expiry; redemption checks the hash, expiry,
// single-use status and order/slot binding before consuming the credential.
package credential

import (
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"fmt"
	"sync/atomic"
	"time"

	"library-locker-lease-controller/internal/domain"
)

// randRead is the crypto/rand reader function, captured as a package-level
// value so NewService can pass it without shadowing the import.
var randRead = rand.Read

// TokenLen is the number of random bytes in a freshly issued token (32 bytes =
// 256 bits of entropy). The textual form is 64 hex characters.
const TokenLen = 32

// Service issues and verifies one-time credentials. The random source is
// replaceable so tests can make issuance deterministic.
type Service struct {
	rand func(p []byte) (int, error)
}

// NewService returns a Service backed by crypto/rand.
func NewService() *Service {
	return &Service{rand: randRead}
}

// NewServiceWithRand returns a Service whose token randomness comes from rand.
// Tests pass a deterministic generator (e.g. a counter) so credentials are
// reproducible.
func NewServiceWithRand(rand func(p []byte) (int, error)) *Service {
	if rand == nil {
		rand = randRead
	}
	return &Service{rand: rand}
}

// Issue mints a new credential for the given order and slot. It returns the
// plaintext token (handed to the reader once) and its irreversible hash (to be
// persisted). The caller is responsible for persisting the hash before
// returning the token.
func (s *Service) Issue(orderID, slotID string, issuedAt, expiresAt time.Time) (token, hash, id string) {
	raw := make([]byte, TokenLen)
	if _, err := s.rand(raw); err != nil {
		// crypto/rand failing is catastrophic; surface it as a storage error so
		// the caller does not silently issue a predictable credential.
		panic(fmt.Sprintf("credential: rand: %v", err))
	}
	token = EncodeToken(raw)
	hash = HashToken(token)
	id = "cred_" + hash[:16]
	_ = orderID
	_ = slotID
	_ = issuedAt
	_ = expiresAt
	return token, hash, id
}

// EncodeToken renders raw bytes as a prefixed hex string. The prefix lets
// auditors recognize a token without revealing anything about its value.
func EncodeToken(raw []byte) string {
	return "lt_" + hex.EncodeToString(raw)
}

// HashToken returns the lower-case hex SHA-256 of token. It is irreversible:
// the plaintext token cannot be recovered from the hash.
func HashToken(token string) string {
	sum := sha256.Sum256([]byte(token))
	return hex.EncodeToString(sum[:])
}

// VerifyHash reports whether token hashes to expectedHash using a
// constant-time comparison to avoid timing oracles.
func (s *Service) VerifyHash(token, expectedHash string) bool {
	got := HashToken(token)
	return subtle.ConstantTimeCompare([]byte(got), []byte(expectedHash)) == 1
}

// Verify performs full validation of a presented token against a persisted
// credential and the expected order/slot binding. It returns a distinguishable
// domain error for each failure mode and never mutates the credential.
func (s *Service) Verify(token string, c domain.OneTimeCredential, orderID, slotID string, now time.Time) error {
	if !s.VerifyHash(token, c.Hash) {
		return fmt.Errorf("%w: hash mismatch", domain.ErrCredentialMismatch)
	}
	if c.IsExpiredAt(now) {
		return fmt.Errorf("%w: expired at %s", domain.ErrCredentialExpired, c.ExpiresAt.Format(time.RFC3339))
	}
	if c.Status == domain.CredentialConsumed {
		return fmt.Errorf("%w: already consumed", domain.ErrCredentialUsed)
	}
	if c.Status != domain.CredentialIssued {
		return fmt.Errorf("%w: status %s", domain.ErrCredentialMismatch, c.Status)
	}
	if !c.MatchesOrder(orderID, slotID) {
		return fmt.Errorf("%w: order/slot binding mismatch", domain.ErrCredentialMismatch)
	}
	return nil
}

// DeterministicRand returns a random source backed by a monotonic counter. It
// is intended for tests: each call fills p with successive counter bytes, so
// tokens are reproducible across runs. It is safe for concurrent use.
func DeterministicRand() func(p []byte) (int, error) {
	var counter uint64
	return func(p []byte) (int, error) {
		for i := range p {
			v := atomic.AddUint64(&counter, 1)
			p[i] = byte(v)
		}
		return len(p), nil
	}
}
