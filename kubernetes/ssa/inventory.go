// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at http://mozilla.org/MPL/2.0/.

package ssa

import (
	"context"

	"k8s.io/client-go/kubernetes"

	"github.com/siderolabs/go-kubernetes/kubernetes/ssa/inventory"
)

// InventoryAnnotationKey is the annotation key used to store the inventory ID in the applied objects.
const InventoryAnnotationKey = inventory.AnnotationKey

// Inventory holds the previously applied manifests state.
type Inventory = inventory.Inventory

// GetInventory returns the inventory object for the given inventory ID creating it if it doesn't exist.
//
// Use [inventory.Get] directly to avoid linking in the server-side apply machinery.
func GetInventory(ctx context.Context, k8sClient *kubernetes.Clientset, inventoryNamespace, inventoryName string) (Inventory, error) {
	return inventory.Get(ctx, k8sClient, inventoryNamespace, inventoryName)
}
