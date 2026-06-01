package meta

func Labels(clusterName, kind, id string) map[string]string {
	return map[string]string{
		"app.kubernetes.io/managed-by": "piper",
		"piper.io/cluster":             clusterName,
		"piper.io/workload-kind":       kind,
		"piper.io/workload-id":         id,
	}
}
