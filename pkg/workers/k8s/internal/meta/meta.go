package meta

import "github.com/piper/piper/pkg/internal/k8smeta"

func Labels(clusterName, kind, id string) map[string]string {
	return k8smeta.WorkloadLabels(clusterName, kind, id)
}
