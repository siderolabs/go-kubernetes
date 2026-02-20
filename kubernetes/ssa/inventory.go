// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at http://mozilla.org/MPL/2.0/.

package ssa

import (
	"context"

	"sigs.k8s.io/cli-utils/pkg/object"
)

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
