package run

// SweepTrial is one parameter combination in a sweep.
type SweepTrial struct {
	Params map[string]any `json:"params"`
}

// SweepRequest is the body for POST /experiments.
type SweepRequest struct {
	YAML       string       `json:"yaml"`
	Experiment string       `json:"experiment"`
	Runs       []SweepTrial `json:"runs"`
}

// SweepResponse is returned by POST /experiments.
type SweepResponse struct {
	Experiment string   `json:"experiment"`
	RunIDs     []string `json:"run_ids"`
}
