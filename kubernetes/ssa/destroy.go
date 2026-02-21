// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at http://mozilla.org/MPL/2.0/.

package ssa

import (
	"context"

	"github.com/fluxcd/pkg/ssa"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/cli-utils/pkg/object"
)

type DestroyOptions struct {
	// DeletePropagationPolicy configures the delete operation propagation policy.
	DeletePropagationPolicy metav1.DeletionPropagation
}

// Destroy removes all objects stored in the inventory from the cluster and then removes the inventory itself.
func (m *Manager) Destroy(ctx context.Context, ops DestroyOptions) error {
	if ops.DeletePropagationPolicy == "" {
		ops.DeletePropagationPolicy = metav1.DeletePropagationBackground
	}

	allObjects, err := m.inventory.GetPruneObjs(ctx, nil)
	if err != nil {
		return err
	}

	for _, obj := range allObjects {
		_, err = m.resourceManager.Delete(ctx, obj, ssa.DeleteOptions{PropagationPolicy: ops.DeletePropagationPolicy})
		if err != nil {
			return err
		}
	}

	// Empty the inventory to reflect cluster state even if the delete operation should fail.
	err = m.inventory.Write(ctx, object.ObjMetadataSet{})
	if err != nil {
		return err
	}

	err = m.inventory.Delete(ctx)
	if err != nil && !apierrors.IsNotFound(err) {
		return err
	}

	return nil
}
