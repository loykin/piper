package k8smeta

import "strings"

const (
	LabelManagedBy    = "app.kubernetes.io/managed-by"
	LabelCluster      = "piper.io/cluster"
	LabelWorkloadKind = "piper.io/workload-kind"
	LabelWorkloadID   = "piper.io/workload-id"
	LabelWorkerID     = "piper.io/worker-id"
	LabelRunID        = "piper.io/run-id"
	LabelStepName     = "piper.io/step-name"

	AnnotationTaskID     = "piper.io/task-id"
	AnnotationAttempt    = "piper.io/attempt"
	AnnotationRunID      = "piper.io/run-id"
	AnnotationStepName   = "piper.io/step-name"
	AnnotationVolumeID   = "piper.io/volume-id"
	AnnotationWorkloadID = "piper.io/workload-id"
	AnnotationProjectID  = "piper.io/project-id"

	ManagedByPiper = "piper"
)

// LabelValue applies the encoding contract used by both resource creation and
// label selectors. Kubernetes label values are limited to 63 characters and
// must start and end with an alphanumeric character when non-empty.
func LabelValue(value string) string {
	var b strings.Builder
	for _, c := range value {
		switch {
		case c >= 'a' && c <= 'z', c >= 'A' && c <= 'Z',
			c >= '0' && c <= '9', c == '-', c == '_', c == '.':
			b.WriteRune(c)
		default:
			b.WriteByte('-')
		}
	}
	encoded := b.String()
	if len(encoded) > 63 {
		encoded = encoded[:63]
	}
	return strings.Trim(encoded, "-_.")
}

func ManagedSelector() string {
	return LabelManagedBy + "=" + ManagedByPiper
}

func WorkerSelector(workerID string) string {
	return ManagedSelector() + "," + LabelWorkerID + "=" + LabelValue(workerID)
}

func WorkloadLabels(clusterName, kind, id string) map[string]string {
	return map[string]string{
		LabelManagedBy:    ManagedByPiper,
		LabelCluster:      LabelValue(clusterName),
		LabelWorkloadKind: LabelValue(kind),
		LabelWorkloadID:   LabelValue(id),
	}
}

func WorkloadAnnotations(id string) map[string]string {
	return map[string]string{AnnotationWorkloadID: id}
}

// SafeName converts an arbitrary name to a Kubernetes-safe resource name.
// The result is lowercase, uses only [a-z0-9-], and is at most 63 characters.
func SafeName(name string) string {
	safe := strings.ToLower(name)
	var b strings.Builder
	for _, c := range safe {
		switch {
		case c >= 'a' && c <= 'z', c >= '0' && c <= '9', c == '-':
			b.WriteRune(c)
		default:
			b.WriteRune('-')
		}
	}
	s := strings.Trim(b.String(), "-")
	if len(s) > 63 {
		s = strings.TrimRight(s[:63], "-")
	}
	return s
}
