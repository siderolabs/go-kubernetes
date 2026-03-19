// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at http://mozilla.org/MPL/2.0/.

//go:build integration

// nolint:goconst
package integration_test

import (
	"context"
	_ "embed"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/fluxcd/cli-utils/pkg/object"
	fluxssa "github.com/fluxcd/pkg/ssa"
	"github.com/fluxcd/pkg/ssa/utils"
	"github.com/siderolabs/gen/xslices"
	"github.com/siderolabs/go-retry/retry"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/fields"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/watch"
	"k8s.io/client-go/discovery"
	"k8s.io/client-go/discovery/cached/memory"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/restmapper"
	"k8s.io/client-go/tools/cache"
	watchtools "k8s.io/client-go/tools/watch"

	"github.com/siderolabs/go-kubernetes/kubernetes/ssa"
)

var (
	//go:embed testdata/namespace_manifest.yml
	namespaceManifest string
	//go:embed testdata/configmap_manifest.yml
	configMapManifest string
	//go:embed testdata/secret_manifest.yml
	secretManifest string
	//go:embed testdata/deployment_manifest.yml
	deploymentManifest string
	//go:embed testdata/widget_crd.yaml
	widgetCRDManifest string
	//go:embed testdata/widget.yaml
	widgetManifest string
)

func getTestObjects(t *testing.T) (ns, cm, secret, deploy *unstructured.Unstructured) {
	ns, err := utils.ReadObject(strings.NewReader(namespaceManifest))
	require.NoError(t, err)

	cm, err = utils.ReadObject(strings.NewReader(configMapManifest))
	require.NoError(t, err)

	secret, err = utils.ReadObject(strings.NewReader(secretManifest))
	require.NoError(t, err)

	deploy, err = utils.ReadObject(strings.NewReader(deploymentManifest))
	require.NoError(t, err)

	return ns, cm, secret, deploy
}

var skipCleanup = os.Getenv("TEST_SKIP_CLEANUP") == "true"

func TestServerSideApply(t *testing.T) {
	kubeconfig := getKubeconfig(t)

	dynamicClient, err := dynamic.NewForConfig(kubeconfig)
	require.NoError(t, err, "failed to create a kubernetes client")

	discoveryClient, err := discovery.NewDiscoveryClientForConfig(kubeconfig)
	require.NoError(t, err)

	mapper := restmapper.NewDeferredDiscoveryRESTMapper(memory.NewMemCacheClient(discoveryClient))

	inventoryNS := "test-inventory"
	inventoryName := "test-inventory"
	manager, err := ssa.NewManager(t.Context(), kubeconfig, "unit-test-field-manager", inventoryNS, inventoryName)
	require.NoError(t, err, "failed to create SSA manager")

	if !skipCleanup {
		t.Log("cleaning up the kubernetes environment")

		t.Cleanup(func() {
			cleanupCtx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
			defer cancel()

			require.NoError(t, manager.Destroy(cleanupCtx, ssa.DestroyOptions{DeletePropagationPolicy: metav1.DeletePropagationForeground}))
		})
	}

	ns, cm, secret, deploy := getTestObjects(t)

	// NOTE: these tests need to run in sequence in correct order

	t.Run("clean up from previous runs", func(t *testing.T) {
		ctx, cancel := context.WithTimeout(t.Context(), 20*time.Second)
		defer cancel()

		err = manager.Destroy(ctx, ssa.DestroyOptions{DeletePropagationPolicy: metav1.DeletePropagationForeground})
		require.NoError(t, err)

		t.Log("wait until the namespace from previous run has fully terminated")
		waitForResourceDeleted(ctx, t, dynamicClient, ns, mapper)
	})

	t.Run("deploy Namespace and ConfigMap", func(t *testing.T) {
		results, err := manager.Apply(t.Context(), []*unstructured.Unstructured{ns, cm}, ssa.ApplyOptions{})
		require.NoError(t, err)
		require.Len(t, results, 2)

		for _, r := range results {
			assert.Equal(t, r.Action, ssa.CreatedAction)
			assert.Contains(t, r.Diff, "+apiVersion: v1")
		}

		resultSubjects := xslices.Map(results, func(r ssa.Change) string { return r.Subject })
		require.Contains(t, resultSubjects, "Namespace/test-lab")
		require.Contains(t, resultSubjects, "ConfigMap/test-lab/app-config")
	})

	t.Run("deploy Namespace and ConfigMap a second time, expect no changes", func(t *testing.T) {
		results, err := manager.Apply(t.Context(), []*unstructured.Unstructured{ns, cm}, ssa.ApplyOptions{})
		require.NoError(t, err)
		require.Len(t, results, 2)

		for _, r := range results {
			assert.Equal(t, ssa.UnchangedAction, r.Action)
			assert.Equal(t, "", r.Diff)
		}
	})

	t.Run("add a Secret to the existing set", func(t *testing.T) {
		results, err := manager.Apply(t.Context(), []*unstructured.Unstructured{ns, cm, secret}, ssa.ApplyOptions{})
		require.NoError(t, err)
		require.Len(t, results, 3)

		resultSubjects := xslices.Map(results, func(r ssa.Change) string { return r.Subject })
		require.Contains(t, resultSubjects, "Secret/test-lab/app-secret")

		for _, r := range results {
			if r.Subject == "Secret/test-lab/app-secret" {
				assert.Equal(t, ssa.CreatedAction, r.Action)
				assert.Contains(t, r.Diff, "+apiVersion: v1")
			}
		}

		assertResourceDeployed(t, dynamicClient, secret)
	})

	t.Run("diff without the secret: expect namespace unchanged, configmap configured, secret pruned", func(t *testing.T) {
		cmForDiff := cm.DeepCopy()
		cmForDiff.Object["data"] = map[string]any{
			"APP_MESSAGE": "hello from diff",
			"APP_PORT":    "9090",
		}

		diffResults, err := manager.Diff(t.Context(), []*unstructured.Unstructured{ns, cmForDiff}, ssa.DiffOptions{})
		require.NoError(t, err)
		require.Len(t, diffResults, 3)

		for _, dr := range diffResults {
			switch dr.Subject {
			case "Namespace/test-lab":
				assert.Equal(t, ssa.DiffUnchangedAction, dr.Action)
			case "ConfigMap/test-lab/app-config":
				assert.Equal(t, ssa.DiffConfiguredAction, dr.Action)
				assert.Contains(t, dr.Diff, "hello from diff")
			case "Secret/test-lab/app-secret":
				assert.Equal(t, ssa.DiffPrunedAction, dr.Action)
				assert.Contains(t, dr.Diff, "-kind: Secret")
			}
		}
	})

	t.Run("modify action: reapply modified secret and configmap", func(t *testing.T) {
		cmModified := cm.DeepCopy()
		cmModified.Object["data"] = map[string]any{
			"APP_MESSAGE": "hello from configmap - modified",
			"APP_PORT":    "9090",
		}

		secretModified := secret.DeepCopy()
		secretModified.Object["stringData"] = map[string]any{
			"PASSWORD": "new-password",
		}

		results, err := manager.Apply(t.Context(), []*unstructured.Unstructured{ns, cmModified, secretModified}, ssa.ApplyOptions{})
		require.NoError(t, err)
		require.Len(t, results, 3)

		for _, r := range results {
			if r.Subject == "ConfigMap/test-lab/app-config" {
				assert.Equal(t, ssa.ConfiguredAction, r.Action)
				assert.Contains(t, r.Diff, "hello from configmap - modified")
			}

			if r.Subject == "Secret/test-lab/app-secret" {
				assert.Equal(t, ssa.ConfiguredAction, r.Action)
				assert.NotEmpty(t, r.Diff, "secret diff should reflect the password change")
				assert.NotContains(t, r.Diff, "new-password", "plaintext secret values must not appear in diffs")
				assert.NotContains(t, r.Diff, "dummy-password", "plaintext secret values must not appear in diffs")
			}
		}

		clusterCM := assertResourceDeployed(t, dynamicClient, cm)

		cmData, _, err := unstructured.NestedStringMap(clusterCM.Object, "data")
		require.NoError(t, err)
		assert.Equal(t, "hello from configmap - modified", cmData["APP_MESSAGE"])
	})

	t.Run("prune action: remove configmap from set and reapply", func(t *testing.T) {
		results, err := manager.Apply(t.Context(), []*unstructured.Unstructured{ns, secret}, ssa.ApplyOptions{})
		require.NoError(t, err)
		require.Len(t, results, 3)

		for _, r := range results {
			if r.Subject == "ConfigMap/test-lab/app-config" {
				assert.Equal(t, ssa.DeletedAction, r.Action)
				assert.Contains(t, r.Diff, "-kind: ConfigMap")
			}
		}

		waitForResourceDeleted(t.Context(), t, dynamicClient, cm, mapper)
	})

	t.Run("inventory policy: unowned objects cannot be adopted with 'MustMatch' policy", func(t *testing.T) {
		foreignCM := &unstructured.Unstructured{}
		foreignCM.SetAPIVersion("v1")
		foreignCM.SetKind("ConfigMap")
		foreignCM.SetName("foreign-config")
		foreignCM.SetNamespace("test-lab")
		foreignCM.Object["data"] = map[string]any{"KEY": "value"}

		cmGVR := foreignCM.GroupVersionKind().GroupVersion().WithResource(strings.ToLower(foreignCM.GetKind()) + "s")
		_, err = dynamicClient.Resource(cmGVR).Namespace(foreignCM.GetNamespace()).Create(t.Context(), foreignCM, metav1.CreateOptions{FieldManager: "external-manager"})
		require.NoError(t, err)

		_, err = manager.Apply(t.Context(), []*unstructured.Unstructured{ns, secret, foreignCM}, ssa.ApplyOptions{})
		require.ErrorContains(t, err, "inventory policy")

		results, err := manager.Apply(t.Context(), []*unstructured.Unstructured{ns, secret, foreignCM}, ssa.ApplyOptions{
			InventoryPolicy: ssa.InventoryPolicyAdoptIfNoInventory,
		})
		require.NoError(t, err)
		assertResourceDeployed(t, dynamicClient, foreignCM)
		require.Len(t, results, 3)
	})

	t.Run("wait logic: apply a deployment that takes a while to get ready", func(t *testing.T) {
		results, err := manager.Apply(t.Context(), []*unstructured.Unstructured{ns, secret, deploy}, ssa.ApplyOptions{})
		require.NoError(t, err)
		require.Len(t, results, 4)

		t.Log("wait for deploy to become ready")

		// check that the manager.Wait call blocks for at least 25 secunds
		start := time.Now()

		err = manager.Wait(t.Context(), object.ObjMetadataSet{object.UnstructuredToObjMetadata(deploy)}, fluxssa.WaitOptions{FailFast: true, Timeout: 2 * time.Second, Interval: 1 * time.Second})
		require.ErrorContains(t, err, "timeout waiting for")

		err = manager.Wait(t.Context(), object.ObjMetadataSet{object.UnstructuredToObjMetadata(deploy)}, fluxssa.WaitOptions{FailFast: true, Timeout: 1 * time.Minute, Interval: 1 * time.Second})
		require.NoError(t, err)

		elapsed := time.Since(start)
		assert.GreaterOrEqual(t, elapsed.Seconds(), float64(20), "manager.Wait should have blocked for at least 25 seconds due to minReadySeconds")
	})
}

func TestCRDWithCustomResourceApply(t *testing.T) {
	kubeconfig := getKubeconfig(t)

	dynamicClient, err := dynamic.NewForConfig(kubeconfig)
	require.NoError(t, err)

	discoveryClient, err := discovery.NewDiscoveryClientForConfig(kubeconfig)
	require.NoError(t, err)

	mapper := restmapper.NewDeferredDiscoveryRESTMapper(memory.NewMemCacheClient(discoveryClient))

	inventoryNS := "test-crd-inventory"
	inventoryName := "test-crd-inventory"
	manager, err := ssa.NewManager(t.Context(), kubeconfig, "crd-test-field-manager", inventoryNS, inventoryName)
	require.NoError(t, err)

	crd, err := utils.ReadObject(strings.NewReader(widgetCRDManifest))
	require.NoError(t, err)

	widget, err := utils.ReadObject(strings.NewReader(widgetManifest))
	require.NoError(t, err)

	ns, _, _, _ := getTestObjects(t)

	if !skipCleanup {
		t.Cleanup(func() {
			cleanupCtx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
			defer cancel()

			require.NoError(t, manager.Destroy(cleanupCtx, ssa.DestroyOptions{DeletePropagationPolicy: metav1.DeletePropagationForeground}))

			waitForResourceDeleted(cleanupCtx, t, dynamicClient, ns, mapper)
			waitForResourceDeleted(cleanupCtx, t, dynamicClient, crd, mapper)
		})
	}

	t.Run("clean up from previous runs", func(t *testing.T) {
		ctx, cancel := context.WithTimeout(t.Context(), 2*time.Minute)
		defer cancel()

		err = manager.Destroy(ctx, ssa.DestroyOptions{DeletePropagationPolicy: metav1.DeletePropagationForeground})
		require.NoError(t, err)

		waitForResourceDeleted(ctx, t, dynamicClient, ns, mapper)
		waitForResourceDeleted(ctx, t, dynamicClient, crd, mapper)
	})

	t.Run("apply CRD and custom resource together", func(t *testing.T) {
		var results []ssa.Change

		err := retry.Constant(time.Second * 20).Retry(func() error {
			var err error

			results, err = manager.Apply(t.Context(), []*unstructured.Unstructured{ns, crd, widget}, ssa.ApplyOptions{})
			if err != nil {
				if !meta.IsNoMatchError(err) {
					return err
				}

				mapper.Reset()

				return retry.ExpectedError(err)
			}

			return nil
		})
		require.NoError(t, err, "failed to apply CRD and custom resource")
		require.NotEmpty(t, results, "partial results for successfully applied objects should be returned")

		resultsBySubject := map[string]ssa.Change{}
		for _, r := range results {
			resultsBySubject[r.Subject] = r
		}

		assert.Equal(t, ssa.CreatedAction, resultsBySubject["Widget/test-lab/my-shiny-widget"].Action)

		assertResourceDeployed(t, dynamicClient, crd)
		assertResourceDeployed(t, dynamicClient, widget)
	})

	t.Run("reapply CRD and custom resource, expect no changes", func(t *testing.T) {
		// Re-read to get fresh objects without annotations from previous apply.
		crd2, err := utils.ReadObject(strings.NewReader(widgetCRDManifest))
		require.NoError(t, err)

		widget2, err := utils.ReadObject(strings.NewReader(widgetManifest))
		require.NoError(t, err)

		results, err := manager.Apply(t.Context(), []*unstructured.Unstructured{ns, crd2, widget2}, ssa.ApplyOptions{})
		require.NoError(t, err)
		require.Len(t, results, 3)

		for _, r := range results {
			assert.Equal(t, ssa.UnchangedAction, r.Action, "expected unchanged for %s", r.Subject)
		}
	})
}

func assertResourceDeployed(t *testing.T, dynamicClient *dynamic.DynamicClient, obj *unstructured.Unstructured) *unstructured.Unstructured {
	clusterObj, err := dynamicClient.Resource(
		obj.GroupVersionKind().GroupVersion().WithResource(strings.ToLower(obj.GetKind())+"s"),
	).Namespace(obj.GetNamespace()).Get(t.Context(), obj.GetName(), metav1.GetOptions{})
	require.NoError(t, err, "%s should exist in the cluster", obj.GetKind())

	return clusterObj
}

// based on https://github.com/siderolabs/talos/blob/8b1c974a2a733c870f371ccb7a86ccc616dbc7ea/internal/integration/base/k8s.go#L876
func waitForResourceDeleted(ctx context.Context, t *testing.T, dynamicClient *dynamic.DynamicClient, obj *unstructured.Unstructured, mapper meta.RESTMapper) {
	t.Helper()

	mapping, err := mapper.RESTMapping(obj.GetObjectKind().GroupVersionKind().GroupKind(), obj.GetObjectKind().GroupVersionKind().Version)
	if err != nil {
		if meta.IsNoMatchError(err) {
			t.Logf("resource type %s not found in RESTMapper, assuming crd wasn't applied", obj.GetKind())

			return
		}

		require.NoError(t, err, "error creating mapping for object %s", obj.GetName())
	}

	dr := dynamicClient.Resource(mapping.Resource).Namespace(obj.GetNamespace())

	fieldSelector := fields.OneTermEqualSelector("metadata.name", obj.GetName()).String()
	lw := &cache.ListWatch{
		ListFunc: func(options metav1.ListOptions) (runtime.Object, error) {
			options.FieldSelector = fieldSelector

			return dr.List(ctx, options)
		},
		WatchFunc: func(options metav1.ListOptions) (watch.Interface, error) {
			options.FieldSelector = fieldSelector

			return dr.Watch(ctx, options)
		},
	}

	preconditionFunc := func(store cache.Store) (bool, error) {
		var exists bool

		_, exists, err = store.Get(&metav1.ObjectMeta{Namespace: obj.GetNamespace(), Name: obj.GetName()})
		if err != nil {
			return true, err
		}

		if !exists {
			// since we're looking for it to disappear we just return here if it no longer exists
			return true, nil
		}

		return false, nil
	}

	watchCtx, cancel := context.WithTimeout(ctx, time.Minute)
	defer cancel()

	_, err = watchtools.UntilWithSync(watchCtx, lw, &unstructured.Unstructured{}, preconditionFunc, func(event watch.Event) (bool, error) {
		return event.Type == watch.Deleted, nil
	})

	assert.NoError(t, err, "error waiting for the object to be deleted %s/%s/%s", obj.GetObjectKind().GroupVersionKind(), obj.GetNamespace(), obj.GetName())
}
