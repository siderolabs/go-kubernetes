// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at http://mozilla.org/MPL/2.0/.

// Package ssa provides a high-level interface for performing server-side apply operations with inventory management.
package ssa

import (
	"context"
	"net/http"

	"github.com/fluxcd/cli-utils/pkg/kstatus/polling"
	"github.com/fluxcd/cli-utils/pkg/object"
	"github.com/fluxcd/pkg/ssa"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/client-go/discovery"
	"k8s.io/client-go/discovery/cached/memory"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/restmapper"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

type InventoryBackedManager interface {
	// Apply server-side applies objects, prunes stale inventory entries, and returns the resulting changes.
	Apply(ctx context.Context, objects []*unstructured.Unstructured, ops ApplyOptions) ([]Change, error)
	// Destroy deletes all objects tracked in the inventory and removes the inventory itself.
	Destroy(ctx context.Context) error
	// Diff performs a dry-run server-side apply and returns per-object diffs against the cluster state.
	Diff(ctx context.Context, objects []*unstructured.Unstructured, ops DiffOptions) ([]DiffResult, error)
	// Wait blocks until all objects in the set reach a ready state.
	Wait(ctx context.Context, set object.ObjMetadataSet, opts ssa.WaitOptions) error
}

// InventoryFactory creates inventory objects.
type InventoryFactory func(ctx context.Context) (Inventory, error)

// Manager is the default Manager implementation.
type Manager struct {
	resourceManager  ResourceManager
	inventoryFactory InventoryFactory
	httpClient       *http.Client
}

// inventory returns the inventory object for the manager.
func (m *Manager) inventory(ctx context.Context) (Inventory, error) {
	return m.inventoryFactory(ctx)
}

// ResourceManager performs SSA related tasks.
type ResourceManager interface {
	Apply(ctx context.Context, object *unstructured.Unstructured, opts ssa.ApplyOptions) (*ssa.ChangeSetEntry, error)
	ApplyAllStaged(ctx context.Context, objects []*unstructured.Unstructured, opts ssa.ApplyOptions) (*ssa.ChangeSet, error)

	Delete(ctx context.Context, object *unstructured.Unstructured, opts ssa.DeleteOptions) (*ssa.ChangeSetEntry, error)
	Diff(ctx context.Context, object *unstructured.Unstructured, opts ssa.DiffOptions) (
		changeSet *ssa.ChangeSetEntry,
		inClusterObj *unstructured.Unstructured,
		dryRunObject *unstructured.Unstructured,
		err error)
	WaitForSetWithContext(ctx context.Context, set object.ObjMetadataSet, opts ssa.WaitOptions) error
	Get(ctx context.Context, objMeta object.ObjMetadata) (*unstructured.Unstructured, error)
}

// NewCustomManager creates a new manager with specified resource manager and inventory.
func NewCustomManager(resourceManager ResourceManager, inventoryFactory InventoryFactory, httpClient *http.Client) *Manager {
	return &Manager{
		resourceManager:  resourceManager,
		inventoryFactory: inventoryFactory,
		httpClient:       httpClient,
	}
}

// NewManager creates a new ssa manager with default backing resource manager (fluxcd/ssa) and inventory (ConfigMap).
func NewManager(ctx context.Context, kubeconfig *rest.Config, fieldManagerName, inventoryNamespace, inventoryName string) (*Manager, error) {
	httpClient, err := rest.HTTPClientFor(kubeconfig)
	if err != nil {
		return nil, err
	}

	dc, err := discovery.NewDiscoveryClientForConfigAndClient(kubeconfig, httpClient)
	if err != nil {
		return nil, err
	}

	mapper := restmapper.NewDeferredDiscoveryRESTMapper(memory.NewMemCacheClient(dc))

	kubeClient, err := client.New(kubeconfig, client.Options{
		HTTPClient: httpClient,
		Mapper:     mapper,
	})
	if err != nil {
		return nil, err
	}

	poller := polling.NewStatusPoller(kubeClient, mapper, polling.Options{})

	k8sClient, err := kubernetes.NewForConfigAndClient(kubeconfig, httpClient)
	if err != nil {
		return nil, err
	}

	inventoryFactory := func(ctx context.Context) (Inventory, error) {
		return GetInventory(ctx, k8sClient, inventoryNamespace, inventoryName)
	}

	resourceManager := &resourceManagerWithGet{
		ResourceManager: *ssa.NewResourceManager(kubeClient, poller, ssa.Owner{
			Field: fieldManagerName,
		}),
		kubeClient: kubeClient,
	}

	return NewCustomManager(resourceManager, inventoryFactory, httpClient), nil
}

// Close performs any necessary cleanup, such as closing the HTTP connections.
func (m *Manager) Close() {
	if m.httpClient != nil {
		m.httpClient.CloseIdleConnections()
	}
}

type resourceManagerWithGet struct {
	kubeClient client.Client

	ssa.ResourceManager
}

func (r *resourceManagerWithGet) Get(ctx context.Context, objMeta object.ObjMetadata) (*unstructured.Unstructured, error) {
	obj := &unstructured.Unstructured{}

	err := r.kubeClient.Get(ctx, client.ObjectKey{
		Namespace: objMeta.Namespace,
		Name:      objMeta.Name,
	}, obj)
	if err != nil {
		return nil, err
	}

	return obj, nil
}
