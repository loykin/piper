package notebook

import "fmt"

const ContainerWorkDir = "/home/jovyan/work"

// JupyterStartArgs returns the canonical notebook server flags for container
// runtimes that use start-notebook.py as the entrypoint command.
// Pass an empty token to disable token auth (master proxy is the security boundary).
func JupyterStartArgs(baseURL, token, rootDir string, port int) []string {
	return []string{
		"start-notebook.py",
		"--ServerApp.base_url=" + baseURL,
		"--ServerApp.token=" + token,
		"--IdentityProvider.token=" + token,
		"--ServerApp.root_dir=" + rootDir,
		"--ServerApp.trust_xheaders=True",
		"--ServerApp.allow_origin=*",
		"--no-browser",
		"--ServerApp.port_retries=0",
		fmt.Sprintf("--ServerApp.port=%d", port),
	}
}

// JupyterLabArgs returns the same canonical notebook server flags without the
// container entrypoint command, for direct host-process invocation.
// Pass an empty token to disable token auth (master proxy is the security boundary).
func JupyterLabArgs(baseURL, token, rootDir string, port int) []string {
	return []string{
		"--ServerApp.base_url=" + baseURL,
		"--ServerApp.token=" + token,
		"--IdentityProvider.token=" + token,
		"--ServerApp.root_dir=" + rootDir,
		"--ServerApp.trust_xheaders=True",
		"--ServerApp.allow_origin=*",
		"--no-browser",
		"--ServerApp.port_retries=0",
		fmt.Sprintf("--ServerApp.port=%d", port),
	}
}
