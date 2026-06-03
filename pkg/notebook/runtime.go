package notebook

import "fmt"

const ContainerWorkDir = "/home/jovyan/work"

// JupyterStartArgs returns args for Docker Stacks containers (jupyter/scipy-notebook etc.)
// which boot via start-notebook.py as the container entrypoint.
func JupyterStartArgs(baseURL, token, rootDir string, port int) []string {
	return []string{
		"start-notebook.py",
		"--ServerApp.base_url=" + baseURL,
		"--ServerApp.token=" + token,
		"--ServerApp.root_dir=" + rootDir,
		"--ServerApp.allow_origin=*",
		"--no-browser",
		fmt.Sprintf("--port=%d", port),
	}
}

// JupyterLabArgs returns args for direct `jupyter lab` / `jupyter-lab` invocation
// in process mode (no Docker Stacks entrypoint).
func JupyterLabArgs(baseURL, token, rootDir string, port int) []string {
	return []string{
		"--ServerApp.base_url=" + baseURL,
		"--ServerApp.token=" + token,
		"--ServerApp.root_dir=" + rootDir,
		"--ServerApp.allow_origin=*",
		"--no-browser",
		fmt.Sprintf("--port=%d", port),
	}
}
