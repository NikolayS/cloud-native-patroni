/*
Copyright © contributors to CloudNativePG, established as
CloudNativePG a Series of LF Projects, LLC.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.

SPDX-License-Identifier: Apache-2.0
*/

package controller

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/cloudnative-pg/machinery/pkg/log"
	coordinationv1 "k8s.io/api/coordination/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/util/retry"

	apiv1 "github.com/cloudnative-pg/cloudnative-pg/api/v1"
	"github.com/cloudnative-pg/cloudnative-pg/pkg/specs"
)

const (
	primaryLeaseDurationSeconds = int32(15)
	primaryLeaseRenewInterval   = 5 * time.Second
	primaryLeaseRequestTimeout  = 2 * time.Second
)

type primaryLeaseGuard struct {
	cancel context.CancelFunc
}

var primaryLeaseGuardMu sync.Mutex

// reconcilePrimaryLeaseGuard starts the in-pod lease renewer only on the
// current primary and stops it everywhere else. The renewer lives inside the
// instance manager so an isolated primary can fence itself when it loses API
// server reachability.
func (r *InstanceReconciler) reconcilePrimaryLeaseGuard(ctx context.Context, cluster *apiv1.Cluster) {
	primaryLeaseGuardMu.Lock()
	defer primaryLeaseGuardMu.Unlock()

	shouldRun := r.instance.GetPodName() == cluster.Status.CurrentPrimary &&
		cluster.Status.CurrentPrimary == cluster.Status.TargetPrimary

	if !shouldRun {
		r.stopPrimaryLeaseGuardLocked()
		return
	}

	if r.primaryLeaseGuard != nil {
		return
	}

	guardCtx, cancel := context.WithCancel(context.Background())
	r.primaryLeaseGuard = &primaryLeaseGuard{cancel: cancel}
	go r.runPrimaryLeaseGuard(guardCtx, cluster.DeepCopy())
}

func (r *InstanceReconciler) stopPrimaryLeaseGuardLocked() {
	if r.primaryLeaseGuard == nil {
		return
	}

	r.primaryLeaseGuard.cancel()
	r.primaryLeaseGuard = nil
}

func (r *InstanceReconciler) runPrimaryLeaseGuard(ctx context.Context, cluster *apiv1.Cluster) {
	contextLogger := log.FromContext(ctx).WithValues(
		"cluster", cluster.Name,
		"namespace", cluster.Namespace,
		"instance", r.instance.GetPodName(),
		"lease", specs.GetPrimaryLeaseName(cluster.Name),
	)

	ticker := time.NewTicker(primaryLeaseRenewInterval)
	defer ticker.Stop()

	if err := r.renewPrimaryLease(ctx, cluster); err != nil {
		contextLogger.Error(err, "Unable to acquire primary lease, fencing local primary")
		r.fenceLocalPrimary(ctx, cluster, err)
		return
	}

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if err := r.renewPrimaryLease(ctx, cluster); err != nil {
				contextLogger.Error(err, "Unable to renew primary lease, fencing local primary")
				r.fenceLocalPrimary(ctx, cluster, err)
				return
			}
		}
	}
}

func (r *InstanceReconciler) renewPrimaryLease(ctx context.Context, cluster *apiv1.Cluster) error {
	leaseName := specs.GetPrimaryLeaseName(cluster.Name)
	renewCtx, cancel := context.WithTimeout(ctx, primaryLeaseRequestTimeout)
	defer cancel()

	now := metav1.NowMicro()
	lease := coordinationv1.Lease{
		ObjectMeta: metav1.ObjectMeta{
			Name:      leaseName,
			Namespace: cluster.Namespace,
			Labels: map[string]string{
				"cnpg.io/cluster": cluster.Name,
			},
		},
		Spec: coordinationv1.LeaseSpec{
			HolderIdentity:       stringPtr(r.instance.GetPodName()),
			LeaseDurationSeconds: int32Ptr(primaryLeaseDurationSeconds),
			AcquireTime:          &now,
			RenewTime:            &now,
		},
	}

	if err := r.client.Create(renewCtx, &lease); err == nil {
		return nil
	} else if !apierrors.IsAlreadyExists(err) {
		return err
	}

	return retry.RetryOnConflict(retry.DefaultBackoff, func() error {
		var current coordinationv1.Lease
		if err := r.client.Get(renewCtx, types.NamespacedName{Name: leaseName, Namespace: cluster.Namespace}, &current); err != nil {
			return err
		}

		if current.Spec.HolderIdentity != nil && *current.Spec.HolderIdentity != r.instance.GetPodName() &&
			!isLeaseExpired(current, time.Now()) {
			return fmt.Errorf("primary lease is still held by %q", *current.Spec.HolderIdentity)
		}

		updated := current.DeepCopy()
		updated.Spec.HolderIdentity = stringPtr(r.instance.GetPodName())
		updated.Spec.LeaseDurationSeconds = int32Ptr(primaryLeaseDurationSeconds)
		if updated.Spec.AcquireTime == nil || current.Spec.HolderIdentity == nil ||
			*current.Spec.HolderIdentity != r.instance.GetPodName() {
			updated.Spec.AcquireTime = &now
		}
		updated.Spec.RenewTime = &now
		return r.client.Update(renewCtx, updated)
	})
}

func isLeaseExpired(lease coordinationv1.Lease, now time.Time) bool {
	if lease.Spec.RenewTime == nil || lease.Spec.LeaseDurationSeconds == nil {
		return true
	}

	expiresAt := lease.Spec.RenewTime.Time.Add(time.Duration(*lease.Spec.LeaseDurationSeconds) * time.Second)
	return !expiresAt.After(now)
}

func (r *InstanceReconciler) fenceLocalPrimary(ctx context.Context, cluster *apiv1.Cluster, cause error) {
	contextLogger := log.FromContext(ctx).WithValues(
		"cluster", cluster.Name,
		"namespace", cluster.Namespace,
		"instance", r.instance.GetPodName(),
	)
	contextLogger.Error(cause, "Primary lease lost, fencing local PostgreSQL primary")

	if err := r.resetFailoverQuorumObject(ctx, cluster); err != nil {
		contextLogger.Error(err, "Unable to reset failover quorum after primary lease loss")
	}

	r.instance.RequestFencingOn()
}

func stringPtr(value string) *string {
	return &value
}

func int32Ptr(value int32) *int32 {
	return &value
}
