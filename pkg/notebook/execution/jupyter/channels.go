package jupyter

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/gorilla/websocket"
)

// OutputSink receives cell outputs as they stream off the kernel's iopub
// channel, in arrival order, for exactly one in-flight ExecuteCell call.
type OutputSink interface {
	// OnOutput is called for each stream/display_data/execute_result/error
	// message the kernel produces while the cell runs.
	OnOutput(Output)
	// OnClearOutput is called when the kernel sends a clear_output message
	// (e.g. from a tqdm-style progress bar). Piper applies it immediately —
	// there is no "wait" buffering, since outputs are already only
	// collected in memory for the duration of one cell.
	OnClearOutput()
}

// ExecuteResult summarizes a completed (or aborted) execute_request.
type ExecuteResult struct {
	ExecutionCount int
	Status         string // "ok" | "error" | "abort"
	ErrorName      string
	ErrorValue     string
	Traceback      []string
}

// msgHeader is the Jupyter Messaging Protocol v5.3 message header
// (https://jupyter-client.readthedocs.io/en/stable/messaging.html#general-message-format).
type msgHeader struct {
	MsgID    string `json:"msg_id"`
	Username string `json:"username"`
	Session  string `json:"session"`
	MsgType  string `json:"msg_type"`
	Version  string `json:"version"`
	Date     string `json:"date"`
}

type wireMessage struct {
	Header       msgHeader       `json:"header"`
	ParentHeader json.RawMessage `json:"parent_header"`
	Metadata     json.RawMessage `json:"metadata"`
	Content      json.RawMessage `json:"content"`
	Channel      string          `json:"channel"`
	Buffers      []string        `json:"buffers,omitempty"`
}

type executeRequestContent struct {
	Code            string            `json:"code"`
	Silent          bool              `json:"silent"`
	StoreHistory    bool              `json:"store_history"`
	UserExpressions map[string]string `json:"user_expressions"`
	AllowStdin      bool              `json:"allow_stdin"`
	StopOnError     bool              `json:"stop_on_error"`
}

// Channel is one open kernel channels WebSocket connection, reused across
// every cell of a single NotebookExecution run — per design doc §5.1, "한
// Kernel에는 동시에 하나의 execute 요청만 전달한다", so callers must not use
// the same Channel concurrently from two goroutines; ExecuteCell already
// serializes sends internally but relies on the caller to await one
// ExecuteCell's return before issuing the next.
type Channel struct {
	conn      *websocket.Conn
	sessionID string
	username  string
	writeMu   sync.Mutex
}

// DialChannel opens the kernel channels WebSocket for kernelID, scoped to
// piperSessionID (Piper's own KernelSession.ID, reused as the Jupyter
// messaging-protocol "session" field so replies can be correlated back to
// this Channel — distinct from the Jupyter-native session/kernel IDs the
// REST client deals with).
func DialChannel(ctx context.Context, endpoint, projectID, notebookName, kernelID, piperSessionID, token string) (*Channel, error) {
	wsURL, err := BuildWebSocketURL(endpoint, projectID, notebookName, kernelID, piperSessionID)
	if err != nil {
		return nil, err
	}
	header := http.Header{}
	if token != "" {
		header.Set("Authorization", "token "+token)
	}
	dialer := websocket.Dialer{HandshakeTimeout: 15 * time.Second}
	conn, _, err := dialer.DialContext(ctx, wsURL, header)
	if err != nil {
		return nil, &opaqueError{op: "kernel channel connect", err: err}
	}
	return &Channel{conn: conn, sessionID: piperSessionID, username: "piper"}, nil
}

// Close closes the underlying WebSocket connection.
func (ch *Channel) Close() error {
	return ch.conn.Close()
}

// ExecuteCell sends one execute_request on the shell channel and blocks
// until the kernel reports both the shell execute_reply and the iopub
// "idle" status for that request (or ctx is done), routing every
// stream/display_data/execute_result/error/clear_output message produced in
// between to sink. Messages whose parent_header doesn't match this
// request's msg_id are drained and ignored — they belong to another
// concurrent caller of this session (e.g. a human's Jupyter UI tab watching
// the same kernel), which Piper does not attempt to suppress or take over.
func (ch *Channel) ExecuteCell(ctx context.Context, code string, sink OutputSink) (*ExecuteResult, error) {
	msgID := uuid.NewString()
	content, err := json.Marshal(executeRequestContent{
		Code:            code,
		Silent:          false,
		StoreHistory:    true,
		UserExpressions: map[string]string{},
		AllowStdin:      false,
		StopOnError:     true,
	})
	if err != nil {
		return nil, fmt.Errorf("jupyter: encode execute_request: %w", err)
	}
	req := wireMessage{
		Header: msgHeader{
			MsgID:    msgID,
			Username: ch.username,
			Session:  ch.sessionID,
			MsgType:  "execute_request",
			Version:  "5.3",
			Date:     time.Now().UTC().Format(time.RFC3339Nano),
		},
		ParentHeader: json.RawMessage(`{}`),
		Metadata:     json.RawMessage(`{}`),
		Content:      content,
		Channel:      "shell",
	}

	ch.writeMu.Lock()
	err = ch.conn.WriteJSON(req)
	ch.writeMu.Unlock()
	if err != nil {
		return nil, &opaqueError{op: "send execute_request", err: err}
	}

	result := &ExecuteResult{Status: "ok"}
	shellReplyReceived, idleReceived := false, false
	for !(shellReplyReceived && idleReceived) {
		if ctx.Err() != nil {
			return result, ctx.Err()
		}
		if dl, ok := ctx.Deadline(); ok {
			_ = ch.conn.SetReadDeadline(dl)
		}
		var msg wireMessage
		if err := ch.conn.ReadJSON(&msg); err != nil {
			if ctx.Err() != nil {
				return result, ctx.Err()
			}
			return nil, &opaqueError{op: "read kernel message", err: err}
		}
		var parent msgHeader
		_ = json.Unmarshal(msg.ParentHeader, &parent)
		if parent.MsgID != msgID {
			continue
		}
		switch msg.Channel {
		case "iopub":
			ch.handleIOPub(msg, sink, result, &idleReceived)
		case "shell":
			if msg.Header.MsgType == "execute_reply" {
				ch.handleExecuteReply(msg, result)
				shellReplyReceived = true
			}
		}
	}
	return result, nil
}

func (ch *Channel) handleIOPub(msg wireMessage, sink OutputSink, result *ExecuteResult, idleReceived *bool) {
	switch msg.Header.MsgType {
	case "status":
		var c struct {
			ExecutionState string `json:"execution_state"`
		}
		_ = json.Unmarshal(msg.Content, &c)
		if c.ExecutionState == "idle" {
			*idleReceived = true
		}
	case "stream":
		var c struct {
			Name string `json:"name"`
			Text string `json:"text"`
		}
		_ = json.Unmarshal(msg.Content, &c)
		sink.OnOutput(Output{OutputType: "stream", Name: c.Name, Text: NewSource(c.Text)})
	case "display_data":
		var c struct {
			Data     map[string]json.RawMessage `json:"data"`
			Metadata map[string]json.RawMessage `json:"metadata"`
		}
		_ = json.Unmarshal(msg.Content, &c)
		sink.OnOutput(Output{OutputType: "display_data", Data: c.Data, Metadata: c.Metadata})
	case "execute_result":
		var c struct {
			ExecutionCount int                        `json:"execution_count"`
			Data           map[string]json.RawMessage `json:"data"`
			Metadata       map[string]json.RawMessage `json:"metadata"`
		}
		_ = json.Unmarshal(msg.Content, &c)
		ec := c.ExecutionCount
		sink.OnOutput(Output{OutputType: "execute_result", ExecutionCount: &ec, Data: c.Data, Metadata: c.Metadata})
	case "error":
		var c struct {
			Ename     string   `json:"ename"`
			Evalue    string   `json:"evalue"`
			Traceback []string `json:"traceback"`
		}
		_ = json.Unmarshal(msg.Content, &c)
		sink.OnOutput(Output{OutputType: "error", Ename: c.Ename, Evalue: c.Evalue, Traceback: c.Traceback})
		result.ErrorName, result.ErrorValue, result.Traceback = c.Ename, c.Evalue, c.Traceback
	case "clear_output":
		sink.OnClearOutput()
	}
}

func (ch *Channel) handleExecuteReply(msg wireMessage, result *ExecuteResult) {
	var c struct {
		Status         string   `json:"status"`
		ExecutionCount int      `json:"execution_count"`
		Ename          string   `json:"ename"`
		Evalue         string   `json:"evalue"`
		Traceback      []string `json:"traceback"`
	}
	_ = json.Unmarshal(msg.Content, &c)
	result.Status = c.Status
	result.ExecutionCount = c.ExecutionCount
	if c.Status == "error" {
		result.ErrorName, result.ErrorValue, result.Traceback = c.Ename, c.Evalue, c.Traceback
	}
}

// Note: kernel interrupt goes through the REST API (Client.InterruptKernel),
// not this WebSocket channel — the Jupyter protocol has no iopub/shell
// interrupt message.
