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
	"time"

	"github.com/cloudnative-pg/machinery/pkg/log"
	coordinationv1 "k8s.io/api/coordination/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/types"

	apiv1 "github.com/cloudnative-pg/cloudnative-pg/api/v1"
	"github.com/cloudnative-pg/cloudnative-pg/pkg/specs"
)

func (r *ClusterReconciler) isPrimaryLeaseExpired(ctx context.Context, cluster *apiv1.Cluster) (bool, error) {
	var lease coordinationv1.Lease
	leaseName := specs.GetPrimaryLeaseName(cluster.Name)
	reader := r.APIReader
	if reader == nil {
		reader = r.Client
	}
	if err := reader.Get(ctx, types.NamespacedName{Name: leaseName, Namespace: cluster.Namespace}, &lease); err != nil {
		if apierrors.IsNotFound(err) {
			// Clusters upgraded from versions without primary lease fencing may not
			// have a lease until the current primary reconciles. Do not block their
			// existing failover behavior on a missing compatibility object.
			return true, nil
		}
		return false, err
	}

	if lease.Spec.RenewTime == nil || lease.Spec.LeaseDurationSeconds == nil {
		return true, nil
	}

	expiresAt := lease.Spec.RenewTime.Time.Add(time.Duration(*lease.Spec.LeaseDurationSeconds) * time.Second)
	expired := !expiresAt.After(time.Now())
	if !expired {
		log.FromContext(ctx).Info(
			"Waiting for old primary lease to expire before promoting a new primary",
			"lease", leaseName,
			"holder", lease.Spec.HolderIdentity,
			"expiresAt", expiresAt,
		)
	}
	return expired, nil
}
