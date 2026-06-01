package rpcutil

import (
	"context"
	"encoding/json"
)

type Dispatcher interface {
	Register(method string, handler func(ctx context.Context, payload json.RawMessage) (any, error)) error
}

func RegisterJSON[T, R any](dispatcher Dispatcher, method string, handler func(context.Context, T) (R, error)) error {
	return dispatcher.Register(method, func(ctx context.Context, payload json.RawMessage) (any, error) {
		var req T
		if err := json.Unmarshal(payload, &req); err != nil {
			return nil, err
		}
		return handler(ctx, req)
	})
}
