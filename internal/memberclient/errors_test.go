package memberclient

import (
	"errors"
	"fmt"
	"testing"

	"github.com/loykin/piper/pkg/statsstore"
)

func TestRPCErrorEnvelopeRestoresStableSentinel(t *testing.T) {
	original := fmt.Errorf("%w: clickhouse timed out", statsstore.ErrBackendUnavailable)
	encoded := EncodeRPCError(original)
	decoded := DecodeRPCError(encoded)
	if !errors.Is(decoded, statsstore.ErrBackendUnavailable) {
		t.Fatalf("decoded error = %v", decoded)
	}
	if decoded.Error() == statsstore.ErrBackendUnavailable.Error() {
		t.Fatalf("backend detail was lost: %v", decoded)
	}
}
