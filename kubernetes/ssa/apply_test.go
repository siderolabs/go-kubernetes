// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at http://mozilla.org/MPL/2.0/.

// nolint: contextcheck,godoclint
package ssa_test

import (
	"context"
	_ "embed"
	"errors"
	"sync/atomic"
	"testing"

	fluxssa "github.com/fluxcd/pkg/ssa"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	sigsyaml "sigs.k8s.io/yaml"

	"github.com/siderolabs/go-kubernetes/kubernetes/ssa"
	"github.com/siderolabs/go-kubernetes/kubernetes/ssa/internal/inventory/memory"
	"github.com/siderolabs/go-kubernetes/kubernetes/ssa/internal/resourcemanager"
	"github.com/siderolabs/go-kubernetes/kubernetes/ssa/object"
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
	require.ErrorContains(t, err, "apply failed", "the manager should return the error from the resourceManager apply")

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

// transientFailResourceManager fails ApplyAllStaged with a retryable internal error
// for the first N calls, then delegates to the embedded Mock.
type transientFailResourceManager struct {
	resourcemanager.Mock
	remaining atomic.Int32
}

func (m *transientFailResourceManager) ApplyAllStaged(ctx context.Context, objects []*unstructured.Unstructured, opts fluxssa.ApplyOptions) (*fluxssa.ChangeSet, error) {
	if m.remaining.Add(-1) >= 0 {
		// Apply the first object successfully, then return a retryable error.
		cs := fluxssa.NewChangeSet()

		entry, err := m.Apply(ctx, objects[0], opts)
		if err != nil {
			return cs, err
		}

		cs.Add(*entry)

		return cs, apierrors.NewInternalError(errors.New("transient API server error"))
	}

	return m.Mock.ApplyAllStaged(ctx, objects, opts)
}

func TestApplyRetry(t *testing.T) {
	// First attempt: cm-1 applied successfully, cm-2 fails with conflict.
	// Second attempt: both succeed. Verify results are correct and not duplicated.
	rm := &transientFailResourceManager{}
	rm.remaining.Store(1)

	inv := memory.NewInventory("test-inventory")
	manager := ssa.NewCustomManager(rm, testInventoryClosure(t.Context(), inv), nil, &mapperMock{})

	obj1 := getConfigmapManifest("cm-1")
	obj2 := getConfigmapManifest("cm-2")

	results, err := manager.Apply(t.Context(), []*unstructured.Unstructured{obj1, obj2}, ssa.ApplyOptions{})
	require.NoError(t, err)
	require.Len(t, results, 2, "each object should appear exactly once in results")

	resultsByName := map[string]ssa.Change{}
	for _, r := range results {
		resultsByName[r.ObjMetadata.Name] = r
	}

	assert.Equal(t, ssa.CreatedAction, resultsByName["cm-1"].Action)
	assert.Equal(t, ssa.CreatedAction, resultsByName["cm-2"].Action)

	invContents := inv.Get()
	require.Len(t, invContents, 2, "both objects should be in inventory after successful retry")
}

// versionRewritingResourceManager wraps Mock and rewrites the GroupVersion field on every
// returned ChangeSetEntry. This simulates a Kubernetes API server returning an apply
// response with a different apiVersion than the request — the situation that arises when
// a CRD has multiple stored versions or a conversion webhook configured, and was the cause
// of the panic reported in siderolabs/talos#13254.
type versionRewritingResourceManager struct {
	resourcemanager.Mock
	rewriteVersion string
}

func (m *versionRewritingResourceManager) ApplyAllStaged(ctx context.Context, objects []*unstructured.Unstructured, opts fluxssa.ApplyOptions) (*fluxssa.ChangeSet, error) {
	cs, err := m.Mock.ApplyAllStaged(ctx, objects, opts)
	if cs != nil {
		for i := range cs.Entries {
			cs.Entries[i].GroupVersion = m.rewriteVersion
		}
	}

	return cs, err
}

func TestApplyVersionMismatch(t *testing.T) {
	// Regression test for siderolabs/talos#13254. Before the fix the change map was
	// keyed by a path string that included the apiVersion: the insert used the input
	// object's version, but the lookup used the version reported by the apply result.
	// When those differed (e.g. a CR whose dry-run response is normalized to the CRD's
	// storage version), the lookup returned nil and writing to .Action/.Diff segfaulted.
	rm := &versionRewritingResourceManager{rewriteVersion: "v1beta1"}
	inv := memory.NewInventory("test-inventory")
	manager := ssa.NewCustomManager(rm, testInventoryClosure(t.Context(), inv), nil, &mapperMock{})

	// Input object has apiVersion v1; the resource manager reports GroupVersion=v1beta1.
	obj := getConfigmapManifest("version-flip-cm")

	results, err := manager.Apply(t.Context(), []*unstructured.Unstructured{obj}, ssa.ApplyOptions{})
	require.NoError(t, err)
	require.Len(t, results, 1)

	// Both Action and Diff must be set on the change, even though the apply result
	// carried a different apiVersion than the input object.
	assert.Equal(t, ssa.CreatedAction, results[0].Action,
		"Action must be set when apply result has a different apiVersion than the input")
	assert.NotEmpty(t, results[0].Diff,
		"Diff must be set when apply result has a different apiVersion than the input")
	assert.Equal(t, "version-flip-cm", results[0].ObjMetadata.Name)
	assert.Equal(t, "ConfigMap", results[0].ObjMetadata.GroupKind.Kind)

	// The object should also be tracked in the inventory.
	invContents := inv.Get()
	require.Len(t, invContents, 1)
	assert.Equal(t, "version-flip-cm", invContents[0].Name)
}

// extraEntryResourceManager wraps Mock and appends an extra ChangeSetEntry to the
// returned ChangeSet that doesn't correspond to any input object. This simulates a
// resource manager reporting an apply for an object whose ObjMetadata isn't tracked
// by the change map.
type extraEntryResourceManager struct {
	resourcemanager.Mock
	extra fluxssa.ChangeSetEntry
}

func (m *extraEntryResourceManager) ApplyAllStaged(ctx context.Context, objects []*unstructured.Unstructured, opts fluxssa.ApplyOptions) (*fluxssa.ChangeSet, error) {
	cs, err := m.Mock.ApplyAllStaged(ctx, objects, opts)
	if cs != nil {
		cs.Add(m.extra)
	}

	return cs, err
}

func TestApplyUnexpectedEntry(t *testing.T) {
	// When the resourceManager reports a result for an object outside the input set,
	// Apply should surface it as an error and still record the change instead of
	// silently dropping it.
	rm := &extraEntryResourceManager{
		extra: fluxssa.ChangeSetEntry{
			ObjMetadata: object.ObjMetadata{
				Namespace: "default",
				Name:      "ghost-cm",
				GroupKind: schema.GroupKind{Kind: "ConfigMap"},
			},
			GroupVersion: "v1",
			Subject:      "ConfigMap/default/ghost-cm",
			Action:       fluxssa.CreatedAction,
		},
	}
	inv := memory.NewInventory("test-inventory")
	manager := ssa.NewCustomManager(rm, testInventoryClosure(t.Context(), inv), nil, &mapperMock{})

	obj := getConfigmapManifest("real-cm")

	results, err := manager.Apply(t.Context(), []*unstructured.Unstructured{obj}, ssa.ApplyOptions{})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "unexpected object")
	assert.Contains(t, err.Error(), "ConfigMap/default/ghost-cm")

	// Both the real object and the unexpected ghost entry should appear in results
	// so that the caller can observe what the resource manager reported.
	resultsByName := map[string]ssa.Change{}
	for _, r := range results {
		resultsByName[r.ObjMetadata.Name] = r
	}

	require.Len(t, resultsByName, 2)
	assert.Equal(t, ssa.CreatedAction, resultsByName["real-cm"].Action)
	assert.Equal(t, ssa.CreatedAction, resultsByName["ghost-cm"].Action)
	assert.Equal(t, "ConfigMap/default/ghost-cm", resultsByName["ghost-cm"].Subject)
}

// clusterScopedNamespaceStripper simulates the Kubernetes API server normalizing cluster-scoped
// resources: server-side apply responses for such resources carry an empty namespace even when
// the request object had one set. It strips the namespace from CustomResourceDefinition apply
// entries, reproducing the mismatch between the dry-run diff (keyed with the user-set namespace)
// and the apply result (keyed with the empty, server-normalized namespace).
type clusterScopedNamespaceStripper struct {
	resourcemanager.Mock
}

func (m *clusterScopedNamespaceStripper) ApplyAllStaged(ctx context.Context, objects []*unstructured.Unstructured, opts fluxssa.ApplyOptions) (*fluxssa.ChangeSet, error) {
	cs, err := m.Mock.ApplyAllStaged(ctx, objects, opts)
	if cs != nil {
		for i := range cs.Entries {
			if cs.Entries[i].ObjMetadata.GroupKind.Kind == "CustomResourceDefinition" {
				cs.Entries[i].ObjMetadata.Namespace = ""
			}
		}
	}

	return cs, err
}

func TestApplyClusterScopedResourceWithNamespace(t *testing.T) {
	// A cluster-scoped resource (a CRD here) may have a namespace set on it by the user. The API
	// server ignores it, so the server-side apply response carries an empty namespace while the
	// dry-run diff, built from the input object, keeps the user-set one. Apply must still associate
	// the diff with the change and must not prune the resource over the namespace mismatch.
	rm := &clusterScopedNamespaceStripper{Mock: *resourcemanager.NewMock()}
	inv := memory.NewInventory("test-inventory")
	manager := ssa.NewCustomManager(rm, testInventoryClosure(t.Context(), inv), nil, &mapperMock{})

	crd := &unstructured.Unstructured{}
	require.NoError(t, sigsyaml.Unmarshal(widgetCRDYAML, &crd.Object))
	crd.SetNamespace("test-lab")

	results, err := manager.Apply(t.Context(), []*unstructured.Unstructured{crd}, ssa.ApplyOptions{})
	require.NoError(t, err)
	require.Len(t, results, 1)

	// The diff must be associated with the change despite the diff/apply namespace mismatch, and
	// the change must be tracked under the server-normalized empty namespace.
	assert.Equal(t, ssa.CreatedAction, results[0].Action)
	assert.Contains(t, results[0].Diff, "+kind: CustomResourceDefinition")
	assert.Empty(t, results[0].ObjMetadata.Namespace, "cluster-scoped resource should be tracked without a namespace")

	invContents := inv.Get()
	require.Len(t, invContents, 1)
	assert.Empty(t, invContents[0].Namespace, "inventory should track the cluster-scoped resource without a namespace")

	// Re-applying with the namespace still set must match the inventory entry (empty namespace) and
	// must not prune the resource.
	crd2 := &unstructured.Unstructured{}
	require.NoError(t, sigsyaml.Unmarshal(widgetCRDYAML, &crd2.Object))
	crd2.SetNamespace("test-lab")

	results, err = manager.Apply(t.Context(), []*unstructured.Unstructured{crd2}, ssa.ApplyOptions{})
	require.NoError(t, err)
	require.Len(t, results, 1)
	assert.NotEqual(t, ssa.DeletedAction, results[0].Action, "the cluster-scoped resource must not be pruned")

	invContents = inv.Get()
	require.Len(t, invContents, 1, "the cluster-scoped resource must remain tracked, not pruned")
	assert.Empty(t, invContents[0].Namespace)
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
