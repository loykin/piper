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

	"github.com/loykin/piper/pkg/project"
	"github.com/loykin/piper/pkg/serving"
)

// TestServingK8sDirectRuntimeLifecycle proves fed.md §13.6's Serving K8s
// direct-runtime prerequisite: a ModelService deployed through the real
// HTTP API on a Piper configured with Runtime.Type=k8s must be launched by
// pkg/serving/dispatch/localdriver/k8s directly (no remote worker/tunnel
// involved) as a Deployment+Service, reach status=running once the
// Deployment becomes ready (observed by the background Observe loop, not
// trusted from Deploy's synchronous return — even though
// serving.Manager.Deploy itself is synchronous, unlike notebook.Manager),
// expose a plain cluster-DNS endpoint, and be cleanly deleted. Uses a
// from_uri http(s) model reference so no artifact-fetcher Secret/init
// container is involved, keeping the test focused on the lifecycle path.
func TestServingK8sDirectRuntimeLifecycle(t *testing.T) {
	const projectID = "proj-serving-k8s"
	const namespace = "svc-ns"

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
kind: ModelService
metadata:
  name: k8s-svc
spec:
  model:
    from_uri: "http://models.example.com/model.tar"
  run:
    command: ["serve"]
    port: 8080
  driver:
    placement:
      runtime: k8s
    k8s:
      image: model-server:latest
      namespace: ` + namespace + `
`
	body, _ := json.Marshal(map[string]string{"yaml": yaml})
	req := httptest.NewRequest(http.MethodPost, "/api/projects/"+projectID+"/serving", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("deploy service status = %d, want 200: %s", rec.Code, rec.Body.String())
	}

	depName := ""
	deadline := time.Now().Add(20 * time.Second)
	for {
		deployments, err := client.AppsV1().Deployments(namespace).List(context.Background(), metav1.ListOptions{})
		if err != nil {
			t.Fatal(err)
		}
		if len(deployments.Items) == 1 {
			depName = deployments.Items[0].Name
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("direct-runtime did not create a Deployment for the service")
		}
		time.Sleep(20 * time.Millisecond)
	}

	// Simulate the pod becoming ready so the background Observe loop reports
	// status=running via ReportStatus -> servingMgr.UpdateStatus.
	deadline = time.Now().Add(5 * time.Second)
	for {
		dep, err := client.AppsV1().Deployments(namespace).Get(context.Background(), depName, metav1.GetOptions{})
		if err != nil {
			t.Fatal(err)
		}
		dep.Status.ReadyReplicas = 1
		if _, err := client.AppsV1().Deployments(namespace).UpdateStatus(context.Background(), dep, metav1.UpdateOptions{}); err == nil {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("could not mark deployment ready")
		}
		time.Sleep(20 * time.Millisecond)
	}

	deadline = time.Now().Add(20 * time.Second)
	var svc serving.Service
	for {
		getReq := httptest.NewRequest(http.MethodGet, "/api/projects/"+projectID+"/serving/k8s-svc", nil)
		getRec := httptest.NewRecorder()
		router.ServeHTTP(getRec, getReq)
		if getRec.Code != http.StatusOK {
			t.Fatalf("get service status = %d: %s", getRec.Code, getRec.Body.String())
		}
		if err := json.NewDecoder(getRec.Body).Decode(&svc); err != nil {
			t.Fatal(err)
		}
		if svc.Status == serving.StatusRunning {
			break
		}
		if svc.Status == serving.StatusFailed {
			t.Fatalf("service reached status=failed")
		}
		if time.Now().After(deadline) {
			t.Fatalf("service did not reach status=running in time, last status=%q", svc.Status)
		}
		time.Sleep(20 * time.Millisecond)
	}

	if svc.WorkerID != servingK8sLocalWorkerID {
		t.Fatalf("WorkerID = %q, want %q (direct-runtime local identity, not a remote worker)", svc.WorkerID, servingK8sLocalWorkerID)
	}
	if !strings.HasPrefix(svc.Endpoint, "http://") || !strings.HasSuffix(svc.Endpoint, ".svc.cluster.local:8080") {
		t.Fatalf("Endpoint = %q, want a plain cluster-DNS endpoint", svc.Endpoint)
	}

	delReq := httptest.NewRequest(http.MethodDelete, "/api/projects/"+projectID+"/serving/k8s-svc", nil)
	delRec := httptest.NewRecorder()
	router.ServeHTTP(delRec, delReq)
	if delRec.Code != http.StatusNoContent {
		t.Fatalf("delete service status = %d, want 204: %s", delRec.Code, delRec.Body.String())
	}

	getReq := httptest.NewRequest(http.MethodGet, "/api/projects/"+projectID+"/serving/k8s-svc", nil)
	getRec := httptest.NewRecorder()
	router.ServeHTTP(getRec, getReq)
	if getRec.Code != http.StatusNotFound {
		t.Fatalf("get service after delete status = %d, want 404: %s", getRec.Code, getRec.Body.String())
	}
}
