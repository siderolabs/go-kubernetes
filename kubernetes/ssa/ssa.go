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
	"k8s.io/apimachinery/pkg/api/meta"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/cli-runtime/pkg/resource"
	"k8s.io/client-go/discovery"
	"k8s.io/client-go/discovery/cached/memory"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/kubernetes"
	openapiclient "k8s.io/client-go/openapi"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/restmapper"
	"k8s.io/client-go/tools/clientcmd"
	"k8s.io/kubectl/pkg/util/openapi"
	"k8s.io/kubectl/pkg/validation"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/siderolabs/go-kubernetes/kubernetes/ssa/internal/inventory/configmap"
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

// Manager is the default Manager implementation.
type Manager struct {
	resourceManager ResourceManager
	inventory       Inventory
	httpClient      *http.Client
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
}

// NewCustomManager creates a new manager with specified resource manager and inventory.
func NewCustomManager(resourceManager ResourceManager, inventory Inventory, httpClient *http.Client) *Manager {
	return &Manager{
		resourceManager: resourceManager,
		inventory:       inventory,
		httpClient:      httpClient,
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

	resourceManager := ssa.NewResourceManager(kubeClient, poller, ssa.Owner{
		Field: fieldManagerName,
	})

	dynamicClient, err := dynamic.NewForConfigAndClient(kubeconfig, httpClient)
	if err != nil {
		return nil, err
	}

	err = configmap.AssureInventoryNamespace(ctx, inventoryNamespace, dynamicClient)
	if err != nil {
		return nil, err
	}

	factory := &factoryMock{
		dynamicClient: dynamicClient,
		mapper:        mapper,
	}

	inventory, err := configmap.NewInventory(ctx, inventoryNamespace, inventoryName, factory)
	if err != nil {
		return nil, err
	}

	return NewCustomManager(resourceManager, inventory, httpClient), nil
}

// Close performs any necessary cleanup, such as closing the HTTP connections.
func (m *Manager) Close() {
	if m.httpClient != nil {
		m.httpClient.CloseIdleConnections()
	}
}

// factoryMock is a minimal implementation of kubeutil.Factory interface.
//
// It implements enough to satisfy the needs of the inventory implementation.
// We do this to ensure that all clients created are using same HTTP client.
type factoryMock struct {
	dynamicClient dynamic.Interface
	mapper        meta.RESTMapper
}

func (mock *factoryMock) ToRESTConfig() (*rest.Config, error) {
	panic("not implemented")
}

func (mock *factoryMock) ToDiscoveryClient() (discovery.CachedDiscoveryInterface, error) {
	panic("not implemented")
}

func (mock *factoryMock) ToRESTMapper() (meta.RESTMapper, error) {
	return mock.mapper, nil
}

func (mock *factoryMock) ToRawKubeConfigLoader() clientcmd.ClientConfig {
	panic("not implemented")
}

func (mock *factoryMock) DynamicClient() (dynamic.Interface, error) {
	return mock.dynamicClient, nil
}

func (mock *factoryMock) KubernetesClientSet() (*kubernetes.Clientset, error) {
	panic("not implemented")
}

func (mock *factoryMock) RESTClient() (*rest.RESTClient, error) {
	panic("not implemented")
}

func (mock *factoryMock) NewBuilder() *resource.Builder {
	panic("not implemented")
}

func (mock *factoryMock) ClientForMapping(mapping *meta.RESTMapping) (resource.RESTClient, error) {
	panic("not implemented")
}

func (mock *factoryMock) UnstructuredClientForMapping(mapping *meta.RESTMapping) (resource.RESTClient, error) {
	panic("not implemented")
}

func (mock *factoryMock) Validator(validationDirective string) (validation.Schema, error) {
	panic("not implemented")
}

func (mock *factoryMock) OpenAPIResourcesGetter() (openapi.OpenAPIResourcesGetter, error) {
	panic("not implemented")
}

func (mock *factoryMock) OpenAPISchema() (openapi.Resources, error) {
	panic("not implemented")
}

func (mock *factoryMock) OpenAPIV3Client() (openapiclient.Client, error) {
	panic("not implemented")
}
