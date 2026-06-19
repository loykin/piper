package notebookworker

import (
	"context"
	"fmt"

	corev1 "k8s.io/api/core/v1"
	k8serrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/piper/piper/pkg/internal/k8smeta"
	"github.com/piper/piper/pkg/notebook"
)

// listFilesK8s is the K8s implementation of the FSListFiles RPC.
// It resolves the volume state and routes to the appropriate provider.
func (a *Worker) listFilesK8s(ctx context.Context, req notebook.FSListFilesRequest) (*notebook.FSListFilesResponse, error) {
	if req.VolumeID == "" {
		return &notebook.FSListFilesResponse{Files: []string{}, State: notebook.FSAccessReady}, nil
	}

	ns := a.notebookNamespace()
	snap, err := a.resolveVolumeSnapshot(ctx, ns, req.VolumeID)
	if err != nil {
		return nil, err
	}

	switch {
	case snap.pvcLost:
		return notebook.UnavailableResponse("PVC is in Lost state"), nil
	case !snap.pvcExists:
		return notebook.UnavailableResponse("PVC not found"), nil
	case snap.notebookTerminating:
		return notebook.TransitioningResponse("notebook pod terminating"), nil
	case snap.unknownConsumer:
		return notebook.TransitioningResponse("PVC in use by unknown workload"), nil
	case snap.notebookReadyPod != nil:
		return a.listFilesViaJupyter(ctx, req, snap.notebookReadyPod)
	case snap.notebookNonReadyPod != nil:
		return notebook.TransitioningResponse("notebook pod not ready"), nil
	case snap.viewerTerminating:
		return notebook.TransitioningResponse("viewer pod terminating"), nil
	case snap.viewerReadyPod != nil:
		return a.listFilesViaViewer(ctx, req, snap.viewerReadyPod)
	case snap.viewerNonReadyPod != nil:
		return notebook.TransitioningResponse("viewer pod pending"), nil
	case snap.stsDesiredReplicas > 0:
		return notebook.TransitioningResponse("notebook starting"), nil
	case snap.pvcBound:
		return a.ensureAndListViaViewer(ctx, req)
	case snap.pvcPending:
		return notebook.TransitioningResponse("PVC pending"), nil
	}

	return notebook.UnavailableResponse(fmt.Sprintf("PVC phase: %s", snap.pvcPhase)), nil
}

// volumeSnapshot is the observed state of a volume and its related resources.
type volumeSnapshot struct {
	pvcExists           bool
	pvcBound            bool
	pvcPending          bool
	pvcLost             bool
	pvcPhase            corev1.PersistentVolumeClaimPhase
	notebookReadyPod    *corev1.Pod
	notebookNonReadyPod *corev1.Pod
	notebookTerminating bool
	viewerReadyPod      *corev1.Pod
	viewerNonReadyPod   *corev1.Pod
	viewerTerminating   bool
	unknownConsumer     bool // terminating or running unknown pod using PVC
	stsDesiredReplicas  int32
}

// resolveVolumeSnapshot queries all relevant K8s resources for a volume.
func (a *Worker) resolveVolumeSnapshot(ctx context.Context, ns, volumeID string) (*volumeSnapshot, error) {
	snap := &volumeSnapshot{}
	pvcName := notebookPVCName(volumeID)

	pvc, err := a.cfg.Client.CoreV1().PersistentVolumeClaims(ns).Get(ctx, pvcName, metav1.GetOptions{})
	if k8serrors.IsNotFound(err) {
		return snap, nil
	}
	if err != nil {
		return nil, err
	}
	snap.pvcExists = true
	snap.pvcPhase = pvc.Status.Phase
	snap.pvcBound = pvc.Status.Phase == corev1.ClaimBound
	snap.pvcPending = pvc.Status.Phase == corev1.ClaimPending
	snap.pvcLost = pvc.Status.Phase == corev1.ClaimLost

	pods, err := a.cfg.Client.CoreV1().Pods(ns).List(ctx, metav1.ListOptions{})
	if err != nil {
		return nil, err
	}
	for i := range pods.Items {
		pod := &pods.Items[i]
		if !podUsesPVC(pod, pvcName) {
			continue
		}
		kind := pod.Labels[k8smeta.LabelWorkloadKind]
		isTerminating := pod.DeletionTimestamp != nil

		switch kind {
		case "notebook":
			if isTerminating {
				snap.notebookTerminating = true
			} else if isPodReady(pod) {
				snap.notebookReadyPod = pod
			} else {
				snap.notebookNonReadyPod = pod
			}
		case viewerLabelKind:
			if isTerminating {
				snap.viewerTerminating = true
			} else if isPodReady(pod) {
				snap.viewerReadyPod = pod
			} else {
				snap.viewerNonReadyPod = pod
			}
		default:
			// Any unknown pod using the PVC (terminating or running) risks Multi-Attach.
			snap.unknownConsumer = true
		}
	}

	stsList, err := a.cfg.Client.AppsV1().StatefulSets(ns).List(ctx, metav1.ListOptions{
		LabelSelector: k8smeta.ManagedSelector() + "," + k8smeta.LabelWorkloadKind + "=notebook",
	})
	if err == nil {
		for i := range stsList.Items {
			sts := &stsList.Items[i]
			if sts.Annotations[k8smeta.AnnotationVolumeID] == volumeID {
				if sts.Spec.Replicas != nil {
					snap.stsDesiredReplicas = *sts.Spec.Replicas
				}
				break
			}
		}
	}

	return snap, nil
}

func (a *Worker) listFilesViaJupyter(ctx context.Context, req notebook.FSListFilesRequest, _ *corev1.Pod) (*notebook.FSListFilesResponse, error) {
	if req.Notebook == "" || req.Token == "" {
		return notebook.TransitioningResponse("notebook token not available"), nil
	}

	ns := a.notebookNamespace()
	client := newJupyterContentsClient()
	svcHost := jupyterServiceHost(req.ProjectID, req.Notebook, ns)
	baseURL := jupyterBaseURL(req.Notebook)

	maxFiles := req.MaxFiles
	if maxFiles <= 0 {
		maxFiles = 500
	}

	files, truncated, err := client.ListFiles(ctx, svcHost, baseURL, req.Token, req.Path, req.Ext, maxFiles)
	if err != nil {
		return notebook.TransitioningResponse("jupyter API error: " + err.Error()), nil
	}
	if files == nil {
		files = []string{}
	}
	return &notebook.FSListFilesResponse{
		Files:     files,
		State:     notebook.FSAccessReady,
		Truncated: truncated,
	}, nil
}

func (a *Worker) listFilesViaViewer(ctx context.Context, req notebook.FSListFilesRequest, pod *corev1.Pod) (*notebook.FSListFilesResponse, error) {
	mgr := newVolumeBrowserManager(a.cfg.Client, a.notebookNamespace(), a.cfg.WorkerID, a.cfg.Image)
	ep := mgr.endpointFromPod(pod)
	_ = mgr.touchAnnotation(ctx, pod)
	return mgr.ListFiles(ctx, ep, req)
}

func (a *Worker) ensureAndListViaViewer(ctx context.Context, req notebook.FSListFilesRequest) (*notebook.FSListFilesResponse, error) {
	ns := a.notebookNamespace()
	mgr := newVolumeBrowserManager(a.cfg.Client, ns, a.cfg.WorkerID, a.cfg.Image)

	var ep *browserEndpoint
	lockErr := volumeLock(ctx, a.cfg.Client, ns, a.cfg.WorkerID, req.VolumeID, func(ctx context.Context) error {
		// Re-resolve full state inside the lock to catch races with startNotebook.
		snap, snapErr := a.resolveVolumeSnapshot(ctx, ns, req.VolumeID)
		if snapErr != nil {
			return snapErr
		}
		switch {
		case snap.notebookTerminating, snap.unknownConsumer,
			snap.notebookNonReadyPod != nil, snap.notebookReadyPod != nil:
			return &transitioningError{"notebook is active or transitioning"}
		case snap.stsDesiredReplicas > 0:
			return &transitioningError{"notebook starting"}
		case snap.viewerReadyPod != nil:
			ep = mgr.endpointFromPod(snap.viewerReadyPod)
			return nil
		case snap.viewerTerminating:
			return &transitioningError{"viewer pod terminating"}
		case snap.viewerNonReadyPod != nil:
			return &transitioningError{"viewer pod pending"}
		}

		var ensureErr error
		ep, ensureErr = mgr.Ensure(ctx, req.VolumeID)
		return ensureErr
	})

	if lockErr != nil {
		if isTransitioningError(lockErr) {
			return notebook.TransitioningResponse(lockErr.Error()), nil
		}
		return nil, lockErr
	}

	return mgr.ListFiles(ctx, ep, req)
}

// podUsesPVC returns true if the pod has a volume referencing the given PVC.
func podUsesPVC(pod *corev1.Pod, pvcName string) bool {
	for _, vol := range pod.Spec.Volumes {
		if vol.PersistentVolumeClaim != nil && vol.PersistentVolumeClaim.ClaimName == pvcName {
			return true
		}
	}
	return false
}
