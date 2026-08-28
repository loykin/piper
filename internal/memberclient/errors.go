package memberclient

import (
	"encoding/json"
	"errors"
	"fmt"

	"github.com/loykin/piper/pkg/statsstore"
)

const (
	ErrorCodeInternal                = "internal"
	ErrorCodeRunNotFound             = "run_not_found"
	ErrorCodeMemberUnavailable       = "member_unavailable"
	ErrorCodeStatsBackendUnavailable = "stats_backend_unavailable"
	ErrorCodeInvalidStatsCursor      = "invalid_stats_cursor"
	ErrorCodeStorageBackendMismatch  = "storage_backend_mismatch"
)

type RPCErrorEnvelope struct {
	Code      string `json:"code"`
	Message   string `json:"message"`
	Retryable bool   `json:"retryable"`
}

func EncodeRPCError(err error) string {
	envelope := RPCErrorEnvelope{Code: ErrorCodeInternal, Message: err.Error()}
	switch {
	case errors.Is(err, ErrRunNotFound):
		envelope.Code = ErrorCodeRunNotFound
	case errors.Is(err, ErrMemberUnavailable):
		envelope.Code = ErrorCodeMemberUnavailable
		envelope.Retryable = true
	case errors.Is(err, statsstore.ErrBackendUnavailable):
		envelope.Code = ErrorCodeStatsBackendUnavailable
		envelope.Message = "statistics backend unavailable"
		envelope.Retryable = true
	case errors.Is(err, statsstore.ErrInvalidCursor):
		envelope.Code = ErrorCodeInvalidStatsCursor
	case errors.Is(err, ErrStorageBackendMismatch):
		envelope.Code = ErrorCodeStorageBackendMismatch
	}
	encoded, marshalErr := json.Marshal(envelope)
	if marshalErr != nil {
		return err.Error()
	}
	return string(encoded)
}

func DecodeRPCError(encoded string) error {
	var envelope RPCErrorEnvelope
	if err := json.Unmarshal([]byte(encoded), &envelope); err != nil || envelope.Message == "" {
		return errors.New(encoded)
	}
	var sentinel error
	switch envelope.Code {
	case ErrorCodeRunNotFound:
		sentinel = ErrRunNotFound
	case ErrorCodeMemberUnavailable:
		sentinel = ErrMemberUnavailable
	case ErrorCodeStatsBackendUnavailable:
		sentinel = statsstore.ErrBackendUnavailable
	case ErrorCodeInvalidStatsCursor:
		sentinel = statsstore.ErrInvalidCursor
	case ErrorCodeStorageBackendMismatch:
		sentinel = ErrStorageBackendMismatch
	default:
		return errors.New(envelope.Message)
	}
	return fmt.Errorf("%w: %s", sentinel, envelope.Message)
}
