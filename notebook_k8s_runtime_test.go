package piper

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes/fake"

	"github.com/loykin/piper/pkg/notebook"
	"github.com/loykin/piper/pkg/project"
)

// TestNotebookK8sDirectRuntimeLifecycle proves fed.md §13.6's Notebook K8s
// direct-runtime prerequisite: a notebook created through the real HTTP API
// on a Piper configured with Runtime.Type=k8s must be launched by
// pkg/notebook/dispatch/localdriver/k8s directly (no remote worker/tunnel
// involved) as a StatefulSet+Service, reach status=running once the
// StatefulSet becomes ready (observed by the background Observe loop, not
// trusted from Start's synchronous return), expose a plain cluster-DNS
// endpoint (no tunnel:// scheme), and be cleanly deleted.
func TestNotebookK8sDirectRuntimeLifecycle(t *testing.T) {
	const projectID = "proj-notebook-k8s"
	const namespace = "nb-ns"

	client := fake.NewSimpleClientset()
	p := newTestPiper(t, Config{
		OutputDir: t.TempDir(),
		Storage:   StorageConfig{Disabled: true},
		Runtime: RuntimeConfig{
			Type: RuntimeK8s,
			K8s:  K8sRuntimeConfig{Client: client, Namespaces: []string{namespace}},
		},
	})
	if err := p.repos.Project.Create(context.Background(), &project.Project{ID: projectID, Name: projectID}); err != nil {
		t.Fatalf("create project: %v", err)
	}

	router := p.Handler(nil)

	yaml := `apiVersion: piper/v1
kind: Notebook
metadata:
  name: k8s-nb
spec:
  volume:
    size: 1Gi
  driver:
    placement:
      runtime: k8s
    k8s:
      image: jupyter/base:latest
      namespace: ` + namespace + `
`
	body, _ := json.Marshal(map[string]string{"yaml": yaml})
	req := httptest.NewRequest(http.MethodPost, "/api/projects/"+projectID+"/notebooks", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	if rec.Code != http.StatusCreated {
		t.Fatalf("create notebook status = %d, want 201: %s", rec.Code, rec.Body.String())
	}

	stsName := "" // resolved once the StatefulSet actually exists
	deadline := time.Now().Add(20 * time.Second)
	for {
		sets, err := client.AppsV1().StatefulSets(namespace).List(context.Background(), metav1.ListOptions{})
		if err != nil {
			t.Fatal(err)
		}
		if len(sets.Items) == 1 {
			stsName = sets.Items[0].Name
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("direct-runtime did not create a StatefulSet for the notebook")
		}
		time.Sleep(20 * time.Millisecond)
	}

	// Simulate the pod becoming ready so the background Observe loop reports
	// status=running via ReportStatus -> nbMgr.UpdateStatus.
	deadline = time.Now().Add(5 * time.Second)
	for {
		sts, err := client.AppsV1().StatefulSets(namespace).Get(context.Background(), stsName, metav1.GetOptions{})
		if err != nil {
			t.Fatal(err)
		}
		sts.Status.ReadyReplicas = 1
		if _, err := client.AppsV1().StatefulSets(namespace).UpdateStatus(context.Background(), sts, metav1.UpdateOptions{}); err == nil {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("could not mark statefulset ready")
		}
		time.Sleep(20 * time.Millisecond)
	}

	deadline = time.Now().Add(20 * time.Second)
	var nb notebook.NotebookServer
	for {
		getReq := httptest.NewRequest(http.MethodGet, "/api/projects/"+projectID+"/notebooks/k8s-nb", nil)
		getRec := httptest.NewRecorder()
		router.ServeHTTP(getRec, getReq)
		if getRec.Code != http.StatusOK {
			t.Fatalf("get notebook status = %d: %s", getRec.Code, getRec.Body.String())
		}
		if err := json.NewDecoder(getRec.Body).Decode(&nb); err != nil {
			t.Fatal(err)
		}
		if nb.Status == notebook.StatusRunning {
			break
		}
		if nb.Status == notebook.StatusFailed {
			t.Fatalf("notebook reached status=failed")
		}
		if time.Now().After(deadline) {
			t.Fatalf("notebook did not reach status=running in time, last status=%q", nb.Status)
		}
		time.Sleep(20 * time.Millisecond)
	}

	if nb.RuntimeID != notebookK8sLocalWorkerID {
		t.Fatalf("RuntimeID = %q, want %q (direct-runtime local identity)", nb.RuntimeID, notebookK8sLocalWorkerID)
	}
	if !strings.HasPrefix(nb.Endpoint, "http://") || !strings.HasSuffix(nb.Endpoint, ".svc.cluster.local:8888") {
		t.Fatalf("Endpoint = %q, want a plain cluster-DNS endpoint (no tunnel:// scheme in direct mode)", nb.Endpoint)
	}

	delReq := httptest.NewRequest(http.MethodDelete, "/api/projects/"+projectID+"/notebooks/k8s-nb", nil)
	delRec := httptest.NewRecorder()
	router.ServeHTTP(delRec, delReq)
	if delRec.Code != http.StatusNoContent {
		t.Fatalf("delete notebook status = %d, want 204: %s", delRec.Code, delRec.Body.String())
	}

	getReq := httptest.NewRequest(http.MethodGet, "/api/projects/"+projectID+"/notebooks/k8s-nb", nil)
	getRec := httptest.NewRecorder()
	router.ServeHTTP(getRec, getReq)
	if getRec.Code != http.StatusNotFound {
		t.Fatalf("get notebook after delete status = %d, want 404: %s", getRec.Code, getRec.Body.String())
	}
}
