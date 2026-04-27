package runner

import (
	"encoding/base64"
	"encoding/json"
	"fmt"

	"github.com/piper/piper/pkg/pipeline"
	"github.com/piper/piper/pkg/proto"
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

// TaskFromAgentInput builds the task an agent should execute.
//
// New launchers should pass taskB64 so the worker and agent execute the same
// proto.Task contract. The step-based fields remain as a compatibility path for
// older launchers.
func TaskFromAgentInput(taskB64, taskID, runID, stepName, stepB64 string, commandOverride []string) (*proto.Task, error) {
	var task *proto.Task
	if taskB64 != "" {
		decoded, err := DecodeTask(taskB64)
		if err != nil {
			return nil, err
		}
		task = decoded
	} else {
		legacy, err := taskFromLegacyAgentInput(taskID, runID, stepName, stepB64)
		if err != nil {
			return nil, err
		}
		task = legacy
	}
	if err := overrideTaskCommand(task, commandOverride); err != nil {
		return nil, err
	}
	return task, nil
}

func taskFromLegacyAgentInput(taskID, runID, stepName, stepB64 string) (*proto.Task, error) {
	var step pipeline.Step
	if stepB64 != "" {
		data, err := base64.StdEncoding.DecodeString(stepB64)
		if err != nil {
			return nil, fmt.Errorf("decode step: %w", err)
		}
		if err := json.Unmarshal(data, &step); err != nil {
			return nil, fmt.Errorf("unmarshal step: %w", err)
		}
	}
	stepJSON, err := json.Marshal(step)
	if err != nil {
		return nil, fmt.Errorf("marshal step: %w", err)
	}
	return &proto.Task{
		ID:       taskID,
		RunID:    runID,
		StepName: stepName,
		Step:     stepJSON,
	}, nil
}

func overrideTaskCommand(task *proto.Task, commandOverride []string) error {
	if len(commandOverride) == 0 {
		return nil
	}
	var step pipeline.Step
	if len(task.Step) > 0 {
		if err := json.Unmarshal(task.Step, &step); err != nil {
			return fmt.Errorf("unmarshal step: %w", err)
		}
	}
	step.Run.Command = commandOverride
	stepJSON, err := json.Marshal(step)
	if err != nil {
		return fmt.Errorf("marshal step: %w", err)
	}
	task.Step = stepJSON
	return nil
}
