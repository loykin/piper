package notebook

import "fmt"

const ContainerWorkDir = "/home/jovyan/work"

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
