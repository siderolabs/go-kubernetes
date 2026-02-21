// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at http://mozilla.org/MPL/2.0/.

// Package configmap implements a ConfigMap-based inventory for server-side apply.
package configmap

import (
	"context"
	"fmt"

	v1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/dynamic"
	kubeutil "k8s.io/kubectl/pkg/cmd/util"
	"sigs.k8s.io/cli-utils/pkg/apply/prune"
	"sigs.k8s.io/cli-utils/pkg/inventory"
	"sigs.k8s.io/cli-utils/pkg/object"
)

// Inventory is a ConfigMap-based implementation of the Inventory interface.
type Inventory struct {
	info      *inventory.SingleObjectInfo
	inv       inventory.Inventory
	client    inventory.Client
	pruner    prune.Pruner
	name      string
	namespace string
}

// NewInventory creates a new ConfigMap-based inventory.
//
// If the inventory doesn't exist yet, it will be created.
// If it already exists, it will be fetched and returned.
func NewInventory(ctx context.Context, namespace, name string, factory kubeutil.Factory) (*Inventory, error) {
	i := &Inventory{
		name:      name,
		namespace: namespace,
	}

	inventoryInfo := inventory.NewSingleObjectInfo(inventory.ID(i.ID()), types.NamespacedName{Namespace: namespace, Name: name})

	i.info = inventoryInfo

	inventoryClient, err := inventory.ConfigMapClientFactory{StatusEnabled: true}.NewClient(factory)
	if err != nil {
		return nil, err
	}

	err = AssureInventory(ctx, inventoryClient, inventoryInfo)
	if err != nil {
		return nil, err
	}

	dynamicClient, err := factory.DynamicClient()
	if err != nil {
		return nil, err
	}

	mapper, err := factory.ToRESTMapper()
	if err != nil {
		return nil, err
	}

	inventoryPruner := prune.Pruner{
		InvClient: inventoryClient,
		Client:    dynamicClient,
		Mapper:    mapper,
	}

	i.client = inventoryClient
	i.pruner = inventoryPruner

	inv, err := i.client.Get(ctx, inventory.NewSingleObjectInfo(inventory.ID(name), types.NamespacedName{Namespace: namespace, Name: name}), inventory.GetOptions{})
	if err != nil {
		return nil, fmt.Errorf("failed to fetch the inventory: %w", err)
	}

	i.inv = inv

	return i, nil
}

func (i *Inventory) ID() string {
	// ID for the configmap inventory is just the name to maintain compatibility with default kubectl behavior.
	return i.name
}

func (i *Inventory) Read(ctx context.Context) (object.ObjMetadataSet, error) {
	err := AssureInventory(ctx, i.client, i.info)
	if err != nil {
		return nil, err
	}

	inv, err := i.client.Get(ctx, i.inv.Info(), inventory.GetOptions{})
	if err != nil {
		return nil, fmt.Errorf("failed to fetch the inventory: %w", err)
	}

	i.inv = inv

	return i.inv.GetObjectRefs(), nil
}

func (i *Inventory) GetPruneObjs(ctx context.Context, objects object.UnstructuredSet) (object.UnstructuredSet, error) {
	return i.pruner.GetPruneObjs(ctx, i.inv, objects, prune.Options{})
}

func (i *Inventory) Write(ctx context.Context, set object.ObjMetadataSet) error {
	i.inv.SetObjectRefs(set)

	err := i.client.CreateOrUpdate(ctx, i.inv, inventory.UpdateOptions{})
	if err != nil {
		return fmt.Errorf("failed to update the inventory: %w", err)
	}

	return nil
}

func (i *Inventory) Delete(ctx context.Context) error {
	err := i.client.Delete(ctx, i.inv.Info(), inventory.DeleteOptions{})
	if err != nil {
		return fmt.Errorf("failed to delete the inventory: %w", err)
	}

	return nil
}

var namespaceGVR = schema.GroupVersionResource{
	Group:    "", // core API group
	Version:  "v1",
	Resource: "namespaces",
}

func AssureInventoryNamespace(ctx context.Context, inventoryNamespace string, k8sClient *dynamic.DynamicClient) error {
	namespace, getInvErr := k8sClient.Resource(namespaceGVR).Get(ctx, inventoryNamespace, metav1.GetOptions{})
	if getInvErr != nil && !apierrors.IsNotFound(getInvErr) {
		return getInvErr
	}

	if namespace != nil {
		return nil
	}

	ns := &v1.Namespace{
		ObjectMeta: metav1.ObjectMeta{
			Name: inventoryNamespace,
		},
	}

	objMap, err := runtime.DefaultUnstructuredConverter.ToUnstructured(ns)
	if err != nil {
		return fmt.Errorf("failed to convert namespace object to unstructured: %w", err)
	}

	unstructuredNS := &unstructured.Unstructured{Object: objMap}

	_, getInvErr = k8sClient.Resource(namespaceGVR).Create(ctx, unstructuredNS, metav1.CreateOptions{})

	return getInvErr
}

func AssureInventory(ctx context.Context, inventoryClient inventory.Client, inventoryInfo *inventory.SingleObjectInfo) error {
	inv, err := inventoryClient.Get(ctx, inventoryInfo, inventory.GetOptions{})
	if err != nil && !apierrors.IsNotFound(err) {
		return err
	}

	if inv != nil {
		return nil
	}

	inv, err = inventoryClient.NewInventory(inventoryInfo)
	if err != nil {
		return err
	}

	return inventoryClient.CreateOrUpdate(ctx, inv, inventory.UpdateOptions{})
}
