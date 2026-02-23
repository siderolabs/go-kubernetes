// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at http://mozilla.org/MPL/2.0/.

// nolint: contextcheck,godoclint
package ssa_test

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"

	"github.com/siderolabs/go-kubernetes/kubernetes/ssa"
	"github.com/siderolabs/go-kubernetes/kubernetes/ssa/internal/inventory/memory"
	"github.com/siderolabs/go-kubernetes/kubernetes/ssa/internal/resourcemanager"
	"github.com/siderolabs/go-kubernetes/kubernetes/ssa/object"
)

var updateSnapshots = os.Getenv("TEST_UPDATE_SNAPSHOT") == "true"

func TestManager_Diff(t *testing.T) {
	ctx := context.Background()

	t.Run("CreateAction", func(t *testing.T) {
		rm := resourcemanager.NewMock()
		manager := ssa.NewCustomManager(rm, testInventoryFactory, nil)

		obj := &unstructured.Unstructured{
			Object: map[string]any{
				"apiVersion": "v1",
				"kind":       "ConfigMap",
				"metadata": map[string]any{
					"name":      "test-cm",
					"namespace": "default",
				},
				"data": map[string]any{
					"key": "value",
				},
			},
		}

		results, err := manager.Diff(ctx, []*unstructured.Unstructured{obj}, ssa.DiffOptions{})
		require.NoError(t, err)
		require.Len(t, results, 1)
		assert.Equal(t, ssa.DiffCreatedAction, results[0].Action)
		assert.Contains(t, results[0].Diff, "+  key: value")
		assert.Equal(t, object.ObjMetadata{Namespace: "default", Name: "test-cm", GroupKind: schema.GroupKind{Kind: "ConfigMap"}}, results[0].ObjMetadata)
		assert.Equal(t, "v1", results[0].GroupVersion)
		assert.Equal(t, "ConfigMap/default/test-cm", results[0].Subject)
	})

	t.Run("ModifyAction", func(t *testing.T) {
		rm := resourcemanager.NewMock()
		inv := memory.NewInventory("test-inventory")
		manager := ssa.NewCustomManager(rm, testInventoryClosure(t.Context(), inv), nil)

		existingObj := &unstructured.Unstructured{
			Object: map[string]any{
				"apiVersion": "v1",
				"kind":       "ConfigMap",
				"metadata": map[string]any{
					"name":      "test-cm",
					"namespace": "default",
					"annotations": map[string]any{
						"key":                      "old-value",
						ssa.InventoryAnnotationKey: "other-inventory",
					},
				},
			},
		}

		setExistingObjects(t, rm, inv, existingObj)

		modifiedObj := existingObj.DeepCopy()
		modifiedObj.SetAnnotations(map[string]string{"key": "new-value"})

		// should fail inventory policy validation
		results, err := manager.Diff(ctx, []*unstructured.Unstructured{}, ssa.DiffOptions{InventoryPolicy: ssa.InventoryPolicyMustMatch})
		require.ErrorContains(t, err, "inventory policy check failure")
		require.Len(t, results, 0)

		results, err = manager.Diff(ctx, []*unstructured.Unstructured{modifiedObj}, ssa.DiffOptions{InventoryPolicy: ssa.InventoryPolicyAdoptAll})
		require.NoError(t, err)
		require.Len(t, results, 1)
		assert.Equal(t, ssa.DiffConfiguredAction, results[0].Action)
		assert.Contains(t, results[0].Diff, "-    key: old-value")
		assert.Contains(t, results[0].Diff, "+    key: new-value")
		assert.Equal(t, object.ObjMetadata{Namespace: "default", Name: "test-cm", GroupKind: schema.GroupKind{Kind: "ConfigMap"}}, results[0].ObjMetadata)
		assert.Equal(t, "v1", results[0].GroupVersion)
		assert.Equal(t, "ConfigMap/default/test-cm", results[0].Subject)
	})

	t.Run("Unchanged", func(t *testing.T) {
		rm := resourcemanager.NewMock()
		inv := memory.NewInventory("test-inventory")
		manager := ssa.NewCustomManager(rm, testInventoryClosure(t.Context(), inv), nil)

		obj := &unstructured.Unstructured{
			Object: map[string]any{
				"apiVersion": "v1",
				"kind":       "ConfigMap",
				"metadata": map[string]any{
					"name":      "test-cm",
					"namespace": "default",
					"annotations": map[string]any{
						// an existing object would have this annotation set if it was applied with this library
						ssa.InventoryAnnotationKey: "test-inventory",
					},
				},
				"data": map[string]any{
					"key": "value",
				},
			},
		}

		setExistingObjects(t, rm, inv, obj)

		results, err := manager.Diff(ctx, []*unstructured.Unstructured{obj}, ssa.DiffOptions{InventoryPolicy: ssa.InventoryPolicyAdoptIfNoInventory})
		require.NoError(t, err)
		require.Len(t, results, 1)
		assert.Equal(t, ssa.DiffUnchangedAction, results[0].Action)
		assert.Equal(t, "", results[0].Diff, "diff should be empty for unchanged results")
		assert.Equal(t, object.ObjMetadata{Namespace: "default", Name: "test-cm", GroupKind: schema.GroupKind{Kind: "ConfigMap"}}, results[0].ObjMetadata)
		assert.Equal(t, "v1", results[0].GroupVersion)
		assert.Equal(t, "ConfigMap/default/test-cm", results[0].Subject)
	})

	t.Run("PruneAction", func(t *testing.T) {
		rm := resourcemanager.NewMock()
		inv := memory.NewInventory("test-inventory")
		manager := ssa.NewCustomManager(rm, testInventoryClosure(t.Context(), inv), nil)

		// Object in inventory but not in applied objects
		pruneObj := &unstructured.Unstructured{
			Object: map[string]any{
				"apiVersion": "v1",
				"kind":       "ConfigMap",
				"metadata": map[string]any{
					"name":      "prune-cm",
					"namespace": "default",
				},
			},
		}

		setExistingObjects(t, rm, inv, pruneObj)

		// should fail inventory policy validation
		results, err := manager.Diff(ctx, []*unstructured.Unstructured{}, ssa.DiffOptions{InventoryPolicy: ssa.InventoryPolicyMustMatch})
		require.ErrorContains(t, err, "inventory policy check failure")
		require.Len(t, results, 0)

		results, err = manager.Diff(ctx, []*unstructured.Unstructured{}, ssa.DiffOptions{InventoryPolicy: ssa.InventoryPolicyAdoptIfNoInventory})
		require.NoError(t, err)

		require.Len(t, results, 1)
		assert.Equal(t, ssa.DiffPrunedAction, results[0].Action)
		assert.Equal(t, object.ObjMetadata{Namespace: "default", Name: "prune-cm", GroupKind: schema.GroupKind{Kind: "ConfigMap"}}, results[0].ObjMetadata)
		assert.Equal(t, "ConfigMap/default/prune-cm", results[0].Subject)
	})

	t.Run("NoPrune_option", func(t *testing.T) {
		rm := resourcemanager.NewMock()
		inv := memory.NewInventory("test-inventory")
		manager := ssa.NewCustomManager(rm, testInventoryClosure(t.Context(), inv), nil)

		pruneObj := &unstructured.Unstructured{
			Object: map[string]any{
				"apiVersion": "v1",
				"kind":       "ConfigMap",
				"metadata": map[string]any{
					"name":      "prune-cm",
					"namespace": "default",
				},
			},
		}

		setExistingObjects(t, rm, inv, pruneObj)

		results, err := manager.Diff(ctx, []*unstructured.Unstructured{}, ssa.DiffOptions{NoPrune: true, InventoryPolicy: ssa.InventoryPolicyAdoptIfNoInventory})
		require.NoError(t, err)
		require.Len(t, results, 0)
	})

	t.Run("Diff_Render_Snapshot", func(t *testing.T) {
		rm := resourcemanager.NewMock()
		inv := memory.NewInventory("test-inventory")
		manager := ssa.NewCustomManager(rm, testInventoryClosure(t.Context(), inv), nil)

		inputObject := &unstructured.Unstructured{
			Object: map[string]any{
				"apiVersion": "v1",
				"kind":       "ConfigMap",
				"metadata": map[string]any{
					"name":      "test-configmap",
					"namespace": "default",
				},
				"data": map[string]any{
					"key1": "value1",
					"key2": "value2",
					"key3": "value3",
				},
			},
		}

		existingObj := inputObject.DeepCopy()
		existingObj.SetAnnotations(map[string]string{
			// an existing object would have this annotation set if it was applied with this library
			ssa.InventoryAnnotationKey: "test-inventory",
		})

		existingToBeDeletedObj := existingObj.DeepCopy()
		existingToBeDeletedObj.SetName("i-was-pruned-configmap")

		setExistingObjects(t, rm, inv, existingObj, existingToBeDeletedObj)

		modifiedObj := inputObject.DeepCopy()
		modifiedObj.Object["data"] = map[string]any{
			"key1": "value1",     // Unchanged
			"key2": "new-value2", // Changed
			// key3 removed
			"key4": "value4", // Added
		}

		newObj := &unstructured.Unstructured{
			Object: map[string]any{
				"apiVersion": "v1",
				"kind":       "Namespace",
				"metadata": map[string]any{
					"name": "foo",
				},
			},
		}

		results, err := manager.Diff(ctx, []*unstructured.Unstructured{modifiedObj, newObj}, ssa.DiffOptions{InventoryPolicy: ssa.InventoryPolicyAdoptIfNoInventory})
		require.NoError(t, err)
		require.Len(t, results, 3)

		assert.Equal(t, ssa.DiffPrunedAction, results[0].Action)
		assert.Equal(t, object.ObjMetadata{Namespace: "default", Name: "i-was-pruned-configmap", GroupKind: schema.GroupKind{Kind: "ConfigMap"}}, results[0].ObjMetadata)
		assert.Equal(t, "v1", results[0].GroupVersion)
		assert.Equal(t, "ConfigMap/default/i-was-pruned-configmap", results[0].Subject)

		assert.Equal(t, ssa.DiffConfiguredAction, results[1].Action)
		assert.Equal(t, object.ObjMetadata{Namespace: "default", Name: "test-configmap", GroupKind: schema.GroupKind{Kind: "ConfigMap"}}, results[1].ObjMetadata)
		assert.Equal(t, "v1", results[1].GroupVersion)
		assert.Equal(t, "ConfigMap/default/test-configmap", results[1].Subject)

		assert.Equal(t, ssa.DiffCreatedAction, results[2].Action)
		assert.Equal(t, object.ObjMetadata{Namespace: "", Name: "foo", GroupKind: schema.GroupKind{Kind: "Namespace"}}, results[2].ObjMetadata)
		assert.Equal(t, "v1", results[2].GroupVersion)
		assert.Equal(t, "Namespace/foo", results[2].Subject)

		assertGoldenFile(t, results[0].Diff, "diff_deleted_snapshot.golden")
		assertGoldenFile(t, results[1].Diff, "diff_configured_snapshot.golden")
		assertGoldenFile(t, results[2].Diff, "diff_created_snapshot.golden")
	})
}

func setExistingObjects(t *testing.T, rm resourcemanager.MockResourceManager, inv *memory.Inventory, existingObjs ...*unstructured.Unstructured) {
	rm.SetObjects(existingObjs...)

	metadataSet := object.ObjMetadataSet{}

	for _, o := range existingObjs {
		metadataSet = append(metadataSet, getObjectMetadataSet(o)...)
	}

	inv.Update(metadataSet)
	err := inv.Write(t.Context())
	require.NoError(t, err)
}

func getObjectMetadataSet(pruneObj *unstructured.Unstructured) object.ObjMetadataSet {
	meta := object.ObjMetadata{
		Namespace: pruneObj.GetNamespace(),
		Name:      pruneObj.GetName(),
		GroupKind: pruneObj.GroupVersionKind().GroupKind(),
	}

	metaSet := object.ObjMetadataSet{meta}

	return metaSet
}

func assertGoldenFile(t *testing.T, actual string, filename string) {
	t.Helper()

	goldenPath := filepath.Join("testdata", filename)

	if updateSnapshots {
		err := os.MkdirAll("testdata", 0o755)
		require.NoError(t, err)
		err = os.WriteFile(goldenPath, []byte(actual), 0o644)
		require.NoError(t, err)
	}

	expected, err := os.ReadFile(goldenPath)
	if err != nil {
		if os.IsNotExist(err) {
			t.Fatalf("golden file %s does not exist. run test with UPDATE=true to create it", goldenPath)
		}

		require.NoError(t, err)
	}

	assert.Equal(t, string(expected), actual)
}
