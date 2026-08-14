package membertunnel

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/loykin/piper/internal/agentpb"
	"github.com/loykin/piper/internal/memberclient"
	"github.com/loykin/piper/pkg/project"
)

func TestMemberRejectsUnsignedAndWrongOwnerCalls(t *testing.T) {
	client := NewClient(Config{HomeID: "home-1", MemberID: "member-1", Token: "secret"}, &fakeMember{})
	ref := project.ProjectRef{HomeID: "home-1", MemberID: "member-1", ProjectID: "project-1"}
	unsigned, err := encodeCall(memberclient.AuthContext{}, ref, memberclient.SubmitRunRequest{})
	if err != nil {
		t.Fatal(err)
	}
	resp := client.handle(context.Background(), &agentpb.MemberRPCCommand{RequestId: "1", Method: MethodSubmitRun, Payload: unsigned})
	if !strings.Contains(resp.Error, "missing delegated authorization") {
		t.Fatalf("unsigned call error = %q", resp.Error)
	}

	req := memberclient.SubmitRunRequest{}
	reqPayload, err := json.Marshal(req)
	if err != nil {
		t.Fatal(err)
	}
	auth, err := memberclient.SignDelegation(memberclient.AuthContext{}, ref, MethodSubmitRun, reqPayload, "secret", time.Now())
	if err != nil {
		t.Fatal(err)
	}
	wrongOwner := ref
	wrongOwner.MemberID = "member-2"
	payload, err := encodeCall(auth, wrongOwner, req)
	if err != nil {
		t.Fatal(err)
	}
	resp = client.handle(context.Background(), &agentpb.MemberRPCCommand{RequestId: "2", Method: MethodSubmitRun, Payload: payload})
	if !strings.Contains(resp.Error, "owner does not match") {
		t.Fatalf("wrong owner error = %q", resp.Error)
	}
}
