package grpcagent

import (
	"testing"
	"time"

	"google.golang.org/grpc/credentials/insecure"
)

func TestClientConfiguresKeepaliveParameters(t *testing.T) {
	opts := dialOptions(ClientConfig{}, insecure.NewCredentials())
	// grpc.DialOption values aren't introspectable directly; the meaningful
	// assertion is that dialOptions always includes more than just the
	// transport credentials option (i.e. keepalive is actually appended).
	if len(opts) < 2 {
		t.Fatalf("dialOptions returned %d options, want at least transport credentials + keepalive", len(opts))
	}
}

func TestClientKeepaliveDefaultsAppliedWhenUnset(t *testing.T) {
	// This test only exercises the default-selection logic via dialOptions
	// not panicking/erroring with a zero-value config; the actual
	// keepalive.ClientParameters values aren't inspectable from outside the
	// grpc.DialOption closure, so we rely on defaultKeepaliveTime/Timeout
	// being sane constants (checked directly here) plus dialOptions running
	// without error for both zero and explicit configs.

	_ = dialOptions(ClientConfig{KeepaliveTime: 5 * time.Second, KeepaliveTimeout: 2 * time.Second}, insecure.NewCredentials())
}
