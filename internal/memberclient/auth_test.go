package memberclient

import (
	"strings"
	"testing"
	"time"

	"github.com/loykin/piper/pkg/project"
	"github.com/loykin/piper/pkg/security"
)

func TestDelegationSignatureBindsProjectAndExpires(t *testing.T) {
	now := time.Date(2026, 8, 15, 0, 0, 0, 0, time.UTC)
	ref := project.ProjectRef{HomeID: "home-1", MemberID: "member-1", ProjectID: "project-1"}
	payload := []byte(`{"yaml":"safe"}`)
	auth, err := SignDelegation(AuthContext{ActorID: "user-1", Role: security.ProjectRoleMember}, ref, "SubmitRun", payload, "secret", now)
	if err != nil {
		t.Fatal(err)
	}
	if err := VerifyDelegation(auth, ref, "SubmitRun", payload, "secret", now.Add(10*time.Second)); err != nil {
		t.Fatalf("valid delegation rejected: %v", err)
	}

	tampered := ref
	tampered.ProjectID = "project-2"
	if err := VerifyDelegation(auth, tampered, "SubmitRun", payload, "secret", now.Add(10*time.Second)); err == nil || !strings.Contains(err.Error(), "signature") {
		t.Fatalf("tampered project error = %v", err)
	}
	if err := VerifyDelegation(auth, ref, "SubmitRun", payload, "wrong-secret", now.Add(10*time.Second)); err == nil || !strings.Contains(err.Error(), "signature") {
		t.Fatalf("wrong key error = %v", err)
	}
	if err := VerifyDelegation(auth, ref, "SubmitRun", payload, "secret", auth.ExpiresAt); err == nil || !strings.Contains(err.Error(), "expired") {
		t.Fatalf("expired delegation error = %v", err)
	}
	if err := VerifyDelegation(auth, ref, "CancelRun", payload, "secret", now); err == nil || !strings.Contains(err.Error(), "operation") {
		t.Fatalf("operation mismatch error = %v", err)
	}
	if err := VerifyDelegation(auth, ref, "SubmitRun", []byte(`{"yaml":"tampered"}`), "secret", now); err == nil || !strings.Contains(err.Error(), "payload") {
		t.Fatalf("payload mismatch error = %v", err)
	}
}

func TestDelegationRejectsUnsignedAndFutureDated(t *testing.T) {
	now := time.Now().UTC()
	ref := project.ProjectRef{HomeID: "home-1", MemberID: "member-1", ProjectID: "project-1"}
	if err := VerifyDelegation(AuthContext{}, ref, "SubmitRun", nil, "secret", now); err == nil || !strings.Contains(err.Error(), "missing") {
		t.Fatalf("unsigned delegation error = %v", err)
	}
	auth, err := SignDelegation(AuthContext{}, ref, "SubmitRun", nil, "secret", now.Add(time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	if err := VerifyDelegation(auth, ref, "SubmitRun", nil, "secret", now); err == nil || !strings.Contains(err.Error(), "not yet valid") {
		t.Fatalf("future delegation error = %v", err)
	}
}
