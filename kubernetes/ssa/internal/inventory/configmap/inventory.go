// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at http://mozilla.org/MPL/2.0/.

// Package configmap implements a ConfigMap-based inventory for server-side apply.
package configmap

import (
	"context"
	"fmt"
	"slices"

	v1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"

	"github.com/siderolabs/go-kubernetes/kubernetes/ssa/object"
)

// Inventory is a ConfigMap-based implementation of the Inventory interface.
type Inventory struct {
	client *kubernetes.Clientset

	name      string
	namespace string

	contents object.ObjMetadataSet
}

// NewInventory creates a new ConfigMap-based inventory.
//
// If the inventory doesn't exist yet, it will be created.
// If it already exists, it will be fetched and returned.
func NewInventory(ctx context.Context, k8sClient *kubernetes.Clientset, namespace, name string) (*Inventory, error) {
	i := &Inventory{
		client: k8sClient,

		name:      name,
		namespace: namespace,
	}

	configmap, err := i.client.CoreV1().ConfigMaps(namespace).Get(ctx, name, metav1.GetOptions{})
	if err != nil && !apierrors.IsNotFound(err) {
		return nil, fmt.Errorf("failed to fetch the inventory configmap: %w", err)
	}

	if apierrors.IsNotFound(err) {
		configmap = &v1.ConfigMap{
			ObjectMeta: metav1.ObjectMeta{
				Name:      name,
				Namespace: namespace,
			},
		}

		_, err = i.client.CoreV1().ConfigMaps(namespace).Create(ctx, configmap, metav1.CreateOptions{})
		if err != nil {
			return nil, fmt.Errorf("failed to create the inventory configmap: %w", err)
		}
	}

	for objKey := range configmap.Data {
		objMetadata, err := object.ParseObjMetadata(objKey)
		if err != nil {
			return nil, fmt.Errorf("failed to parse object metadata: %w", err)
		}

		i.contents = append(i.contents, objMetadata)
	}

	return i, nil
}

func (i *Inventory) ID() string {
	// ID for the configmap inventory is just the name to maintain compatibility with default kubectl behavior.
	return i.name
}

// Get returns the list of object references tracked in the inventory.
func (i *Inventory) Get() object.ObjMetadataSet {
	return slices.Clone(i.contents)
}

// Update updates the inventory with the given set of object references.
func (i *Inventory) Update(objectRefs object.ObjMetadataSet) {
	i.contents = slices.Clone(objectRefs)
}

func (i *Inventory) Write(ctx context.Context) error {
	configmap := &v1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{
			Name:      i.name,
			Namespace: i.namespace,
		},
		Data: make(map[string]string, len(i.contents)),
	}

	for _, objMetadata := range i.contents {
		configmap.Data[objMetadata.String()] = ""
	}

	_, err := i.client.CoreV1().ConfigMaps(i.namespace).Update(ctx, configmap, metav1.UpdateOptions{})
	if err != nil {
		return fmt.Errorf("failed to update the inventory: %w", err)
	}

	return nil
}

// Delete removes the inventory from the cluster.
func (i *Inventory) Delete(ctx context.Context) error {
	err := i.client.CoreV1().ConfigMaps(i.namespace).Delete(ctx, i.name, metav1.DeleteOptions{})
	if err != nil && !apierrors.IsNotFound(err) {
		return fmt.Errorf("failed to delete the inventory: %w", err)
	}

	return nil
}

func AssureInventoryNamespace(ctx context.Context, k8sClient *kubernetes.Clientset, inventoryNamespace string) error {
	_, err := k8sClient.CoreV1().Namespaces().Get(ctx, inventoryNamespace, metav1.GetOptions{})
	if err != nil && !apierrors.IsNotFound(err) {
		return err
	}

	if err == nil {
		return nil
	}

	ns := &v1.Namespace{
		ObjectMeta: metav1.ObjectMeta{
			Name: inventoryNamespace,
		},
	}

	_, err = k8sClient.CoreV1().Namespaces().Create(ctx, ns, metav1.CreateOptions{})
	if err != nil && !apierrors.IsAlreadyExists(err) {
		return fmt.Errorf("failed to create inventory namespace: %w", err)
	}

	return nil
}
