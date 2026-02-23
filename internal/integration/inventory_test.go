// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at http://mozilla.org/MPL/2.0/.

//go:build integration

package integration_test

import (
	"context"
	"fmt"
	"math/rand/v2"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/tools/clientcmd"

	"github.com/siderolabs/go-kubernetes/kubernetes/ssa"
	"github.com/siderolabs/go-kubernetes/kubernetes/ssa/object"
)

func getKubernetesClient(t *testing.T) *kubernetes.Clientset {
	t.Helper()

	loadingRules := clientcmd.NewDefaultClientConfigLoadingRules()

	config, err := clientcmd.NewNonInteractiveDeferredLoadingClientConfig(loadingRules, nil).ClientConfig()
	if err != nil {
		t.Skip("skipping test since Kubernetes client configuration is not available")
	}

	k8sClient, err := kubernetes.NewForConfig(config)
	require.NoError(t, err)

	return k8sClient
}

func getTestNamespace(t *testing.T, client *kubernetes.Clientset) string {
	t.Helper()

	namespace := fmt.Sprintf("inventory-%04x", rand.UintN(65536))

	t.Cleanup(func() {
		require.NoError(t, client.CoreV1().Namespaces().Delete(context.Background(), namespace, metav1.DeleteOptions{}))
	})

	return namespace
}

const inventoryName = "test-inventory"

func TestInventoryLifecycle(t *testing.T) {
	k8sClient := getKubernetesClient(t)
	namespace := getTestNamespace(t, k8sClient)

	inventory, err := ssa.GetInventory(t.Context(), k8sClient, namespace, inventoryName)
	require.NoError(t, err)

	require.Equal(t, inventoryName, inventory.ID())
	require.Empty(t, inventory.Get())

	objects := object.ObjMetadataSet{
		{Namespace: "default", Name: "object-1", GroupKind: schema.GroupKind{Group: "apps", Kind: "Deployment"}},
		{Namespace: "default", Name: "object-2", GroupKind: schema.GroupKind{Group: "", Kind: "Service"}},
	}

	inventory.Update(objects)
	assert.Equal(t, objects, inventory.Get())

	require.NoError(t, inventory.Write(t.Context()))

	inventory, err = ssa.GetInventory(t.Context(), k8sClient, namespace, inventoryName)
	require.NoError(t, err)

	assert.ElementsMatch(t, objects, inventory.Get())

	require.NoError(t, inventory.Delete(t.Context()))
}

func TestInventoryConcurrentUpdate(t *testing.T) {
	k8sClient := getKubernetesClient(t)
	namespace := getTestNamespace(t, k8sClient)

	inv1, err := ssa.GetInventory(t.Context(), k8sClient, namespace, inventoryName)
	require.NoError(t, err)

	inv2, err := ssa.GetInventory(t.Context(), k8sClient, namespace, inventoryName)
	require.NoError(t, err)

	objects1 := object.ObjMetadataSet{
		{Namespace: "default", Name: "object-1", GroupKind: schema.GroupKind{Group: "apps", Kind: "Deployment"}},
	}

	objects2 := object.ObjMetadataSet{
		{Namespace: "default", Name: "object-2", GroupKind: schema.GroupKind{Group: "", Kind: "Service"}},
	}

	inv1.Update(objects1)
	require.NoError(t, inv1.Write(t.Context()))

	inv2.Update(objects2)
	require.NoError(t, inv2.Write(t.Context()))

	inv, err := ssa.GetInventory(t.Context(), k8sClient, namespace, inventoryName)
	require.NoError(t, err)

	assert.ElementsMatch(t, objects2, inv.Get())
}
