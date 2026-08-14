package memberclient

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/loykin/piper/pkg/project"
)

const DelegationTTL = time.Minute

// SignDelegation turns Home's authorization decision into a short-lived,
// project-, operation-, and payload-bound credential that a remote Member
// can verify. The enrollment token is reused only as HMAC key material with
// a domain-separated payload.
func SignDelegation(auth AuthContext, ref project.ProjectRef, operation string, payload []byte, key string, now time.Time) (AuthContext, error) {
	if strings.TrimSpace(key) == "" {
		return AuthContext{}, fmt.Errorf("memberclient: delegation signing key is empty")
	}
	if strings.TrimSpace(operation) == "" {
		return AuthContext{}, fmt.Errorf("memberclient: delegation operation is empty")
	}
	auth.IssuedAt = now.UTC()
	auth.ExpiresAt = auth.IssuedAt.Add(DelegationTTL)
	auth.Operation = operation
	sum := sha256.Sum256(payload)
	auth.PayloadHash = base64.RawURLEncoding.EncodeToString(sum[:])
	auth.Signature = signature(auth, ref, key)
	return auth, nil
}

// VerifyDelegation rejects forged, expired, future-dated, and overly long
// authorization contexts before a Member dispatches a tunneled operation.
func VerifyDelegation(auth AuthContext, ref project.ProjectRef, operation string, payload []byte, key string, now time.Time) error {
	if strings.TrimSpace(key) == "" || auth.Signature == "" {
		return fmt.Errorf("memberclient: missing delegated authorization signature")
	}
	now = now.UTC()
	if auth.IssuedAt.IsZero() || auth.ExpiresAt.IsZero() || !auth.ExpiresAt.After(auth.IssuedAt) {
		return fmt.Errorf("memberclient: invalid delegated authorization lifetime")
	}
	if auth.ExpiresAt.Sub(auth.IssuedAt) > DelegationTTL {
		return fmt.Errorf("memberclient: delegated authorization lifetime exceeds limit")
	}
	if auth.IssuedAt.After(now.Add(30 * time.Second)) {
		return fmt.Errorf("memberclient: delegated authorization is not yet valid")
	}
	if !now.Before(auth.ExpiresAt) {
		return fmt.Errorf("memberclient: delegated authorization expired")
	}
	if auth.Operation != operation {
		return fmt.Errorf("memberclient: delegated authorization operation mismatch")
	}
	sum := sha256.Sum256(payload)
	wantPayloadHash := base64.RawURLEncoding.EncodeToString(sum[:])
	if !hmac.Equal([]byte(wantPayloadHash), []byte(auth.PayloadHash)) {
		return fmt.Errorf("memberclient: delegated authorization payload mismatch")
	}
	want := signature(auth, ref, key)
	if !hmac.Equal([]byte(want), []byte(auth.Signature)) {
		return fmt.Errorf("memberclient: invalid delegated authorization signature")
	}
	return nil
}

func signature(auth AuthContext, ref project.ProjectRef, key string) string {
	mac := hmac.New(sha256.New, []byte(key))
	fields := []string{
		"piper-member-delegation-v1",
		ref.HomeID,
		ref.MemberID,
		ref.ProjectID,
		auth.ActorID,
		strconv.Itoa(int(auth.Role)),
		auth.Operation,
		auth.PayloadHash,
		strconv.FormatInt(auth.IssuedAt.UnixNano(), 10),
		strconv.FormatInt(auth.ExpiresAt.UnixNano(), 10),
	}
	for _, field := range fields {
		_, _ = fmt.Fprintf(mac, "%d:%s|", len(field), field)
	}
	return base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
}
