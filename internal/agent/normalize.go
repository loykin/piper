package agent

import (
	"strings"
)

type PipelineWorker struct {
	ID           string
	Label        string
	Capabilities string
	Hostname     string
}

type NotebookWorker struct {
	ID       string
	Addr     string
	GPUs     []string
	Hostname string
}

type ServingWorker struct {
	ID       string
	Addr     string
	GPUs     []string
	Hostname string
}

func FromPipelineWorker(info PipelineWorker) Info {
	labels := map[string]string{}
	if info.Label != "" {
		labels["label"] = info.Label
	}
	return Info{
		ID:           info.ID,
		Kind:         KindBareMetal,
		Hostname:     info.Hostname,
		Capabilities: normalizeCapabilities(info.Capabilities, CapabilityPipeline),
		Labels:       labels,
	}
}

func FromNotebookWorker(info NotebookWorker) Info {
	return Info{
		ID:           info.ID,
		Kind:         KindBareMetal,
		Addr:         info.Addr,
		Hostname:     info.Hostname,
		GPUs:         info.GPUs,
		Capabilities: []string{CapabilityNotebook},
	}
}

func FromServingWorker(info ServingWorker) Info {
	return Info{
		ID:           info.ID,
		Kind:         KindBareMetal,
		Addr:         info.Addr,
		Hostname:     info.Hostname,
		GPUs:         info.GPUs,
		Capabilities: []string{CapabilityServing},
	}
}

func normalizeCapabilities(raw string, fallback string) []string {
	seen := map[string]bool{}
	out := []string{}
	for _, part := range strings.Split(raw, ",") {
		capability := strings.TrimSpace(part)
		if capability == "" || seen[capability] {
			continue
		}
		seen[capability] = true
		out = append(out, capability)
	}
	if len(out) == 0 && fallback != "" {
		out = append(out, fallback)
	}
	return out
}
