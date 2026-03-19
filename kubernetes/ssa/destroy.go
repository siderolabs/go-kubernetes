// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at http://mozilla.org/MPL/2.0/.

package ssa

import (
	"context"
	"fmt"
	"sort"

	"github.com/fluxcd/pkg/ssa"
	"github.com/go-logr/logr"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
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
	ctx = logr.NewContext(ctx, logr.FromContextOrDiscard(ctx))

	inv, err := m.inventory(ctx)
	if err != nil {
		return err
	}

	if ops.DeletePropagationPolicy == "" {
		ops.DeletePropagationPolicy = metav1.DeletePropagationBackground
	}

	allInvObjects := inv.Get()

	objects := make([]*unstructured.Unstructured, 0, len(allInvObjects))

	for _, objMeta := range allInvObjects {
		var obj *unstructured.Unstructured

		obj, err = m.resourceManager.Get(ctx, objMeta)
		if err != nil {
			if apierrors.IsNotFound(err) || meta.IsNoMatchError(err) {
				continue
			}

			return fmt.Errorf("failed to get object %s, %w", FormatMetaPath(objMeta), err)
		}

		objects = append(objects, obj)
	}

	sort.Sort(sort.Reverse(ssa.SortableUnstructureds(objects)))

	for _, obj := range objects {
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
