package notebookworker

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	coordinationv1 "k8s.io/api/coordination/v1"
	k8serrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
)

const (
	leaseDuration     = 15 * time.Second
	leaseRenewGap     = 5 * time.Second
	leaseAcquireRetry = 500 * time.Millisecond
)

// volumeLock acquires a Kubernetes Lease for the given volume ID, runs fn
// while renewing the lease, then releases it. If the lease is lost during fn
// (renew fails), fn's context is cancelled and an error is returned.
func volumeLock(ctx context.Context, client kubernetes.Interface, ns, workerID, volumeID string, fn func(ctx context.Context) error) error {
	leaseName := volumeLeaseName(volumeID)
	// Unique per acquisition so a late renew/release from a previous lock
	// cannot interfere with a new lock held by the same worker.
	holderID := workerID + "/" + uuid.NewString()

	if err := acquireLease(ctx, client, ns, leaseName, holderID); err != nil {
		return fmt.Errorf("acquire volume lease %s: %w", leaseName, err)
	}

	fnErr := runWithRenewal(ctx, client, ns, leaseName, holderID, fn)

	releaseLease(client, ns, leaseName, holderID)
	return fnErr
}

func acquireLease(ctx context.Context, client kubernetes.Interface, ns, leaseName, holderID string) error {
	for {
		if err := ctx.Err(); err != nil {
			return err
		}

		now := metav1.NewMicroTime(time.Now())
		durationSecs := int32(leaseDuration.Seconds())

		lease := &coordinationv1.Lease{
			ObjectMeta: metav1.ObjectMeta{Name: leaseName, Namespace: ns},
			Spec: coordinationv1.LeaseSpec{
				HolderIdentity:       &holderID,
				LeaseDurationSeconds: &durationSecs,
				AcquireTime:          &now,
				RenewTime:            &now,
			},
		}

		_, err := client.CoordinationV1().Leases(ns).Create(ctx, lease, metav1.CreateOptions{})
		if err == nil {
			return nil
		}
		if !k8serrors.IsAlreadyExists(err) {
			return err
		}

		existing, getErr := client.CoordinationV1().Leases(ns).Get(ctx, leaseName, metav1.GetOptions{})
		if getErr != nil {
			if k8serrors.IsNotFound(getErr) {
				continue
			}
			return getErr
		}

		if isLeaseExpired(existing) {
			existing.Spec.HolderIdentity = &holderID
			existing.Spec.AcquireTime = &now
			existing.Spec.RenewTime = &now
			if _, updateErr := client.CoordinationV1().Leases(ns).Update(ctx, existing, metav1.UpdateOptions{}); updateErr == nil {
				return nil
			}
		}

		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(leaseAcquireRetry):
		}
	}
}

func isLeaseExpired(lease *coordinationv1.Lease) bool {
	if lease.Spec.RenewTime == nil || lease.Spec.LeaseDurationSeconds == nil {
		return true
	}
	expiry := lease.Spec.RenewTime.Add(time.Duration(*lease.Spec.LeaseDurationSeconds) * time.Second)
	return time.Now().After(expiry)
}

func runWithRenewal(ctx context.Context, client kubernetes.Interface, ns, leaseName, holderID string, fn func(ctx context.Context) error) error {
	// Give fn a cancellable child context so we can abort it if the lease is lost.
	fnCtx, fnCancel := context.WithCancel(ctx)
	defer fnCancel()

	done := make(chan error, 1)
	go func() { done <- fn(fnCtx) }()

	for {
		select {
		case err := <-done:
			return err
		case <-time.After(leaseRenewGap):
			if !renewLease(client, ns, leaseName, holderID) {
				// Lease lost — abort fn and surface error.
				fnCancel()
				<-done
				return fmt.Errorf("volume lease lost during operation")
			}
		case <-ctx.Done():
			fnErr := <-done
			if isTransitioningError(fnErr) {
				return fnErr
			}
			return ctx.Err()
		}
	}
}

// renewLease returns true if the lease was successfully renewed.
func renewLease(client kubernetes.Interface, ns, leaseName, holderID string) bool {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	lease, err := client.CoordinationV1().Leases(ns).Get(ctx, leaseName, metav1.GetOptions{})
	if err != nil {
		return false
	}
	if lease.Spec.HolderIdentity == nil || *lease.Spec.HolderIdentity != holderID {
		return false // lease taken by another holder
	}
	now := metav1.NewMicroTime(time.Now())
	lease.Spec.RenewTime = &now
	_, err = client.CoordinationV1().Leases(ns).Update(ctx, lease, metav1.UpdateOptions{})
	return err == nil
}

// releaseLease deletes the lease only if we still own it (holder + resourceVersion check).
func releaseLease(client kubernetes.Interface, ns, leaseName, holderID string) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	lease, err := client.CoordinationV1().Leases(ns).Get(ctx, leaseName, metav1.GetOptions{})
	if err != nil {
		return
	}
	if lease.Spec.HolderIdentity == nil || *lease.Spec.HolderIdentity != holderID {
		return
	}
	_ = client.CoordinationV1().Leases(ns).Delete(ctx, leaseName, metav1.DeleteOptions{
		Preconditions: &metav1.Preconditions{ResourceVersion: &lease.ResourceVersion},
	})
}

func volumeLeaseName(volumeID string) string {
	clean := strings.ReplaceAll(volumeID, "-", "")
	if len(clean) > 12 {
		clean = clean[:12]
	}
	return "piper-nb-volume-" + clean
}
