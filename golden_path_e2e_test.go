//go:build golden_e2e

// Golden-path e2e: the flow a user actually takes — launch a notebook, write
// ML code in it, turn it into a pipeline template, run it — on each runtime,
// against a server configured the way it ships (built-in auth, built-in
// artifact store, workload_token, non-root jupyter image). The pipeline YAML
// is testdata/golden/notebook-template.<runtime>.yaml, which
// frontend/test/golden-templates.test.ts keeps equal to the editor's output,
// so this always runs what the UI submits.
//
// The three runtimes differ only in how the server and its environment come
// up; runGoldenPath is the same for all of them.
//
//	make test-golden-e2e                         # baremetal + docker
//	PIPER_GOLDEN_K8S_IMAGE=piper/piper:dev make test-golden-e2e   # + k8s
//
// Needs python3 and network access (pip). Docker needs a reachable daemon
// (on Linux set PIPER_GOLDEN_DOCKER_HOST=172.17.0.1); k8s needs kubectl, a
// cluster, and the image already loaded into it.
package piper_test

import (
	"bytes"
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"
)

const (
	goldenProject  = "default"
	goldenNotebook = "ml-nb"
	goldenImage    = "jupyter/minimal-notebook:latest"
)

// goldenNotebookCode reads its parameter as-is, so the run proves the editor's
// n_estimators=120 reaches the notebook as an int.
var goldenNotebookCode = map[string]any{
	"nbformat": 4, "nbformat_minor": 5,
	"metadata": map[string]any{"kernelspec": map[string]any{"name": "python3", "display_name": "Python 3", "language": "python"}},
	"cells": []any{
		map[string]any{"cell_type": "code", "metadata": map[string]any{"tags": []string{"parameters"}}, "source": "n_estimators = 50\n", "outputs": []any{}, "execution_count": nil},
		map[string]any{"cell_type": "code", "metadata": map[string]any{}, "outputs": []any{}, "execution_count": nil, "source": `from sklearn.datasets import load_iris
from sklearn.ensemble import RandomForestClassifier
X, y = load_iris(return_X_y=True)
clf = RandomForestClassifier(n_estimators=n_estimators, random_state=0).fit(X, y)
print("golden n_estimators", n_estimators, type(n_estimators).__name__)
`},
	},
}

func TestGoldenPath_Baremetal(t *testing.T) {
	requireGoldenPython(t)
	port := goldenFreePort(t)
	base := goldenStartServer(t, fmt.Sprintf(`
version: 4
server:
  http_addr: "127.0.0.1:%d"
  data_dir: ./piper-data
runtime:
  type: baremetal
notebook:
  notebooks_root: ./notebooks
  port_range: %d-%d
`, port, port+1, port+20), port)

	runGoldenPath(t, base, `apiVersion: piper/v1
kind: Notebook
metadata:
  name: ml-nb
spec:
  driver:
    placement:
      runtime: baremetal
`, goldenTemplate(t, "baremetal", ""))
}

func TestGoldenPath_Docker(t *testing.T) {
	if out, err := exec.Command("docker", "info").CombinedOutput(); err != nil {
		t.Skipf("docker daemon not reachable: %v\n%s", err, out)
	}
	// Containers reach the server through this host: Docker Desktop resolves
	// host.docker.internal; on a Linux daemon use the bridge gateway
	// (PIPER_GOLDEN_DOCKER_HOST=172.17.0.1), as a Linux install would.
	host := os.Getenv("PIPER_GOLDEN_DOCKER_HOST")
	if host == "" {
		host = "host.docker.internal"
	}
	port := goldenFreePort(t)
	base := goldenStartServer(t, fmt.Sprintf(`
version: 4
server:
  http_addr: ":%d"
  data_dir: ./piper-data
  workload_token: %q
runtime:
  type: docker
  docker:
    workload_url: "http://%s:%d"
notebook:
  notebooks_root: ./notebooks
  port_range: %d-%d
`, port, goldenRandom(t), host, port, port+1, port+20), port)

	runGoldenPath(t, base, fmt.Sprintf(`apiVersion: piper/v1
kind: Notebook
metadata:
  name: ml-nb
spec:
  driver:
    placement:
      runtime: docker
    docker:
      image: %q
`, goldenImage), goldenTemplate(t, "docker", ""))
}

func TestGoldenPath_K8s(t *testing.T) {
	image := os.Getenv("PIPER_GOLDEN_K8S_IMAGE")
	if image == "" {
		t.Skip("set PIPER_GOLDEN_K8S_IMAGE to a piper image loaded into the cluster")
	}
	if out, err := exec.Command("kubectl", "cluster-info").CombinedOutput(); err != nil {
		t.Skipf("kubectl cannot reach a cluster: %v\n%s", err, out)
	}
	ns := fmt.Sprintf("piper-golden-%d", time.Now().Unix())
	base := goldenDeployK8s(t, ns, image)

	runGoldenPath(t, base, fmt.Sprintf(`apiVersion: piper/v1
kind: Notebook
metadata:
  name: ml-nb
spec:
  volume:
    size: 1Gi
  driver:
    placement:
      runtime: k8s
    k8s:
      image: %q
      namespace: %q
`, goldenImage, ns), goldenTemplate(t, "k8s", ns))
}

// runGoldenPath is the user's flow, identical on every runtime.
func runGoldenPath(t *testing.T, base, notebookYAML, templateYAML string) {
	t.Helper()
	c := newGoldenClient(t, base)
	api := "/api/projects/" + goldenProject

	// 1. Launch the notebook and wait until Jupyter answers through the proxy.
	c.expect(t, http.MethodPost, api+"/notebooks", map[string]string{"yaml": notebookYAML}, http.StatusCreated, nil)
	t.Cleanup(func() { c.do(t, http.MethodDelete, api+"/notebooks/"+goldenNotebook, nil, nil) })
	var nb struct {
		Status   string `json:"status"`
		Reason   string `json:"reason"`
		VolumeID string `json:"volume_id"`
	}
	proxy := "/projects/" + goldenProject + "/notebooks/" + goldenNotebook + "/proxy/"
	goldenWait(t, 10*time.Minute, "notebook running", func() (bool, string) {
		c.expect(t, http.MethodGet, api+"/notebooks/"+goldenNotebook, nil, http.StatusOK, &nb)
		if nb.Status == "failed" {
			t.Fatalf("notebook failed: %s", nb.Reason)
		}
		return nb.Status == "running" && c.do(t, http.MethodGet, proxy+"api/status", nil, nil) == http.StatusOK, nb.Status
	})

	// 2. Write the ML notebook and its requirements, as the user does in JupyterLab.
	c.expect(t, http.MethodPut, proxy+"api/contents/train_iris.ipynb",
		map[string]any{"type": "notebook", "format": "json", "content": goldenNotebookCode}, http.StatusCreated, nil)
	c.expect(t, http.MethodPut, proxy+"api/contents/requirements.txt",
		map[string]any{"type": "file", "format": "text", "content": "scikit-learn\n"}, http.StatusCreated, nil)

	// 3. Submit the template the editor produces.
	var tpl struct {
		ID   string `json:"id"`
		YAML string `json:"yaml"`
	}
	c.expect(t, http.MethodPost, api+"/pipeline-templates",
		map[string]string{"yaml": templateYAML, "volume_id": nb.VolumeID}, http.StatusCreated, &tpl)
	// Stored text is the submitted text plus metadata.version — what New
	// Version reopens without a Design-loss warning.
	want := strings.Replace(templateYAML, "  name: \"iris\"\n", "  name: \"iris\"\n  version: 1\n", 1)
	if tpl.YAML != want {
		t.Fatalf("stored template yaml:\n%s\nwant:\n%s", tpl.YAML, want)
	}

	// 4. Run it.
	var run struct {
		ID string `json:"id"`
	}
	c.expect(t, http.MethodPost, api+"/pipeline-templates/"+tpl.ID+"/run", map[string]any{}, http.StatusCreated, &run)
	var status struct {
		Run struct {
			Status string `json:"status"`
		} `json:"run"`
	}
	goldenWait(t, 10*time.Minute, "run finished", func() (bool, string) {
		c.expect(t, http.MethodGet, api+"/runs/"+run.ID, nil, http.StatusOK, &status)
		s := status.Run.Status
		return s == "success" || s == "failed" || s == "canceled", s
	})
	if status.Run.Status != "success" {
		var logs bytes.Buffer
		c.do(t, http.MethodGet, api+"/runs/"+run.ID+"/steps/task-1/logs", nil, &logs)
		t.Fatalf("run %s = %s\n%s", run.ID, status.Run.Status, logs.String())
	}

	// 5. The executed notebook is an artifact, and the parameter arrived typed.
	var executed bytes.Buffer
	if code := c.do(t, http.MethodGet, api+"/runs/"+run.ID+"/artifacts/task-1/executed-notebook/executed.ipynb", nil, &executed); code != http.StatusOK {
		t.Fatalf("executed notebook artifact: status %d", code)
	}
	if !strings.Contains(executed.String(), "golden n_estimators 120 int") {
		t.Fatalf("executed notebook does not show an int parameter:\n%s", executed.String())
	}
}

// goldenTemplate reads the editor-generated fixture; for k8s it swaps in the
// test namespace for the placeholder the frontend test uses.
func goldenTemplate(t *testing.T, runtimeType, namespace string) string {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("testdata", "golden", "notebook-template."+runtimeType+".yaml"))
	if err != nil {
		t.Fatal(err)
	}
	yaml := string(data)
	if namespace != "" {
		yaml = strings.ReplaceAll(yaml, `"piper-golden"`, fmt.Sprintf("%q", namespace))
	}
	return yaml
}

// ── server environments ─────────────────────────────────────────────────────

var (
	goldenBuildOnce sync.Once
	goldenBinDir    string
	goldenBuildErr  error
)

// goldenBinary builds the server and, beside it, the Linux agent the docker
// runtime mounts into step containers (piper-<arch>).
func goldenBinary(t *testing.T) string {
	t.Helper()
	goldenBuildOnce.Do(func() {
		goldenBinDir, goldenBuildErr = os.MkdirTemp("", "piper-golden-bin-")
		if goldenBuildErr != nil {
			return
		}
		build := func(env []string, out string) error {
			cmd := exec.Command("go", "build", "-o", filepath.Join(goldenBinDir, out), "./cmd/piper")
			cmd.Env = append(os.Environ(), env...)
			if b, err := cmd.CombinedOutput(); err != nil {
				return fmt.Errorf("go build %s: %v\n%s", out, err, b)
			}
			return nil
		}
		if goldenBuildErr = build(nil, "piper"); goldenBuildErr != nil {
			return
		}
		goldenBuildErr = build([]string{"GOOS=linux", "GOARCH=" + runtime.GOARCH, "CGO_ENABLED=0"}, "piper-"+runtime.GOARCH)
	})
	if goldenBuildErr != nil {
		t.Fatal(goldenBuildErr)
	}
	return filepath.Join(goldenBinDir, "piper")
}

func goldenStartServer(t *testing.T, config string, port int) string {
	t.Helper()
	bin := goldenBinary(t)
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "piper.yaml"), []byte(config), 0o600); err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command(bin, "server", "--config", "piper.yaml")
	cmd.Dir = dir
	logFile, err := os.Create(filepath.Join(dir, "server.log"))
	if err != nil {
		t.Fatal(err)
	}
	cmd.Stdout, cmd.Stderr = logFile, logFile
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
		_ = logFile.Close()
		if t.Failed() {
			if b, err := os.ReadFile(logFile.Name()); err == nil {
				t.Logf("server log:\n%s", b)
			}
		}
	})
	base := fmt.Sprintf("http://127.0.0.1:%d", port)
	goldenWait(t, time.Minute, "server health", func() (bool, string) {
		resp, err := http.Get(base + "/health")
		if err != nil {
			return false, err.Error()
		}
		_ = resp.Body.Close()
		return resp.StatusCode == http.StatusOK, resp.Status
	})
	return base
}

// goldenDeployK8s applies deploy/k8s as shipped, into its own namespace, with
// only the namespace and image changed, and port-forwards to it.
func goldenDeployK8s(t *testing.T, ns, image string) string {
	t.Helper()
	dir := t.TempDir()
	for _, name := range []string{"kustomization.yaml", "namespace.yaml", "rbac.yaml", "server.yaml", "piper.yaml"} {
		data, err := os.ReadFile(filepath.Join("deploy", "k8s", name))
		if err != nil {
			t.Fatal(err)
		}
		s := string(data)
		s = strings.ReplaceAll(s, "namespace: piper\n", "namespace: "+ns+"\n")
		s = strings.ReplaceAll(s, "piper-server.piper.svc", "piper-server."+ns+".svc")
		s = strings.ReplaceAll(s, "ghcr.io/loykin/piper:latest", image)
		if name == "namespace.yaml" {
			s = strings.ReplaceAll(s, "name: piper\n", "name: "+ns+"\n")
		}
		if name == "piper.yaml" {
			s = strings.ReplaceAll(s, "\n    - piper\n", "\n    - "+ns+"\n")
		}
		if err := os.WriteFile(filepath.Join(dir, name), []byte(s), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	goldenKubectl(t, "apply", "-f", filepath.Join(dir, "namespace.yaml"))
	t.Cleanup(func() { _ = exec.Command("kubectl", "delete", "namespace", ns, "--wait=false").Run() })
	goldenKubectl(t, "-n", ns, "create", "secret", "generic", "piper-server-secrets",
		"--from-literal=auth_signing_key="+goldenRandom(t),
		// Same shape deploy/k8s/server-secret.example.yaml documents: base64 of 32 bytes.
		"--from-literal=secret_encryption_key="+goldenKey(t),
		"--from-literal=workload_token="+goldenRandom(t))
	goldenKubectl(t, "apply", "-k", dir)
	if out, err := exec.Command("kubectl", "-n", ns, "rollout", "status", "deploy/piper-server", "--timeout=180s").CombinedOutput(); err != nil {
		state, _ := exec.Command("kubectl", "-n", ns, "get", "pods,pvc", "-o", "wide").CombinedOutput()
		logs, _ := exec.Command("kubectl", "-n", ns, "logs", "deploy/piper-server", "--previous", "--tail=40").CombinedOutput()
		t.Fatalf("piper-server rollout: %v\n%s\n%s\n%s", err, out, state, logs)
	}

	port := goldenFreePort(t)
	pf := exec.Command("kubectl", "-n", ns, "port-forward", "svc/piper-server", fmt.Sprintf("%d:8080", port))
	if err := pf.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = pf.Process.Kill(); _ = pf.Wait() })
	base := fmt.Sprintf("http://127.0.0.1:%d", port)
	goldenWait(t, time.Minute, "port-forward", func() (bool, string) {
		resp, err := http.Get(base + "/health")
		if err != nil {
			return false, err.Error()
		}
		_ = resp.Body.Close()
		return resp.StatusCode == http.StatusOK, resp.Status
	})
	t.Cleanup(func() {
		if t.Failed() {
			out, _ := exec.Command("kubectl", "-n", ns, "logs", "deploy/piper-server", "--tail=80").CombinedOutput()
			t.Logf("piper-server logs:\n%s", out)
		}
	})
	return base
}

// ── client ──────────────────────────────────────────────────────────────────

type goldenClient struct {
	base string
	http *http.Client
}

// newGoldenClient creates the first admin through the bootstrap endpoint, as
// the UI's first-run screen does, and signs in.
func newGoldenClient(t *testing.T, base string) *goldenClient {
	t.Helper()
	jar, _ := cookiejar.New(nil)
	c := &goldenClient{base: base, http: &http.Client{Jar: jar, Timeout: 2 * time.Minute}}
	creds := map[string]string{"username": "golden", "password": "Golden-" + goldenRandom(t)[:16]}
	c.expect(t, http.MethodPost, "/api/auth/bootstrap", creds, http.StatusCreated, nil)
	c.expect(t, http.MethodPost, "/api/auth/login", creds, http.StatusOK, nil)
	return c
}

// do sends a request (JSON body when body != nil) and returns the status,
// copying the response body into out when it is an io.Writer or decoding JSON
// into it otherwise. Jupyter's XSRF cookie, when present, is echoed back.
func (c *goldenClient) do(t *testing.T, method, path string, body, out any) int {
	t.Helper()
	var reader io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			t.Fatal(err)
		}
		reader = bytes.NewReader(b)
	}
	req, err := http.NewRequest(method, c.base+path, reader)
	if err != nil {
		t.Fatal(err)
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if u, err := url.Parse(c.base + path); err == nil {
		for _, ck := range c.http.Jar.Cookies(u) {
			if ck.Name == "_xsrf" {
				req.Header.Set("X-XSRFToken", ck.Value)
			}
		}
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return 0
	}
	defer func() { _ = resp.Body.Close() }()
	switch o := out.(type) {
	case nil:
		_, _ = io.Copy(io.Discard, resp.Body)
	case io.Writer:
		_, _ = io.Copy(o, resp.Body)
	default:
		b, _ := io.ReadAll(resp.Body)
		if resp.StatusCode < 300 {
			if err := json.Unmarshal(b, out); err != nil {
				t.Fatalf("%s %s: decode %q: %v", method, path, b, err)
			}
		} else {
			t.Logf("%s %s: %d %s", method, path, resp.StatusCode, b)
		}
	}
	return resp.StatusCode
}

func (c *goldenClient) expect(t *testing.T, method, path string, body any, want int, out any) {
	t.Helper()
	var buf bytes.Buffer
	sink := out
	if out == nil {
		sink = &buf
	}
	if got := c.do(t, method, path, body, sink); got != want {
		t.Fatalf("%s %s = %d, want %d %s", method, path, got, want, buf.String())
	}
}

// ── helpers ─────────────────────────────────────────────────────────────────

func goldenWait(t *testing.T, timeout time.Duration, what string, done func() (bool, string)) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	last := ""
	for time.Now().Before(deadline) {
		ok, state := done()
		if ok {
			return
		}
		last = state
		time.Sleep(2 * time.Second)
	}
	t.Fatalf("timed out waiting for %s (last: %s)", what, last)
}

func goldenFreePort(t *testing.T) int {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = l.Close() }()
	return l.Addr().(*net.TCPAddr).Port
}

func goldenRandom(t *testing.T) string {
	t.Helper()
	b := make([]byte, 24)
	if _, err := rand.Read(b); err != nil {
		t.Fatal(err)
	}
	return hex.EncodeToString(b)
}

func goldenKey(t *testing.T) string {
	t.Helper()
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		t.Fatal(err)
	}
	return base64.StdEncoding.EncodeToString(b)
}

func goldenKubectl(t *testing.T, args ...string) {
	t.Helper()
	if out, err := exec.Command("kubectl", args...).CombinedOutput(); err != nil {
		t.Fatalf("kubectl %s: %v\n%s", strings.Join(args, " "), err, out)
	}
}

func requireGoldenPython(t *testing.T) {
	t.Helper()
	if out, err := exec.Command("python3", "-m", "venv", "--help").CombinedOutput(); err != nil {
		t.Skipf("python3 with venv is required: %v (%s)", err, out)
	}
}
