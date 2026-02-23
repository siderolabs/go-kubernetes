// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at http://mozilla.org/MPL/2.0/.

package ssa

import (
	"context"

	"k8s.io/client-go/kubernetes"

	"github.com/siderolabs/go-kubernetes/kubernetes/ssa/internal/inventory/configmap"
	"github.com/siderolabs/go-kubernetes/kubernetes/ssa/object"
)

// InventoryAnnotationKey is the annotation key used to store the inventory ID in the applied objects.
const InventoryAnnotationKey = "config.k8s.io/owning-inventory"

// Inventory holds the previously applied manifests state.
type Inventory interface {
	// ID returns the inventory identifier.
	ID() string
	// Get returns the list of object references tracked in the inventory.
	Get() object.ObjMetadataSet
	// Update updates the inventory with the given set of object references.
	Update(object.ObjMetadataSet)
	// Write writes the inventory to the cluster.
	Write(context.Context) error
	// Delete removes the inventory from the cluster.
	Delete(context.Context) error
}

// GetInventory returns the inventory object for the given inventory ID creating it if it doesn't exist.
func GetInventory(ctx context.Context, k8sClient *kubernetes.Clientset, inventoryNamespace, inventoryName string) (Inventory, error) {
	if err := configmap.AssureInventoryNamespace(ctx, k8sClient, inventoryNamespace); err != nil {
		return nil, err
	}

	return configmap.NewInventory(ctx, k8sClient, inventoryNamespace, inventoryName)
}
