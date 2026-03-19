// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at http://mozilla.org/MPL/2.0/.

// nolint: contextcheck,godoclint
package ssa_test

import (
	"context"
	_ "embed"
	"errors"
	"testing"

	fluxssa "github.com/fluxcd/pkg/ssa"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	sigsyaml "sigs.k8s.io/yaml"

	"github.com/siderolabs/go-kubernetes/kubernetes/ssa"
	"github.com/siderolabs/go-kubernetes/kubernetes/ssa/internal/inventory/memory"
	"github.com/siderolabs/go-kubernetes/kubernetes/ssa/internal/resourcemanager"
)

type mapperMock struct{}

func (m *mapperMock) Reset() {}

//go:embed testdata/widget.yaml
var widgetYAML []byte

//go:embed testdata/widget_crd.yaml
var widgetCRDYAML []byte

func testInventoryClosure(_ context.Context, inv ssa.Inventory) ssa.InventoryFactory {
	return func(context.Context) (ssa.Inventory, error) {
		return inv, nil
	}
}

func testInventoryFactory(context.Context) (ssa.Inventory, error) {
	return memory.NewInventory("test-inventory"), nil
}

func TestCreateAllNew(t *testing.T) {
	rm := resourcemanager.NewMock()
	manager := ssa.NewCustomManager(rm, testInventoryFactory, nil, &mapperMock{})
	obj := getConfigmapManifest("test-cm")

	results, err := manager.Apply(t.Context(), []*unstructured.Unstructured{obj}, ssa.ApplyOptions{})
	require.NoError(t, err)

	require.Len(t, results, 1)
	assert.Equal(t, ssa.CreatedAction, results[0].Action)
	assert.Contains(t, results[0].Diff, "+apiVersion: v1")
	assert.Contains(t, results[0].Diff, "+  key: value")
	assert.Equal(t, "ConfigMap/default/test-cm", results[0].Subject)
}

// brokenApplyResourceManager fails to apply the second object after successfully applying the first one.
type brokenApplyResourceManager struct {
	resourcemanager.Mock
}

func (m brokenApplyResourceManager) ApplyAllStaged(ctx context.Context, objects []*unstructured.Unstructured, opts fluxssa.ApplyOptions) (*fluxssa.ChangeSet, error) {
	changeSet, err := m.Mock.ApplyAllStaged(ctx, []*unstructured.Unstructured{objects[0]}, opts)
	if err != nil {
		panic("err should be nil")
	}

	return changeSet, errors.New("apply failed")
}

func TestApplyError(t *testing.T) {
	rm := &brokenApplyResourceManager{}
	inv := memory.NewInventory("test-inventory")
	manager := ssa.NewCustomManager(rm, testInventoryClosure(t.Context(), inv), nil, &mapperMock{})
	obj1 := getConfigmapManifest("configmap1")
	obj2 := getConfigmapManifest("configmap2")

	results, err := manager.Apply(t.Context(), []*unstructured.Unstructured{obj1, obj2}, ssa.ApplyOptions{})
	require.EqualError(t, err, "apply failed", "the manager should return the error from the resourceManager apply")

	require.Len(t, results, 1, "results for applied objects should exist")

	invContents := inv.Get()
	require.Len(t, invContents, 1, "inventory should contain data about objects applied successfully")
	assert.Equal(t, invContents[0].Name, obj1.GetName())
}

func TestApplyError_No_Prune(t *testing.T) {
	rm := &brokenApplyResourceManager{}
	inv := memory.NewInventory("test-inventory")
	manager := ssa.NewCustomManager(rm, testInventoryClosure(t.Context(), inv), nil, &mapperMock{})
	obj1 := getConfigmapManifest("configmap1")
	obj2 := getConfigmapManifest("configmap2")
	existingObj := getConfigmapManifest("prune-configmap")

	setExistingObjects(t, &rm.Mock, inv, existingObj)

	// don't add the existingObj here, so it not being pruned can be tested
	results, err := manager.Apply(t.Context(), []*unstructured.Unstructured{obj1, obj2}, ssa.ApplyOptions{})
	require.Error(t, err)

	require.Len(t, results, 1)

	invContents := inv.Get()
	require.Len(t, invContents, 2, "inventory should still contain the prune-configmap")
	assert.Equal(t, "prune-configmap", invContents[0].Name)
	assert.Equal(t, obj1.GetName(), invContents[1].Name)
}

func TestResultDiff(t *testing.T) {
	rm := resourcemanager.NewMock()
	inv := memory.NewInventory("test-inventory")
	manager := ssa.NewCustomManager(rm, testInventoryClosure(t.Context(), inv), nil, &mapperMock{})

	existingObj := &unstructured.Unstructured{
		Object: map[string]any{
			"apiVersion": "v1",
			"kind":       "ConfigMap",
			"metadata": map[string]any{
				"name":      "existing-cm",
				"namespace": "default",
			},
			"data": map[string]any{
				"key": "old-value",
			},
		},
	}
	pruneObj := getConfigmapManifest("prune-cm")

	setExistingObjects(t, rm, inv, existingObj, pruneObj)

	modifiedObj := existingObj.DeepCopy()
	modifiedObj.Object["data"] = map[string]any{
		"key": "new-value",
	}

	newObj := getConfigmapManifest("new-cm")

	// Apply modified + new objects, omitting pruneObj so it gets pruned.
	results, err := manager.Apply(t.Context(), []*unstructured.Unstructured{modifiedObj, newObj}, ssa.ApplyOptions{InventoryPolicy: ssa.InventoryPolicyAdoptAll})
	require.NoError(t, err)
	require.Len(t, results, 3)

	resultsByName := map[string]ssa.Change{}
	for _, r := range results {
		resultsByName[r.ObjMetadata.Name] = r
	}

	// Created object: diff shows full manifest as additions.
	created := resultsByName["new-cm"]
	assert.Equal(t, ssa.CreatedAction, created.Action)
	assert.Contains(t, created.Diff, "+apiVersion: v1")
	assert.Contains(t, created.Diff, "+  key: value")

	// Configured object: diff shows the changed field.
	configured := resultsByName["existing-cm"]
	assert.Equal(t, ssa.ConfiguredAction, configured.Action)
	assert.Contains(t, configured.Diff, "-  key: old-value")
	assert.Contains(t, configured.Diff, "+  key: new-value")

	// Pruned object: diff shows manifest as deletions.
	pruned := resultsByName["prune-cm"]
	assert.Equal(t, ssa.DeletedAction, pruned.Action)
	assert.Contains(t, pruned.Diff, "-kind: ConfigMap")
	assert.Contains(t, pruned.Diff, "-  name: prune-cm")
}

// brokenWriteInventory wraps memory.Inventory and fails on Write calls.
type brokenWriteInventory struct {
	writeErr error
	memory.Inventory
}

func (i *brokenWriteInventory) Write(_ context.Context) error {
	return i.writeErr
}

func TestInventoryErrors(t *testing.T) {
	t.Run("write_error_after_apply", func(t *testing.T) {
		rm := resourcemanager.NewMock()
		writeErr := errors.New("inventory write failed")
		inv := &brokenWriteInventory{
			Inventory: *memory.NewInventory("test-inventory"),
			writeErr:  writeErr,
		}
		manager := ssa.NewCustomManager(rm, func(ctx context.Context) (ssa.Inventory, error) { return inv, nil }, nil, &mapperMock{})

		obj := getConfigmapManifest("test-cm")

		results, err := manager.Apply(t.Context(), []*unstructured.Unstructured{obj}, ssa.ApplyOptions{})
		require.ErrorIs(t, err, writeErr)
		require.Len(t, results, 1, "results for successfully applied objects should still be returned")
		assert.Equal(t, ssa.CreatedAction, results[0].Action)

		// The object should still be applied in the cluster even though inventory write failed.
		applied := rm.GetObject("", "ConfigMap", "default", "test-cm")
		require.NotNil(t, applied, "object should exist in cluster despite inventory write failure")
	})

	t.Run("write_error_skips_pruning", func(t *testing.T) {
		rm := resourcemanager.NewMock()
		writeErr := errors.New("inventory write failed")
		inv := &brokenWriteInventory{
			Inventory: *memory.NewInventory("test-inventory"),
			writeErr:  writeErr,
		}
		manager := ssa.NewCustomManager(rm, func(ctx context.Context) (ssa.Inventory, error) { return inv, nil }, nil, &mapperMock{})

		pruneObj := getConfigmapManifest("should-not-be-pruned")
		// Seed existing object directly into the resource manager since broken inventory can't Write.
		rm.SetObjects(pruneObj)

		results, err := manager.Apply(t.Context(), []*unstructured.Unstructured{}, ssa.ApplyOptions{})
		require.ErrorIs(t, err, writeErr)
		require.Empty(t, results)

		// The object must NOT have been pruned because the write error should abort before pruning.
		stillExists := rm.GetObject("", "ConfigMap", "default", "should-not-be-pruned")
		require.NotNil(t, stillExists, "objects should not be pruned when inventory write fails")
	})
}

func TestInventoryPolicy(t *testing.T) {
	t.Run("input_object_with_foreign_annotation_rejected", func(t *testing.T) {
		// The pre-apply check rejects objects that already carry a different inventory
		// annotation, regardless of the inventory policy.
		rm := resourcemanager.NewMock()
		manager := ssa.NewCustomManager(rm, testInventoryFactory, nil, &mapperMock{})

		obj := getConfigmapManifest("test-cm")
		obj.SetAnnotations(map[string]string{
			ssa.InventoryAnnotationKey: "other-inventory",
		})

		_, err := manager.Apply(t.Context(), []*unstructured.Unstructured{obj}, ssa.ApplyOptions{
			InventoryPolicy: ssa.InventoryPolicyAdoptAll,
		})
		require.Error(t, err)
		assert.Contains(t, err.Error(), "already has an inventory annotation")
	})

	t.Run("policy_failure_prevents_all_applies", func(t *testing.T) {
		// When one object fails the policy check, NO objects should be applied.
		rm := resourcemanager.NewMock()
		manager := ssa.NewCustomManager(rm, testInventoryFactory, nil, &mapperMock{})

		// This object exists in the cluster with a foreign annotation — will fail MustMatch.
		foreignObj := getConfigmapManifest("foreign-cm")
		foreignObj.SetAnnotations(map[string]string{
			ssa.InventoryAnnotationKey: "other-inventory",
		})
		rm.SetObjects(foreignObj)

		newObj := getConfigmapManifest("new-cm")
		applyForeignObj := getConfigmapManifest("foreign-cm")
		applyForeignObj.Object["data"] = map[string]any{"key": "new-value"}

		_, err := manager.Apply(t.Context(), []*unstructured.Unstructured{newObj, applyForeignObj}, ssa.ApplyOptions{})
		require.Error(t, err)
		assert.Contains(t, err.Error(), "inventory policy")

		// Neither object should be applied.
		assert.Nil(t, rm.GetObject("", "ConfigMap", "default", "new-cm"),
			"new object must not be applied when another object fails policy check")
	})

	t.Run("policy_failure_prevents_pruning", func(t *testing.T) {
		rm := resourcemanager.NewMock()
		inv := memory.NewInventory("test-inventory")
		manager := ssa.NewCustomManager(rm, testInventoryClosure(t.Context(), inv), nil, &mapperMock{})
		pruneObj := getConfigmapManifest("prune-cm")
		pruneObj.SetAnnotations(map[string]string{ssa.InventoryAnnotationKey: "foreign-inventory"})

		setExistingObjects(t, rm, inv, pruneObj)

		results, err := manager.Apply(t.Context(), []*unstructured.Unstructured{}, ssa.ApplyOptions{InventoryPolicy: ssa.InventoryPolicyMustMatch})
		require.ErrorContains(t, err, "inventory policy check failure")

		require.Len(t, results, 0)

		invContents := inv.Get()
		require.Len(t, invContents, 1, "the object should still be in the inventory after prune failure")

		obj := rm.GetObject("", "ConfigMap", "default", "prune-cm")
		require.NotNil(t, obj, "object should exist in cluster due to inventory policy check")
	})
}

// brokenDiffResourceManager returns a non-NotFound error from Diff with a non-nil in-cluster object,
// so the error is not masked by the toBeCreated path.
type brokenDiffResourceManager struct {
	resourcemanager.Mock
}

func (m *brokenDiffResourceManager) Diff(_ context.Context, obj *unstructured.Unstructured, _ fluxssa.DiffOptions) (
	*fluxssa.ChangeSetEntry, *unstructured.Unstructured, *unstructured.Unstructured, error,
) {
	return &fluxssa.ChangeSetEntry{
		Subject: "ConfigMap/default/" + obj.GetName(),
	}, obj.DeepCopy(), obj.DeepCopy(), errors.New("diff server error")
}

// brokenDeleteResourceManager fails on all Delete calls.
type brokenDeleteResourceManager struct {
	resourcemanager.Mock
}

func (m *brokenDeleteResourceManager) Delete(_ context.Context, _ *unstructured.Unstructured, _ fluxssa.DeleteOptions) (*fluxssa.ChangeSetEntry, error) {
	return nil, errors.New("delete failed")
}

func TestApplyEdgeCases(t *testing.T) {
	t.Run("idempotent_reapply", func(t *testing.T) {
		// Re-applying the same objects should not duplicate inventory entries.
		rm := resourcemanager.NewMock()
		inv := memory.NewInventory("test-inventory")
		manager := ssa.NewCustomManager(rm, testInventoryClosure(t.Context(), inv), nil, &mapperMock{})

		obj := getConfigmapManifest("test-cm")

		// First apply — creates the object.
		_, err := manager.Apply(t.Context(), []*unstructured.Unstructured{obj}, ssa.ApplyOptions{})
		require.NoError(t, err)

		invContents := inv.Get()
		require.Len(t, invContents, 1)

		// Second apply — same object.
		obj2 := getConfigmapManifest("test-cm")
		results, err := manager.Apply(t.Context(), []*unstructured.Unstructured{obj2}, ssa.ApplyOptions{})
		require.NoError(t, err)
		require.Len(t, results, 1)
		assert.Equal(t, ssa.ConfiguredAction, results[0].Action)

		invContents = inv.Get()
		require.Len(t, invContents, 1, "inventory should not contain duplicates after re-apply")
	})

	t.Run("diff_error", func(t *testing.T) {
		// A non-NotFound error from Diff should abort Apply before any objects are applied.
		rm := &brokenDiffResourceManager{}
		manager := ssa.NewCustomManager(rm, testInventoryFactory, nil, &mapperMock{})

		obj := getConfigmapManifest("test-cm")

		results, err := manager.Apply(t.Context(), []*unstructured.Unstructured{obj}, ssa.ApplyOptions{})
		require.Error(t, err)
		assert.Contains(t, err.Error(), "apply dry run failed")
		assert.Contains(t, err.Error(), "diff server error")
		assert.Nil(t, results)

		// No objects should be applied since the error occurs before ApplyAllStaged.
		assert.Nil(t, rm.GetObject("", "ConfigMap", "default", "test-cm"))
	})

	t.Run("delete_error_during_prune", func(t *testing.T) {
		// When Delete fails during pruning, the error should be returned without the change result.
		rm := &brokenDeleteResourceManager{}
		inv := memory.NewInventory("test-inventory")
		manager := ssa.NewCustomManager(rm, testInventoryClosure(t.Context(), inv), nil, &mapperMock{})

		pruneObj := getConfigmapManifest("old-cm")
		setExistingObjects(t, &rm.Mock, inv, pruneObj)

		results, err := manager.Apply(t.Context(), []*unstructured.Unstructured{}, ssa.ApplyOptions{InventoryPolicy: ssa.InventoryPolicyAdoptAll})
		require.Error(t, err)
		assert.Contains(t, err.Error(), "delete failed")
		assert.Empty(t, results)

		// The object should remain in inventory since it couldn't be deleted.
		invContents := inv.Get()
		require.Len(t, invContents, 1, "failed-to-delete object should remain in inventory")
		assert.Equal(t, "old-cm", invContents[0].Name)
	})

	t.Run("custom_resource_apply", func(t *testing.T) {
		// CRDs and their custom resources should be applied successfully in the same Apply call, even though the CRD doesn't exist at the time of diff.
		rm := resourcemanager.NewMock()
		manager := ssa.NewCustomManager(rm, testInventoryFactory, nil, &mapperMock{})

		crd := &unstructured.Unstructured{}
		require.NoError(t, sigsyaml.Unmarshal(widgetCRDYAML, &crd.Object))

		widget := &unstructured.Unstructured{}
		require.NoError(t, sigsyaml.Unmarshal(widgetYAML, &widget.Object))

		results, err := manager.Apply(t.Context(), []*unstructured.Unstructured{crd, widget}, ssa.ApplyOptions{})
		require.NoError(t, err)
		require.Len(t, results, 2)

		resultsBySubject := map[string]ssa.Change{}
		for _, r := range results {
			resultsBySubject[r.Subject] = r
		}

		assert.Equal(t, ssa.CreatedAction, resultsBySubject["CustomResourceDefinition/widgets.stable.example.com"].Action)
		assert.Equal(t, ssa.CreatedAction, resultsBySubject["Widget/my-shiny-widget"].Action)

		appliedCRD := rm.GetObject("apiextensions.k8s.io", "CustomResourceDefinition", "", "widgets.stable.example.com")
		require.NotNil(t, appliedCRD)

		appliedWidget := rm.GetObject("stable.example.com", "Widget", "", "my-shiny-widget")
		require.NotNil(t, appliedWidget)
	})

	t.Run("no_prune_option", func(t *testing.T) {
		rm := resourcemanager.NewMock()
		inv := memory.NewInventory("test-inventory")
		manager := ssa.NewCustomManager(rm, testInventoryClosure(t.Context(), inv), nil, &mapperMock{})

		pruneObj := getConfigmapManifest("old-cm")
		setExistingObjects(t, rm, inv, pruneObj)

		results, err := manager.Apply(t.Context(), []*unstructured.Unstructured{}, ssa.ApplyOptions{
			NoPrune: true,
		})
		require.NoError(t, err)

		require.Len(t, results, 0)

		invContents := inv.Get()
		require.Len(t, invContents, 1, "object should remain in inventory")
	})
}

func getConfigmapManifest(name string) *unstructured.Unstructured {
	obj := &unstructured.Unstructured{
		Object: map[string]any{
			"apiVersion": "v1",
			"kind":       "ConfigMap",
			"metadata": map[string]any{
				"name":      name,
				"namespace": "default",
			},
			"data": map[string]any{
				"key": "value",
			},
		},
	}

	return obj
}
