// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at http://mozilla.org/MPL/2.0/.

package manifests

import (
	"context"
	"fmt"
	"log"
	"slices"
	"time"

	"github.com/fluxcd/cli-utils/pkg/kstatus/polling"
	"github.com/fluxcd/pkg/ssa"
	v1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/discovery"
	"k8s.io/client-go/discovery/cached/memory"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/restmapper"
	"k8s.io/client-go/tools/clientcmd"
	"k8s.io/kubectl/pkg/cmd/util"
	kubeapply "sigs.k8s.io/cli-utils/pkg/apply"
	kevent "sigs.k8s.io/cli-utils/pkg/apply/event"
	"sigs.k8s.io/cli-utils/pkg/apply/prune"
	"sigs.k8s.io/cli-utils/pkg/common"
	"sigs.k8s.io/cli-utils/pkg/inventory"
	"sigs.k8s.io/cli-utils/pkg/object"
	"sigs.k8s.io/cli-utils/pkg/object/validation"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/siderolabs/go-kubernetes/kubernetes"
	"github.com/siderolabs/go-kubernetes/kubernetes/manifests/event"
)

type DiffAction string

const (
	CreateAction DiffAction = "create"
	PruneAction  DiffAction = "prune"
	ModifyAction DiffAction = "modify"

	namespaceKind = "Namespace"
)

// DiffResult is a diff result for one object.
type DiffResult struct {
	Object Manifest
	Action DiffAction
	Diff   string
}

// SSApplyBehaviorOptions define the options purely ralted to the apply behavior of server side apply.
type SSApplyBehaviorOptions struct {
	InventoryPolicy inventory.Policy
	// ReconcileTimeout defines whether the applier should wait until all applied resources have been reconciled, and if so, how long to wait.
	ReconcileTimeout time.Duration
	// PruneTimeout defines whether we should wait for all resources to be fully deleted after pruning, and if so, how long we should wait.
	PruneTimeout time.Duration
	// ForceConflicts overwrites the fields when applying if the field manager differs.
	ForceConflicts bool
	// ForceConflicts overwrites the fields when applying if the field manager differs.
	DryRun bool
	// NoPrune defines whether pruning of previously applied objects should happen after apply.
	NoPrune bool
}

// SSAOptions define the kubernetes server side apply related options.
type SSAOptions struct {
	FieldManagerName   string
	InventoryNamespace string
	InventoryName      string

	SSApplyBehaviorOptions
}

func DefaultSSApplyBehaviorOptions() SSApplyBehaviorOptions {
	return SSApplyBehaviorOptions{
		InventoryPolicy:  inventory.PolicyAdoptIfNoInventory,
		ReconcileTimeout: 3 * time.Minute,
		PruneTimeout:     3 * time.Minute,
	}
}

// DiffSSA performs a diff between the current and desired state, returning objects that are to be created, pruned and modified.
func DiffSSA(
	ctx context.Context,
	objects []Manifest,
	config *rest.Config,
	ops SSAOptions,
) ([]DiffResult, error) {
	result := []DiffResult{}

	helpers, err := initHelpers(ctx, config, ops)
	if err != nil {
		return nil, err
	}

	pruner, err := prune.NewPruner(helpers.factory, helpers.inventoryClient)
	if err != nil {
		return nil, err
	}

	inventory, err := helpers.inventoryClient.Get(ctx, helpers.inventoryInfo, inventory.GetOptions{})
	if err != nil {
		return nil, err
	}

	pruneObjs, err := pruner.GetPruneObjs(ctx, inventory, objects, prune.Options{})
	if err != nil {
		return nil, err
	}

	if !ops.NoPrune {
		for _, obj := range pruneObjs {
			// create a "deleted" diff
			diffObj := obj.DeepCopy()
			// remove managed fields as they're not really useful for the prune diff
			diffObj.SetManagedFields([]metav1.ManagedFieldsEntry{})

			diff, err := manifestDiff(diffObj, nil)
			if err != nil {
				return nil, err
			}

			result = append(result, DiffResult{
				Object: obj,
				Action: PruneAction,
				Diff:   diff,
			})
		}
	}

	for _, obj := range objects {
		changeSet, inclusterObj, inputObj, err := helpers.resourceManager.Diff(ctx, obj, ssa.DiffOptions{})
		if (err != nil && apierrors.IsNotFound(err)) || (err == nil && changeSet.Action == ssa.CreatedAction) {
			// create a "new" diff
			diff, err1 := manifestDiff(nil, obj)
			if err1 != nil {
				return nil, err1
			}

			result = append(result, DiffResult{
				Object: obj,
				Action: CreateAction,
				Diff:   diff,
			})

			continue
		}

		if err != nil {
			return nil, err
		}

		if changeSet.Action == ssa.ConfiguredAction {
			diff, err := manifestDiff(inclusterObj, inputObj)
			if err != nil {
				return nil, err
			}

			result = append(result, DiffResult{
				Object: obj,
				Action: ModifyAction,
				Diff:   diff,
			})
		}
	}

	return result, nil
}

// SyncSSA applies the manifests to the cluster via SSA providing the results and diff if any.
// If an individual event contains an error, it means that the given action for that object has failed.
// If an error is returned by the function itself, it means something fatal has occurred and the process can not be continued.
// By default server side apply is used. Client side apply is used only in dry-run when some of the resources are to be created
// in a namespace that does not yet exist.
func SyncSSA(
	ctx context.Context,
	objects []Manifest,
	config *rest.Config,
	eventCh chan<- event.Event,
	ops SSAOptions,
) error {
	dialer := kubernetes.NewDialer()
	config.Dial = dialer.DialContext

	defer func() {
		dialer.CloseAll()

		config.Dial = nil
	}()

	helpers, err := initHelpers(ctx, config, ops)
	if err != nil {
		return err
	}

	if !ops.DryRun {
		return applySSA(ctx, helpers, objects, eventCh, common.DryRunNone, ops)
	}

	// Is a dry-run apply.
	// Need to separate resources that are in yet-to-be-created namespace(s) and use client-side dry run for them separately.

	newNamespaces := []string{}

	for _, o := range objects {
		if o.GetKind() != namespaceKind {
			continue
		}

		_, err1 := helpers.dynamicClient.Resource(namespaceGVR).Get(ctx, o.GetName(), metav1.GetOptions{})
		if err1 != nil {
			if apierrors.IsNotFound(err1) {
				newNamespaces = append(newNamespaces, o.GetName())
			}
		}
	}

	clientSideDryRunResources := []Manifest{}
	serverSideDryRunResources := []Manifest{}

	for _, o := range objects {
		if slices.Contains(newNamespaces, o.GetNamespace()) ||
			(o.GetKind() == namespaceKind && slices.Contains(newNamespaces, o.GetName())) {
			clientSideDryRunResources = append(clientSideDryRunResources, o)
		} else {
			serverSideDryRunResources = append(serverSideDryRunResources, o)
		}
	}

	if len(clientSideDryRunResources) > 0 {
		err = applySSA(ctx, helpers, clientSideDryRunResources, eventCh, common.DryRunClient, ops)
		if err != nil {
			return err
		}
	}

	// Run even if there are no resources, as the prune check still needs to be done.
	return applySSA(ctx, helpers, serverSideDryRunResources, eventCh, common.DryRunServer, ops)
}

func applySSA(
	ctx context.Context,
	helpers syncHelpers,
	objects []Manifest,
	eventCh chan<- event.Event,
	dryRunStragedy common.DryRunStrategy,
	opts SSAOptions,
) error {
	applyOps := kubeapply.ApplierOptions{
		ReconcileTimeout:       opts.ReconcileTimeout,
		PruneTimeout:           opts.PruneTimeout,
		EmitStatusEvents:       false,
		InventoryPolicy:        opts.InventoryPolicy,
		ValidationPolicy:       validation.ExitEarly,
		PrunePropagationPolicy: metav1.DeletePropagationBackground,
		DryRunStrategy:         dryRunStragedy,
		NoPrune:                opts.NoPrune,
	}

	if dryRunStragedy != common.DryRunClient {
		applyOps.ServerSideOptions = common.ServerSideOptions{
			ServerSideApply: true,
			FieldManager:    opts.FieldManagerName,
			ForceConflicts:  opts.ForceConflicts,
		}
	} else {
		// Disable pruning on client side dry-run as we're running only a subset of the resources,
		// which would cause the omitted resources to be marked for pruning.
		applyOps.NoPrune = true
	}

	kubeEventCh := helpers.applier.Run(ctx, helpers.inventoryInfo, objects, applyOps)

	for {
		select {
		case <-ctx.Done():
			return nil
		case e, ok := <-kubeEventCh:
			if !ok {
				return nil
			}

			err1 := handleKubernetesEvent(e, objects, eventCh)
			if err1 != nil {
				return err1
			}
		}
	}
}

func handleKubernetesEvent(e kevent.Event, objects []Manifest, eventCh chan<- event.Event) error {
	switch e.Type {
	case kevent.ErrorType:
		return e.ErrorEvent.Err

	case kevent.ApplyType:
		obj, err := getEventObject(e.ApplyEvent.Identifier, objects)
		if err != nil {
			return err
		}

		objPath := getManifestPath(obj)

		applySkipped := false

		var applyFailedErr error

		switch e.ApplyEvent.Status {
		case kevent.ApplySkipped:
			applySkipped = true
		case kevent.ApplyFailed:
			applyFailedErr = fmt.Errorf("apply of %q has failed: %w", objPath, e.ApplyEvent.Error)
		case kevent.ApplyPending:
			applyFailedErr = fmt.Errorf("apply of %q is pending", objPath)
		case kevent.ApplySuccessful:
		}

		eventCh <- event.Event{
			Type:     event.ApplyType,
			ObjectID: e.ApplyEvent.Identifier,
			Error:    applyFailedErr,

			Apply: event.ApplyEvent{
				Skipped: applySkipped,
			},
		}
	case kevent.PruneType:
		eventCh <- event.Event{
			Type:     event.PruneType,
			ObjectID: e.PruneEvent.Identifier,
		}
	case kevent.WaitType:
		var reconcileErr error

		switch e.WaitEvent.Status {
		case kevent.ReconcilePending:
			eventCh <- event.Event{
				Type:     event.WaitType,
				ObjectID: e.WaitEvent.Identifier,
			}
		case kevent.ReconcileTimeout:
			reconcileErr = fmt.Errorf("reconcile timed out")
		case kevent.ReconcileFailed:
			reconcileErr = fmt.Errorf("reconcile failed")
		// don't care about other wait events (only success or error)
		case kevent.ReconcileSkipped, kevent.ReconcileSuccessful:
			break
		}

		if reconcileErr != nil || e.WaitEvent.Status == kevent.ReconcileSuccessful {
			eventCh <- event.Event{
				Type:     event.RolloutType,
				ObjectID: e.WaitEvent.Identifier,
				Error:    reconcileErr,
			}
		}

	// don't care about these
	case kevent.InitType, kevent.ActionGroupType, kevent.StatusType, kevent.ValidationType,
		// the sync will never delete (only prune), so we don't need to know about this event
		kevent.DeleteType:
		break
	}

	return nil
}

type syncHelpers struct {
	applier         *kubeapply.Applier
	inventoryInfo   *inventory.SingleObjectInfo
	inventoryClient inventory.Client
	resourceManager *ssa.ResourceManager
	factory         util.Factory
	kubeClient      client.Client
	dynamicClient   *dynamic.DynamicClient
}

// initHelpers initializes tools needed for syncing and ensures the inventory and it's namespace.
func initHelpers(ctx context.Context, config *rest.Config, ops SSAOptions) (syncHelpers, error) {
	dynamicClient, mapper, err := initSyncHelpers(config)
	if err != nil {
		return syncHelpers{}, err
	}

	discoveryClient, err := discovery.NewDiscoveryClientForConfig(config)
	if err != nil {
		return syncHelpers{}, err
	}

	cachedDC := memory.NewMemCacheClient(discoveryClient)

	clientGetter := K8sRESTClientGetter{
		RestConfig:      config,
		DiscoveryClient: cachedDC,
		Mapper:          mapper,
		ClientConfig:    nil,
	}

	factory := util.NewFactory(clientGetter)

	inventoryClient, err := inventory.ConfigMapClientFactory{StatusEnabled: true}.NewClient(factory)
	if err != nil {
		return syncHelpers{}, err
	}

	builder := kubeapply.NewApplierBuilder().
		WithRestConfig(config).
		WithRestMapper(mapper).
		WithDiscoveryClient(cachedDC).
		WithDynamicClient(dynamicClient).
		WithRestMapper(mapper).
		WithInventoryClient(inventoryClient).
		WithFactory(factory)

	applier, err := builder.Build()
	if err != nil {
		return syncHelpers{}, err
	}

	inventoryInfo := inventory.NewSingleObjectInfo(inventory.ID(ops.InventoryName), types.NamespacedName{Namespace: ops.InventoryNamespace, Name: ops.InventoryName})

	err = AssureInventoryNamespace(ctx, config, ops.InventoryNamespace, dynamicClient)
	if err != nil {
		return syncHelpers{}, err
	}

	err = AssureInventory(ctx, inventoryClient, inventoryInfo)
	if err != nil {
		return syncHelpers{}, err
	}

	kubeClient, err := client.New(config, client.Options{
		Mapper: mapper,
	})
	if err != nil {
		return syncHelpers{}, err
	}

	poller := polling.NewStatusPoller(kubeClient, mapper, polling.Options{})

	resourceManager := ssa.NewResourceManager(kubeClient, poller, ssa.Owner{
		Field: "resource-manager",
	})

	return syncHelpers{
		applier:         applier,
		inventoryInfo:   inventoryInfo,
		inventoryClient: inventoryClient,
		resourceManager: resourceManager,
		factory:         factory,
		kubeClient:      kubeClient,
		dynamicClient:   dynamicClient,
	}, nil
}

func getEventObject(id object.ObjMetadata, objects []Manifest) (*unstructured.Unstructured, error) {
	var obj *unstructured.Unstructured

	for _, o := range objects {
		if o.GetName() == id.Name &&
			o.GetNamespace() == id.Namespace &&
			o.GroupVersionKind().Kind == id.GroupKind.Kind &&
			o.GroupVersionKind().Group == id.GroupKind.Group {
			obj = o

			break
		}
	}

	if obj == nil {
		return nil, fmt.Errorf("failed to find input object for status event %q", id.String())
	}

	return obj, nil
}

var namespaceGVR = schema.GroupVersionResource{
	Group:    "", // core API group
	Version:  "v1",
	Resource: "namespaces",
}

func AssureInventoryNamespace(ctx context.Context, config *rest.Config, inventoryNamespace string, k8sClient *dynamic.DynamicClient) error {
	_, err := k8sClient.Resource(namespaceGVR).Get(ctx, inventoryNamespace, metav1.GetOptions{})
	if err != nil {
		if apierrors.IsNotFound(err) {
			ns := &v1.Namespace{
				ObjectMeta: metav1.ObjectMeta{
					Name: inventoryNamespace,
				},
			}

			objMap, err1 := runtime.DefaultUnstructuredConverter.ToUnstructured(ns)
			if err1 != nil {
				log.Fatalf("failed to convert: %v", err)
			}

			unstructuredNS := &unstructured.Unstructured{Object: objMap}

			_, err = k8sClient.Resource(namespaceGVR).Create(ctx, unstructuredNS, metav1.CreateOptions{})
			if err != nil {
				return err
			}
		} else {
			return err
		}
	}

	return nil
}

func AssureInventory(ctx context.Context, inventoryClient inventory.Client, inventoryInfo *inventory.SingleObjectInfo) error {
	_, getErr := inventoryClient.Get(ctx, inventoryInfo, inventory.GetOptions{})
	if apierrors.IsNotFound(getErr) {
		inv, err := inventoryClient.NewInventory(inventoryInfo)
		if err != nil {
			return err
		}

		err = inventoryClient.CreateOrUpdate(ctx, inv, inventory.UpdateOptions{})
		if err != nil {
			return err
		}
	} else {
		return getErr
	}

	return nil
}

func initSyncHelpers(config *rest.Config) (*dynamic.DynamicClient, *restmapper.DeferredDiscoveryRESTMapper, error) {
	k8sClient, err := dynamic.NewForConfig(config)
	if err != nil {
		return nil, nil, err
	}

	dc, err := discovery.NewDiscoveryClientForConfig(config)
	if err != nil {
		return nil, nil, err
	}

	mapper := restmapper.NewDeferredDiscoveryRESTMapper(memory.NewMemCacheClient(dc))

	return k8sClient, mapper, err
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
