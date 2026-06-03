package notebook

import "fmt"

const ContainerWorkDir = "/home/jovyan/work"

// JupyterStartArgs returns the canonical notebook server flags for container
// runtimes that use start-notebook.py as the entrypoint command.
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

// JupyterLabArgs returns the same canonical notebook server flags without the
// container entrypoint command, for direct host-process invocation.
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
