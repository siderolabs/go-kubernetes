// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at http://mozilla.org/MPL/2.0/.

package ssa

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"time"

	"github.com/fluxcd/pkg/ssa"
	"github.com/fluxcd/pkg/ssa/utils"
	"github.com/go-logr/logr"
	"github.com/siderolabs/go-retry/retry"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	v1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"

	"github.com/siderolabs/go-kubernetes/kubernetes"
	"github.com/siderolabs/go-kubernetes/kubernetes/ssa/object"
)

// ApplyOptions defines the options for the Apply method.
type ApplyOptions struct {
	// InventoryPolicy defines if an inventory object can take over objects that belong to another inventory object or don't belong to any inventory object.
	InventoryPolicy InventoryPolicy
	// DeletePropagationPolicy configures the delete operation propagation policy.
	DeletePropagationPolicy v1.DeletionPropagation
	// WaitInterval defines the interval at which the engine polls for cluster
	// scoped resources to reach their final state.
	WaitInterval time.Duration
	// WaitTimeout defines after which interval should the engine give up on waiting for
	// cluster scoped resources to reach their final state.
	WaitTimeout time.Duration
	// NoPrune defines whether pruning of previously applied objects should occur.
	NoPrune bool
	// Force configures the engine to recreate objects that contain immutable field changes.
	Force bool

	// TODO: implement if ever needed (fluxcd/ssa doesn't support out of the box)
	// ForceConflicts overwrites the fields when applying even if the field manager differs.
	// ForceConflicts bool
}

// Action represents the type of change that occurred to an object as a result of an SSA operation.
type Action = ssa.Action

const (
	CreatedAction    Action = ssa.CreatedAction
	ConfiguredAction Action = ssa.ConfiguredAction
	UnchangedAction  Action = ssa.UnchangedAction
	DeletedAction    Action = ssa.DeletedAction
	SkippedAction    Action = ssa.SkippedAction
	UnknownAction    Action = ssa.UnknownAction
)

// ChangeSetEntry represents the change to a single object as a result of an SSA operation.
type ChangeSetEntry struct {
	ObjMetadata  object.ObjMetadata
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
	ctx = logr.NewContext(ctx, logr.FromContextOrDiscard(ctx))

	// Use this map to track changes made. Return it only once the changes have been made.
	// Keyed by ObjMetadata (group/kind/namespace/name) to avoid lookup mismatches when
	// the API server's dry-run response carries a different apiVersion than the input
	// object — e.g., for CRs whose CRD has multiple versions or a conversion webhook.
	changeMap := make(map[object.ObjMetadata]*Change)

	// prepare the map
	for _, obj := range objects {
		// use UnknownAction state as a placeholder for unchanged actions and remove them later
		change := changeFromObject(obj, "", ssa.UnknownAction)
		changeMap[change.ObjMetadata] = &change
	}

	inv, err := m.inventory(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to get inventory, %w", err)
	}

	if err = m.prepareObjects(objects, inv.ID()); err != nil {
		return nil, fmt.Errorf("failed to prepare objects for apply, %w", err)
	}

	setDefaultApplyOps(&ops)

	for _, obj := range objects {
		_, diff, diffErr := m.diff(ctx, obj, ops.Force, ops.InventoryPolicy, inv.ID())
		if diffErr != nil {
			return nil, fmt.Errorf("failed to diff object %s, %w", FormatObjectPathWithGV(obj), diffErr)
		}

		changeMap[object.ObjMetadata{
			Namespace: obj.GetNamespace(),
			Name:      obj.GetName(),
			GroupKind: obj.GroupVersionKind().GroupKind(),
		}].Diff = diff
	}

	// invalidate the RESTMapper cache to avoid potential "no matches for kind" errors during apply
	// when CRDs are applied alongside their custom resources
	if containsCRDs(objects) {
		m.mapper.Reset()
	}

	changeSet := ssa.NewChangeSet()

	applyErr := retry.Constant(3*time.Minute, retry.WithUnits(10*time.Second), retry.WithErrorLogging(true)).RetryWithContext(ctx, func(ctx context.Context) error {
		var result *ssa.ChangeSet

		result, err = m.resourceManager.ApplyAllStaged(ctx, objects, ssa.ApplyOptions{
			Force:        ops.Force,
			WaitInterval: ops.WaitInterval,
			WaitTimeout:  ops.WaitTimeout,
		})

		// only push new results to avoid "unchanged" results for objects that were already applied
		for _, entry := range result.Entries {
			if !slices.ContainsFunc(changeSet.Entries, func(e ssa.ChangeSetEntry) bool { return e.Subject == entry.Subject }) {
				changeSet.Add(entry)
			}
		}

		if kubernetes.IsRetryableError(err) {
			return retry.ExpectedError(err)
		}

		return err
	})
	if applyErr != nil && changeSet == nil {
		return nil, fmt.Errorf("apply failed: %w", applyErr)
	}

	inventoryObjRefs := inv.Get()

	var invErr error

	for _, e := range changeSet.Entries {
		switch e.Action {
		case ssa.ConfiguredAction, ssa.CreatedAction, ssa.SkippedAction, ssa.UnchangedAction:
			if c, ok := changeMap[e.ObjMetadata]; ok {
				c.Action = e.Action
			}

			if inventoryObjRefs.Contains(e.ObjMetadata) {
				continue
			}

			inventoryObjRefs = append(inventoryObjRefs, e.ObjMetadata)
		// should never happen
		case ssa.DeletedAction, ssa.UnknownAction:
			invErr = errors.Join(invErr, fmt.Errorf("unexpected %q action taken by resourceManager for resource %s", e.Action, e.Subject))
		}
	}

	// Write the newly deployed objects to the inventory .
	inv.Update(inventoryObjRefs)
	invErr = errors.Join(invErr, inv.Write(ctx))

	// return if there were inventory or apply errors and skip pruning
	err = errors.Join(applyErr, invErr)
	if err != nil {
		return changesMapToArray(changeMap), err
	}

	if ops.NoPrune {
		return changesMapToArray(changeMap), nil
	}

	pruneObjRefs := calculatePruneObjects(inventoryObjRefs, objects)

	var pruneErr error

	for _, objMeta := range pruneObjRefs {
		obj, err := m.resourceManager.Get(ctx, objMeta)
		if err != nil {
			if apierrors.IsNotFound(err) {
				// object doesn't exist, remove it from inventory and continue
				inventoryObjRefs = inventoryObjRefs.Remove(objMeta)

				continue
			}

			return nil, fmt.Errorf("failed to get object %s, %w", FormatMetaPath(objMeta), err)
		}

		err = checkInventoryPolicy(inv.ID(), obj, ops.InventoryPolicy)
		if err != nil {
			return nil, invPolicyFailureErr(obj, err)
		}

		e, err := m.resourceManager.Delete(ctx, obj, ssa.DeleteOptions{
			PropagationPolicy: v1.DeletePropagationBackground,
		})
		if err != nil {
			pruneErr = errors.Join(pruneErr, err)

			continue
		}

		inventoryObjRefs = inventoryObjRefs.Remove(e.ObjMetadata)

		diff, err := createDeletedDiff(obj)
		if err != nil {
			pruneErr = errors.Join(pruneErr, err)
		}

		change := changeFromObject(obj, diff, ssa.DeletedAction)
		changeMap[change.ObjMetadata] = &change
	}

	inv.Update(inventoryObjRefs)
	invErr = inv.Write(ctx)

	return changesMapToArray(changeMap), errors.Join(pruneErr, invErr)
}

func invPolicyFailureErr(obj *unstructured.Unstructured, err error) error {
	return fmt.Errorf("inventory policy check failure for object %s, %w", FormatObjectPathWithGV(obj), err)
}

func calculatePruneObjects(inventoryObjRefs object.ObjMetadataSet, objects []*unstructured.Unstructured) object.ObjMetadataSet {
	pruneObjRefs := object.ObjMetadataSet{}

	for _, ref := range inventoryObjRefs {
		found := false

		for _, obj := range objects {
			if ref.Equals(&object.ObjMetadata{
				Namespace: obj.GetNamespace(),
				Name:      obj.GetName(),
				GroupKind: obj.GroupVersionKind().GroupKind(),
			}) {
				found = true

				break
			}
		}

		if !found {
			pruneObjRefs = append(pruneObjRefs, ref)
		}
	}

	return pruneObjRefs
}

// prepareObjects prepares the objects before diff/apply actions.
func (m *Manager) prepareObjects(objects []*unstructured.Unstructured, inventoryID string) error {
	for _, obj := range objects {
		annotations := obj.GetAnnotations()

		if annotations == nil {
			annotations = make(map[string]string)
		}

		inventoryAnnotation, inventoryAnnotationSet := annotations[InventoryAnnotationKey]

		if inventoryAnnotationSet && inventoryAnnotation != inventoryID {
			return fmt.Errorf("object %s already has an inventory annotation", FormatObjectPathWithGV(obj))
		}

		annotations[InventoryAnnotationKey] = inventoryID
		obj.SetAnnotations(annotations)
	}

	return nil
}

func setDefaultApplyOps(ops *ApplyOptions) {
	if ops.WaitInterval == 0 {
		ops.WaitInterval = ssa.DefaultApplyOptions().WaitInterval
	}

	if ops.WaitTimeout == 0 {
		ops.WaitTimeout = ssa.DefaultApplyOptions().WaitTimeout
	}

	if ops.DeletePropagationPolicy == "" {
		ops.DeletePropagationPolicy = v1.DeletePropagationBackground
	}

	if ops.InventoryPolicy == "" {
		ops.InventoryPolicy = InventoryPolicyMustMatch
	}
}

func changeFromObject(obj *unstructured.Unstructured, diff string, action Action) Change {
	return Change{
		ChangeSetEntry: ChangeSetEntry{
			ObjMetadata: object.ObjMetadata{
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

func changesMapToArray(changeMap map[object.ObjMetadata]*Change) []Change {
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

func containsCRDs(objects []*unstructured.Unstructured) bool {
	for _, obj := range objects {
		gvk := obj.GroupVersionKind()
		if gvk.Group == "apiextensions.k8s.io" && gvk.Kind == "CustomResourceDefinition" {
			return true
		}
	}

	return false
}
