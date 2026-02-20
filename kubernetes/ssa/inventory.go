// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at http://mozilla.org/MPL/2.0/.

package ssa

import (
	"context"

	"k8s.io/apimachinery/pkg/api/meta"
	"k8s.io/client-go/dynamic"
	"sigs.k8s.io/cli-utils/pkg/object"

	"github.com/siderolabs/go-kubernetes/kubernetes/ssa/internal/inventory/configmap"
)

// InventoryAnnotationKey is the annotation key used to store the inventory ID in the applied objects.
const InventoryAnnotationKey = "config.k8s.io/owning-inventory"

// Inventory holds the previously applied manifests state.
type Inventory interface {
	// ID returns the inventory identifier.
	ID() string
	// Read returns the list of object references tracked in the inventory
	Read(context.Context) (object.ObjMetadataSet, error)
	// Write writes the inventory.
	Write(context.Context, object.ObjMetadataSet) error
	// GetPruneObjs returns the objects that should be pruned.
	GetPruneObjs(context.Context, object.UnstructuredSet) (object.UnstructuredSet, error)
	// Delete removes the inventory from the cluster.
	Delete(context.Context) error
}

// GetInventory returns the inventory object for the given inventory ID.
func GetInventory(ctx context.Context, dynamicClient *dynamic.DynamicClient, mapper meta.RESTMapper, inventoryNamespace, inventoryName string) (Inventory, error) {
	if err := configmap.AssureInventoryNamespace(ctx, inventoryNamespace, dynamicClient); err != nil {
		return nil, err
	}

	factory := &factoryMock{
		dynamicClient: dynamicClient,
		mapper:        mapper,
	}

	return configmap.NewInventory(ctx, inventoryNamespace, inventoryName, factory)
}
