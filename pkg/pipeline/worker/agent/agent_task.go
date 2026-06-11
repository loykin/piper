package agent

import (
	"encoding/base64"
	"encoding/json"
	"fmt"

	"github.com/piper/piper/internal/proto"
)

// EncodeTask serializes a task for one-shot agents.
func EncodeTask(task *proto.Task) (string, error) {
	data, err := json.Marshal(task)
	if err != nil {
		return "", fmt.Errorf("marshal task: %w", err)
	}
	return base64.StdEncoding.EncodeToString(data), nil
}

// DecodeTask decodes a task serialized by EncodeTask.
func DecodeTask(encoded string) (*proto.Task, error) {
	data, err := base64.StdEncoding.DecodeString(encoded)
	if err != nil {
		return nil, fmt.Errorf("decode task: %w", err)
	}
	var task proto.Task
	if err := json.Unmarshal(data, &task); err != nil {
		return nil, fmt.Errorf("unmarshal task: %w", err)
	}
	return &task, nil
}
