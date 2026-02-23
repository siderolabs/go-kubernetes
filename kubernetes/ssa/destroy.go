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

// Destroy removes all objects stored in the inventory from the cluster and then removes the inventory itself.
func (m *Manager) Destroy(ctx context.Context) error {
	inv, err := m.inventory(ctx)
	if err != nil {
		return err
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

		_, err = m.resourceManager.Delete(ctx, obj, ssa.DeleteOptions{PropagationPolicy: metav1.DeletePropagationBackground})
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
