// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at http://mozilla.org/MPL/2.0/.

package ssa

import (
	"context"
	"fmt"
	"strings"

	"github.com/fluxcd/cli-utils/pkg/object"
	"github.com/fluxcd/pkg/ssa"
	"github.com/siderolabs/talos/pkg/machinery/textdiff"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"sigs.k8s.io/cli-utils/pkg/inventory"
	k8syaml "sigs.k8s.io/yaml"
)

type DiffOptions struct {
	// NoPrune defines whether pruning of previously applied objects should occur.
	NoPrune bool
	// Force configures the engine to recreate objects that contain immutable field changes.
	Force bool
	// Policy defines if an inventory object can take over objects that belong to another inventory object or don't belong to any inventory object.
	InventoryPolicy inventory.Policy
}

// DiffAction are a subset of actions that are returned by the Diff method.
type DiffAction string

const (
	DiffCreatedAction    = DiffAction(CreatedAction)
	DiffConfiguredAction = DiffAction(ConfiguredAction)
	DiffPrunedAction     = DiffAction("Pruned")
	DiffUnchangedAction  = DiffAction(UnchangedAction)
)

// DiffResult is a diff result for one object.
// In case of a Prune action the deleted object is set as the 'DryRunResultObject'.
type DiffResult struct {
	// Action is the Diff action.
	Action DiffAction
	// Diff is the human readable string diff.
	Diff string
	// ObjMetadata holds the unique identifier of this entry.
	ObjMetadata object.ObjMetadata

	// GroupVersion holds the API group version of this entry.
	GroupVersion string

	// Subject represents the Object ID in the format 'kind/namespace/name'.
	Subject string
}

// Diff does a server side apply dry-run and returns the resulting diff.
func (m *Manager) Diff(ctx context.Context, objects []*unstructured.Unstructured, ops DiffOptions) ([]DiffResult, error) {
	result := []DiffResult{}

	if err := m.prepareObjects(objects); err != nil {
		return nil, err
	}

	pruneObjs, err := m.inventory.GetPruneObjs(ctx, objects)
	if err != nil {
		return nil, err
	}

	if !ops.NoPrune {
		for _, obj := range pruneObjs {
			_, err = inventory.CanApply(inventoryIDInfo{id: m.inventory.ID()}, obj, ops.InventoryPolicy)
			if err != nil {
				return nil, fmt.Errorf("inventory policy check failure for object %s, %w", FormatObjectPath(obj), err)
			}

			// create a "deleted" diff
			diff, err1 := createDeletedDiff(obj)
			if err1 != nil {
				return nil, err1
			}

			result = append(result, DiffResult{
				Action: DiffPrunedAction,
				Diff:   diff,
			})
		}
	}

	for _, obj := range objects {
		var action DiffAction

		changeSet, diff, err := m.diff(ctx, obj, ops.Force, ops.InventoryPolicy)
		if err != nil {
			return nil, err
		}

		switch changeSet.Action {
		case ssa.ConfiguredAction:
			action = DiffConfiguredAction
		case ssa.CreatedAction:
			action = DiffCreatedAction
		case ssa.UnchangedAction:
			action = DiffUnchangedAction
		case ssa.DeletedAction, ssa.SkippedAction, ssa.UnknownAction:
			return nil, fmt.Errorf("unexpected %q result received from Diff function %s", changeSet.Action, changeSet.Subject)
		}

		result = append(result, DiffResult{
			Action:       action,
			Diff:         diff,
			ObjMetadata:  changeSet.ObjMetadata,
			GroupVersion: changeSet.GroupVersion,
			Subject:      changeSet.Subject,
		})
	}

	return result, nil
}

func (m *Manager) diff(ctx context.Context, inputObj *unstructured.Unstructured, force bool, invPolicy inventory.Policy) (*ssa.ChangeSetEntry, string, error) {
	changeSet, inClusterObj, dryRunResult, err := m.resourceManager.Diff(ctx, inputObj, ssa.DiffOptions{Force: force})
	if err != nil && (apierrors.IsNotFound(err) || strings.Contains(err.Error(), "not found")) {
		if changeSet == nil {
			changeSet = &ssa.ChangeSetEntry{
				ObjMetadata:  object.UnstructuredToObjMetadata(inputObj),
				GroupVersion: inputObj.GroupVersionKind().Version,
				Subject:      FormatObjectPath(inputObj),
			}
		}

		changeSet.Action = ssa.CreatedAction
	} else if err != nil {
		return nil, "", fmt.Errorf("apply dry run failed for %s: %w", FormatObjectPath(inputObj), err)
	}

	diff := ""

	if changeSet.Action == ssa.CreatedAction {
		diff, err = renderManifestDiff(nil, inputObj)
		if err != nil {
			return nil, "", err
		}
	}

	if changeSet.Action == ssa.ConfiguredAction {
		diff, err = renderManifestDiff(inClusterObj, dryRunResult)
		if err != nil {
			return nil, "", err
		}
	}

	// should never happen, but just in case
	if changeSet.Action == ssa.DeletedAction {
		diff, err = createDeletedDiff(inputObj)
		if err != nil {
			return nil, "", err
		}
	}

	// inventory conflict check: only relevant for modified objects
	if changeSet.Action == ssa.ConfiguredAction {
		_, err = inventory.CanApply(inventoryIDInfo{id: m.inventory.ID()}, inClusterObj, invPolicy)
		if err != nil {
			return nil, "", fmt.Errorf("inventory policy check failure for object %s, %w", changeSet.Subject, err)
		}
	}

	return changeSet, diff, nil
}

func createDeletedDiff(obj *unstructured.Unstructured) (string, error) {
	diffObj := obj.DeepCopy()
	// remove managed fields as they're not really useful for the prune diff
	diffObj.SetManagedFields(nil)

	diff, err := renderManifestDiff(diffObj, nil)
	if err != nil {
		return "", err
	}

	return diff, nil
}

func renderManifestDiff(a, b *unstructured.Unstructured) (string, error) {
	var (
		ma, mb []byte
		path   string
		err    error
	)

	if a != nil {
		path = FormatObjectPathWithGV(a)

		ma, err = k8syaml.Marshal(a)
		if err != nil {
			return "", err
		}
	}

	if b != nil {
		path = FormatObjectPathWithGV(b)

		mb, err = k8syaml.Marshal(b)
		if err != nil {
			return "", err
		}
	}

	return computeDiff(path, string(ma), string(mb))
}

func computeDiff(path string, a, b string) (string, error) {
	return textdiff.DiffWithCustomPaths(a, b, "a/"+path, "b/"+path)
}
