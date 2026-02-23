// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at http://mozilla.org/MPL/2.0/.

package ssa

import (
	"context"
	"fmt"

	"github.com/fluxcd/pkg/ssa"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"

	"github.com/siderolabs/go-kubernetes/kubernetes/ssa/object"
)

type DestroyOptions struct {
	// DeletePropagationPolicy configures the delete operation propagation policy.
	DeletePropagationPolicy metav1.DeletionPropagation
}

// Destroy removes all objects stored in the inventory from the cluster and then removes the inventory itself.
func (m *Manager) Destroy(ctx context.Context, ops DestroyOptions) error {
	inv, err := m.inventory(ctx)
	if err != nil {
		return err
	}

	if ops.DeletePropagationPolicy == "" {
		ops.DeletePropagationPolicy = metav1.DeletePropagationBackground
	}

	allInvObjects := inv.Get()

	for _, objMeta := range allInvObjects {
		var obj *unstructured.Unstructured

		obj, err = m.resourceManager.Get(ctx, objMeta)
		if err != nil {
			if apierrors.IsNotFound(err) {
				continue
			}

			return fmt.Errorf("failed to get object %s, %w", FormatMetaPath(objMeta), err)
		}

		_, err = m.resourceManager.Delete(ctx, obj, ssa.DeleteOptions{PropagationPolicy: ops.DeletePropagationPolicy})
		if err != nil {
			return err
		}
	}

	// Empty the inventory to reflect cluster state even if the delete operation should fail.
	inv.Update(object.ObjMetadataSet{})

	err = inv.Write(ctx)
	if err != nil {
		return err
	}

	err = inv.Delete(ctx)
	if err != nil {
		return err
	}

	return nil
}
