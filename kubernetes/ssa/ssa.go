// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at http://mozilla.org/MPL/2.0/.

// Package ssa provides a high-level interface for performing server-side apply operations with inventory management.
package ssa

import (
	"context"

	"github.com/fluxcd/cli-utils/pkg/kstatus/polling"
	"github.com/fluxcd/cli-utils/pkg/object"
	"github.com/fluxcd/pkg/ssa"
	"k8s.io/apimachinery/pkg/api/meta"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/client-go/discovery"
	"k8s.io/client-go/discovery/cached/memory"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/restmapper"
	"k8s.io/client-go/tools/clientcmd"
	kubeutil "k8s.io/kubectl/pkg/cmd/util"
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
func NewCustomManager(resourceManager ResourceManager, inventory Inventory) *Manager {
	return &Manager{
		resourceManager: resourceManager,
		inventory:       inventory,
	}
}

// NewManager creates a new ssa manager with default backing resource manager (fluxcd/ssa) and inventory (ConfigMap).
func NewManager(ctx context.Context, kubeconfig *rest.Config, fieldManagerName, inventoryNamespace, inventoryName string) (*Manager, error) {
	dc, err := discovery.NewDiscoveryClientForConfig(kubeconfig)
	if err != nil {
		return nil, err
	}

	mapper := restmapper.NewDeferredDiscoveryRESTMapper(memory.NewMemCacheClient(dc))

	kubeClient, err := client.New(kubeconfig, client.Options{
		Mapper: mapper,
	})
	if err != nil {
		return nil, err
	}

	poller := polling.NewStatusPoller(kubeClient, mapper, polling.Options{})

	resourceManager := ssa.NewResourceManager(kubeClient, poller, ssa.Owner{
		Field: fieldManagerName,
	})

	cachedDC := memory.NewMemCacheClient(dc)

	clientGetter := K8sRESTClientGetter{
		RestConfig:      kubeconfig,
		DiscoveryClient: cachedDC,
		Mapper:          mapper,
		ClientConfig:    nil,
	}

	factory := kubeutil.NewFactory(clientGetter)

	dynamicClient, err := dynamic.NewForConfig(kubeconfig)
	if err != nil {
		return nil, err
	}

	err = configmap.AssureInventoryNamespace(ctx, inventoryNamespace, dynamicClient)
	if err != nil {
		return nil, err
	}

	inventory, err := configmap.NewInventory(ctx, inventoryNamespace, inventoryName, factory)
	if err != nil {
		return nil, err
	}

	return NewCustomManager(resourceManager, inventory), nil
}

// K8sRESTClientGetter is a basic implementation of the kubernetes genericclioptions.RESTClientGetter.
type K8sRESTClientGetter struct {
	RestConfig      *rest.Config
	DiscoveryClient discovery.CachedDiscoveryInterface
	Mapper          meta.RESTMapper
	ClientConfig    clientcmd.ClientConfig
}

func (getter K8sRESTClientGetter) ToRESTConfig() (*rest.Config, error) {
	return getter.RestConfig, nil
}

func (getter K8sRESTClientGetter) ToDiscoveryClient() (discovery.CachedDiscoveryInterface, error) {
	return getter.DiscoveryClient, nil
}

func (getter K8sRESTClientGetter) ToRESTMapper() (meta.RESTMapper, error) {
	return getter.Mapper, nil
}

func (getter K8sRESTClientGetter) ToRawKubeConfigLoader() clientcmd.ClientConfig {
	return getter.ClientConfig
}
