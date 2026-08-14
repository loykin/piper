package k8sdriver

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"path"
	"strconv"
	"strings"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/kubernetes/scheme"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/remotecommand"

	k8smanifest "github.com/loykin/piper/pkg/manifest/k8s"
	"github.com/loykin/piper/pkg/notebook"
)

// WorkspaceReader implements notebook.WorkspaceReader for K8s direct-runtime
// notebook volumes by exec'ing into the volume's currently-running notebook
// pod (kubectl-exec equivalent, via client-go's remotecommand) — the
// counterpart to the tunnel-based FSListFiles/FSUploadSnapshot the deleted
// remote K8s worker (pkg/notebook/worker/driver/k8s/volume_browser.go) used
// to provide. A K8s notebook volume has no meaning outside of a pod mounting
// it (see NotebookVolume.WorkerID's doc comment), so this requires the
// notebook to be running; there is no fallback for a stopped notebook.
type WorkspaceReader struct {
	Client kubernetes.Interface
	// RestConfig is required to build the SPDY executor remotecommand uses;
	// kubernetes.Interface alone cannot construct pod exec streams.
	RestConfig *rest.Config
	Namespaces []string
}

var _ notebook.WorkspaceReader = (*WorkspaceReader)(nil)

func (w *WorkspaceReader) Stat(ctx context.Context, vol *notebook.NotebookVolume, p string) (bool, int64, error) {
	ns, pod, err := w.resolvePod(ctx, vol)
	if err != nil {
		return false, 0, err
	}
	full := containerPath(vol, p)
	script := `p="$1"; if [ -d "$p" ]; then echo DIR; elif [ -f "$p" ]; then wc -c < "$p"; else exit 2; fi`
	var stdout, stderr bytes.Buffer
	if err := w.exec(ctx, ns, pod, []string{"sh", "-c", script, "sh", full}, nil, &stdout, &stderr); err != nil {
		return false, 0, fmt.Errorf("k8sdriver: stat %s: %s: %w", p, strings.TrimSpace(stderr.String()), err)
	}
	out := strings.TrimSpace(stdout.String())
	if out == "DIR" {
		return true, 0, nil
	}
	size, convErr := strconv.ParseInt(out, 10, 64)
	if convErr != nil {
		return false, 0, fmt.Errorf("k8sdriver: stat %s: unexpected output %q", p, out)
	}
	return false, size, nil
}

func (w *WorkspaceReader) Open(ctx context.Context, vol *notebook.NotebookVolume, p string) (io.ReadCloser, error) {
	ns, pod, err := w.resolvePod(ctx, vol)
	if err != nil {
		return nil, err
	}
	full := containerPath(vol, p)
	pr, pw := io.Pipe()
	go func() {
		var stderr bytes.Buffer
		if err := w.exec(ctx, ns, pod, []string{"cat", full}, nil, pw, &stderr); err != nil {
			_ = pw.CloseWithError(fmt.Errorf("k8sdriver: open %s: %s: %w", p, strings.TrimSpace(stderr.String()), err))
			return
		}
		_ = pw.Close()
	}()
	return pr, nil
}

func (w *WorkspaceReader) ListFiles(ctx context.Context, vol *notebook.NotebookVolume, p string) ([]notebook.WorkspaceFile, error) {
	ns, pod, err := w.resolvePod(ctx, vol)
	if err != nil {
		return nil, err
	}
	full := containerPath(vol, p)
	script := `p="$1"; find "$p" -type f -exec wc -c {} \;`
	var stdout, stderr bytes.Buffer
	if err := w.exec(ctx, ns, pod, []string{"sh", "-c", script, "sh", full}, nil, &stdout, &stderr); err != nil {
		return nil, fmt.Errorf("k8sdriver: list %s: %s: %w", p, strings.TrimSpace(stderr.String()), err)
	}
	var files []notebook.WorkspaceFile
	for _, line := range strings.Split(stdout.String(), "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		fields := strings.SplitN(line, " ", 2)
		if len(fields) != 2 {
			continue
		}
		size, err := strconv.ParseInt(strings.TrimSpace(fields[0]), 10, 64)
		if err != nil {
			continue
		}
		foundPath := strings.TrimSpace(fields[1])
		rel := strings.TrimPrefix(foundPath, full+"/")
		files = append(files, notebook.WorkspaceFile{Rel: rel, Size: size})
	}
	return files, nil
}

// resolvePod finds the namespace and pod name of the currently-running
// notebook that has vol mounted, via the piper.io/volume-id annotation Start
// stamps on the notebook's StatefulSet (see driver.go's Start).
func (w *WorkspaceReader) resolvePod(ctx context.Context, vol *notebook.NotebookVolume) (namespace, pod string, err error) {
	selector := k8smanifest.ManagedSelector() + "," + k8smanifest.LabelWorkloadKind + "=notebook"
	for _, ns := range w.Namespaces {
		items, err := w.Client.AppsV1().StatefulSets(ns).List(ctx, metav1.ListOptions{LabelSelector: selector})
		if err != nil {
			return "", "", fmt.Errorf("k8sdriver: list statefulsets in %s: %w", ns, err)
		}
		for i := range items.Items {
			sts := &items.Items[i]
			if sts.Annotations[k8smanifest.AnnotationVolumeID] != vol.ID {
				continue
			}
			if sts.Status.ReadyReplicas < 1 {
				return "", "", fmt.Errorf("k8sdriver: notebook using volume %q is not running", vol.ID)
			}
			return ns, sts.Name + "-0", nil
		}
	}
	return "", "", fmt.Errorf("k8sdriver: no running notebook found for volume %q", vol.ID)
}

func (w *WorkspaceReader) exec(ctx context.Context, ns, pod string, command []string, stdin io.Reader, stdout, stderr io.Writer) error {
	req := w.Client.CoreV1().RESTClient().Post().
		Resource("pods").
		Namespace(ns).
		Name(pod).
		SubResource("exec").
		VersionedParams(&corev1.PodExecOptions{
			Container: "notebook",
			Command:   command,
			Stdin:     stdin != nil,
			Stdout:    true,
			Stderr:    true,
		}, scheme.ParameterCodec)
	executor, err := remotecommand.NewSPDYExecutor(w.RestConfig, "POST", req.URL())
	if err != nil {
		return err
	}
	return executor.StreamWithContext(ctx, remotecommand.StreamOptions{Stdin: stdin, Stdout: stdout, Stderr: stderr})
}

// containerPath resolves p (relative to the notebook's workspace root) to
// an absolute path inside the notebook container, using the POSIX join
// rules of the container's filesystem regardless of Piper's own host OS.
func containerPath(vol *notebook.NotebookVolume, p string) string {
	base := vol.WorkDir
	if base == "" {
		base = notebook.ContainerWorkDir
	}
	return path.Join(base, p)
}
