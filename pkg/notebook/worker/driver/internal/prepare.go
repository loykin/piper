package internal

import "github.com/piper/piper/pkg/notebook"

// PrepareStepsForBackend resolves prepare steps for a concrete driver backend.
func PrepareStepsForBackend(spec *notebook.NotebookPrepareSpec, backend string) ([]notebook.NotebookPrepareStep, error) {
	if spec == nil {
		return nil, nil
	}
	return spec.StepsForBackend(backend)
}
