package notebook

import "fmt"

const ContainerWorkDir = "/home/jovyan/work"

// allowHiddenArg permits the Contents API to create/read dot-prefixed
// files and directories — jupyter_server's FileContentsManager rejects
// them by default (ContentsManager.allow_hidden defaults to False).
// docs/jupyter-mcp-execution.md §5.3 puts execution result notebooks at
// .piper/executions/{execution_id}/result.ipynb specifically so they stay
// out of a user's normal JupyterLab file browser view; without this flag
// every write under that path 400s with "Cannot create file or directory"
// — confirmed live against a real jupyter_server (a non-dot-prefixed
// directory at the same nesting depth succeeds with the exact same
// request shape, isolating the cause to the leading dot, not depth or
// permissions).
const allowHiddenArg = "--ContentsManager.allow_hidden=True"

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
		allowHiddenArg,
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
		allowHiddenArg,
		fmt.Sprintf("--ServerApp.port=%d", port),
	}
}
