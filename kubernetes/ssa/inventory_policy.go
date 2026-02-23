// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at http://mozilla.org/MPL/2.0/.

package ssa

import (
	"fmt"
)

// InventoryPolicy defines if an inventory object can take over
// objects that belong to another inventory object or don't
// belong to any inventory object.
// This is done by determining if the apply/prune operation
// can go through for a resource based on the comparison
// the inventory-id value in the package and the owning-inventory
// annotation in the live object.
type InventoryPolicy string

const (
	// InventoryPolicyMustMatch: This policy enforces that the resources being applied can not
	// have any overlap with objects in other inventories or objects that already exist
	// in the cluster but don't belong to an inventory.
	//
	// The apply operation can go through when
	// - A new resources in the package doesn't exist in the cluster
	// - An existing resource in the package doesn't exist in the cluster
	// - An existing resource exist in the cluster. The owning-inventory annotation in the live object
	//   matches with that in the package.
	//
	// The prune operation can go through when
	// - The owning-inventory annotation in the live object match with that
	//   in the package.
	InventoryPolicyMustMatch InventoryPolicy = "MustMatch"

	// InventoryPolicyAdoptIfNoInventory: This policy enforces that resources being applied
	// can not have any overlap with objects in other inventories, but are
	// permitted to take ownership of objects that don't belong to any inventories.
	//
	// The apply operation can go through when
	// - New resource in the package doesn't exist in the cluster
	// - If a new resource exist in the cluster, its owning-inventory annotation is empty
	// - Existing resource in the package doesn't exist in the cluster
	// - If existing resource exist in the cluster, its owning-inventory annotation in the live object
	//   is empty
	// - An existing resource exist in the cluster. The owning-inventory annotation in the live object
	//   matches with that in the package.
	//
	// The prune operation can go through when
	// - The owning-inventory annotation in the live object match with that
	//   in the package.
	// - The live object doesn't have the owning-inventory annotation.
	InventoryPolicyAdoptIfNoInventory InventoryPolicy = "AdoptIfNoInventory"

	// InventoryPolicyAdoptAll: This policy will let the current inventory take ownership of any objects.
	//
	// The apply operation can go through for any resource in the package even if the
	// live object has an unmatched owning-inventory annotation.
	//
	// The prune operation can go through when
	// - The owning-inventory annotation in the live object match or doesn't match with that
	//   in the package.
	// - The live object doesn't have the owning-inventory annotation.
	InventoryPolicyAdoptAll InventoryPolicy = "AdoptAll"
)

type annotated interface {
	GetAnnotations() map[string]string
}

type invIDMatchStatus string

const (
	Empty   invIDMatchStatus = "Empty"
	Match   invIDMatchStatus = "Match"
	NoMatch invIDMatchStatus = "NoMatch"
)

func checkInventoryPolicy(invID string, obj annotated, policy InventoryPolicy) error {
	annotations := obj.GetAnnotations()
	value, found := annotations[InventoryAnnotationKey]

	matchStatus := NoMatch
	if !found {
		matchStatus = Empty
	}

	if value == invID {
		matchStatus = Match
	}

	switch matchStatus {
	case Empty:
		if policy != InventoryPolicyMustMatch {
			return nil
		}
	case Match:
		return nil
	case NoMatch:
		if policy == InventoryPolicyAdoptAll {
			return nil
		}
	default:
		return fmt.Errorf("invalid inventory policy: %v", policy)
	}

	return fmt.Errorf("inventory policy prevented actuation (status: %s, policy: %s)", matchStatus, policy)
}
