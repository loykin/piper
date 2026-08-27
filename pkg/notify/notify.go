// Package notify provides bounded-time delivery to configured notification
// endpoints. Secret configuration is passed as structured data and is never
// encoded back into a URL for logging or transport selection.
package notify

import (
	"context"
	"fmt"
	"net/http"
)

type Message struct {
	Title  string         `json:"title"`
	Body   string         `json:"body"`
	Fields map[string]any `json:"fields,omitempty"`
}

type Notifier interface {
	Send(context.Context, Message) error
}

func Open(kind string, data map[string]string, client *http.Client) (Notifier, error) {
	switch kind {
	case "slack":
		return newHTTPNotifier("slack", data["webhook_url"], nil, client)
	case "webhook":
		headers := make(map[string]string)
		for key, value := range data {
			if len(key) > len("header_") && key[:len("header_")] == "header_" {
				headers[key[len("header_"):]] = value
			}
		}
		return newHTTPNotifier("webhook", data["url"], headers, client)
	default:
		return nil, fmt.Errorf("unsupported notification kind %q", kind)
	}
}
