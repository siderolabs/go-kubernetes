// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at http://mozilla.org/MPL/2.0/.

package ssa

import (
	"context"
	"errors"
	"fmt"
	"time"

	fluxobj "github.com/fluxcd/cli-utils/pkg/object"
	"github.com/fluxcd/pkg/ssa"
	"github.com/fluxcd/pkg/ssa/utils"
	v1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"sigs.k8s.io/cli-utils/pkg/inventory"
	"sigs.k8s.io/cli-utils/pkg/object"
)

type ApplyOptions struct {
	// DeletePropagationPolicy configures the delete operation propagation policy.
	DeletePropagationPolicy v1.DeletionPropagation
	// Policy defines if an inventory object can take over objects that belong to another inventory object or don't belong to any inventory object.
	InventoryPolicy inventory.Policy
	// WaitInterval defines the interval at which the engine polls for cluster
	// scoped resources to reach their final state.
	WaitInterval time.Duration
	// WaitTimeout defines after which interval should the engine give up on waiting for
	// cluster scoped resources to reach their final state.
	WaitTimeout time.Duration
	// NoPrune defines whether pruning of previously applied objects should occur.
	NoPrune bool
	// ForceConflicts overwrites the fields when applying if the field manager differs.
	ForceConflicts bool
	// Force configures the engine to recreate objects that contain immutable field changes.
	Force bool
}

type ObjMetadata = fluxobj.ObjMetadata

type Action = ssa.Action

const (
	CreatedAction    Action = ssa.CreatedAction
	ConfiguredAction Action = ssa.ConfiguredAction
	UnchangedAction  Action = ssa.UnchangedAction
	DeletedAction    Action = ssa.DeletedAction
	SkippedAction    Action = ssa.SkippedAction
	UnknownAction    Action = ssa.UnknownAction
)

type ChangeSetEntry struct {
	ObjMetadata  ObjMetadata
	GroupVersion string
	Subject      string
	Action       Action
}

// Check that structs are equal.
var _ ChangeSetEntry = ChangeSetEntry(ssa.ChangeSetEntry{})

type Change struct {
	ChangeSetEntry

	Diff string
}

// Apply applies a given set of manifests via ssa, prunes unneeded objects and updates the backing inventory.
// Changes are returned for actions successfully taken, even if an error is encountered half way through.
// Objects are pruned as a last step only after all other manifests are successfully applied.
//
// CRDs, ClusterRoles, and Namespaces and Class definitions are applied first.
// Once they are ready, the rest of the manifests are applied and results are returned immediately
// without waiting for the resources to reach ready state.
//
//nolint:gocyclo,gocognit,cyclop
func (m *Manager) Apply(ctx context.Context, objects []*unstructured.Unstructured, ops ApplyOptions) ([]Change, error) {
	// Use this map to track changes made. Return it only once the changes have been made.
	changeMap := make(map[string]*Change)

	// prepare the map
	for _, obj := range objects {
		// use UnknownAction state as a placeholder for unchanged actions and remove them later
		change := changeFromObject(obj, "", ssa.UnknownAction)
		changeMap[FormatObjectPathWithGV(obj)] = &change
	}

	if err := m.prepareObjects(objects); err != nil {
		return nil, err
	}

	setDefaultOps(&ops)

	for _, obj := range objects {
		changeSet, diff, err := m.diff(ctx, obj, ops.Force, ops.InventoryPolicy)
		if err != nil {
			return nil, err
		}

		changeMap[FormatObjectPathWithGV(obj)].Diff = diff

		// only perform the inventory policy check for modified actions as that's the only conflict possibility
		if changeSet.Action != ssa.ConfiguredAction {
			continue
		}

		_, err = inventory.CanApply(inventoryIDInfo{id: m.inventory.ID()}, obj, ops.InventoryPolicy)
		if err != nil {
			return nil, fmt.Errorf("inventory policy check failure for object %s, %w", changeSet.Subject, err)
		}
	}

	changeSet, applyErr := m.resourceManager.ApplyAllStaged(ctx, objects, ssa.ApplyOptions{
		Force:        ops.Force,
		WaitInterval: ops.WaitInterval,
		WaitTimeout:  ops.WaitTimeout,
	})
	if applyErr != nil && changeSet == nil {
		return nil, applyErr
	}

	inventoryObjRefs, err := m.inventory.Read(ctx)
	if err != nil {
		return nil, err
	}

	var invErr error

	for _, e := range changeSet.Entries {
		switch e.Action {
		case ssa.ConfiguredAction, ssa.CreatedAction, ssa.SkippedAction, ssa.UnchangedAction:
			changeMap[FormatObjectMetaPath(e.ObjMetadata, e.GroupVersion)].Action = e.Action

			if inventoryObjRefs.Contains(object.ObjMetadata(e.ObjMetadata)) {
				continue
			}

			inventoryObjRefs = append(inventoryObjRefs, object.ObjMetadata(e.ObjMetadata))
		// should never happen
		case ssa.DeletedAction, ssa.UnknownAction:
			invErr = errors.Join(invErr, fmt.Errorf("unexpected %q action taken by resourceManager for resource %s", e.Action, e.Subject))
		}
	}

	invErr = errors.Join(invErr, m.inventory.Write(ctx, inventoryObjRefs))

	// return if there were inventory or apply errors and skip pruning
	err = errors.Join(applyErr, invErr)
	if err != nil {
		return changesMapToArray(changeMap), err
	}

	if ops.NoPrune {
		return changesMapToArray(changeMap), nil
	}

	pruneObj, err := m.inventory.GetPruneObjs(ctx, objects)
	if err != nil {
		return changesMapToArray(changeMap), fmt.Errorf("failed to get prune objects: %w", err)
	}

	var pruneErr error

	for _, obj := range pruneObj {
		_, err = inventory.CanApply(inventoryIDInfo{id: m.inventory.ID()}, obj, ops.InventoryPolicy)
		if err != nil {
			return nil, fmt.Errorf("inventory policy check failure for object %s, %w", FormatObjectPathWithGV(obj), err)
		}

		e, err := m.resourceManager.Delete(ctx, obj, ssa.DeleteOptions{
			PropagationPolicy: v1.DeletePropagationBackground,
		})
		if err != nil {
			pruneErr = errors.Join(pruneErr, err)

			continue
		}

		inventoryObjRefs = inventoryObjRefs.Remove(object.ObjMetadata(e.ObjMetadata))

		diff, err := createDeletedDiff(obj)
		if err != nil {
			pruneErr = errors.Join(pruneErr, err)
		}

		change := changeFromObject(obj, diff, ssa.DeletedAction)
		changeMap[FormatObjectPathWithGV(obj)] = &change
	}

	invErr = m.inventory.Write(ctx, inventoryObjRefs)

	return changesMapToArray(changeMap), errors.Join(pruneErr, invErr)
}

// prepareObjects prepares the objects before diff/apply actions.
func (m *Manager) prepareObjects(objects []*unstructured.Unstructured) error {
	for _, obj := range objects {
		annotations := obj.GetAnnotations()

		inventoryAnnotation, inventoryAnnotationSet := annotations[InventoryAnnotationKey]

		if inventoryAnnotationSet && inventoryAnnotation != m.inventory.ID() {
			return fmt.Errorf("object %s already has an inventory annotation", FormatObjectPathWithGV(obj))
		}

		inventory.AddInventoryIDAnnotation(obj, inventory.ID(m.inventory.ID()))
	}

	return nil
}

func setDefaultOps(ops *ApplyOptions) {
	if ops.WaitInterval == 0 {
		ops.WaitInterval = ssa.DefaultApplyOptions().WaitInterval
	}

	if ops.WaitTimeout == 0 {
		ops.WaitTimeout = ssa.DefaultApplyOptions().WaitTimeout
	}

	if ops.DeletePropagationPolicy == "" {
		ops.DeletePropagationPolicy = v1.DeletePropagationBackground
	}
}

func changeFromObject(obj *unstructured.Unstructured, diff string, action Action) Change {
	return Change{
		ChangeSetEntry: ChangeSetEntry{
			ObjMetadata: ObjMetadata{
				Namespace: obj.GetNamespace(),
				Name:      obj.GetName(),
				GroupKind: obj.GroupVersionKind().GroupKind(),
			},
			Subject:      utils.FmtUnstructured(obj),
			GroupVersion: obj.GetAPIVersion(),
			Action:       action,
		},
		Diff: diff,
	}
}

func changesMapToArray(changeMap map[string]*Change) []Change {
	changes := []Change{}

	for _, c := range changeMap {
		// skip unknown actions as this state was used as a placeholder for actions not taken
		// most likely these remain if an error was encountered half way through
		if c.Action == ssa.UnknownAction {
			continue
		}

		changes = append(changes, *c)
	}

	return changes
}

// inventoryIDInfo implements inventory.Info, but only returns the ID.
type inventoryIDInfo struct {
	id string
}

func (info inventoryIDInfo) GetNamespace() string {
	// we don't know the namespace of a generic inventory
	return ""
}

func (info inventoryIDInfo) GetID() inventory.ID {
	return inventory.ID(info.id)
}
