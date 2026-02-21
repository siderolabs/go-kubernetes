// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at http://mozilla.org/MPL/2.0/.

// nolint:goconst
package integration_test

import (
	"context"
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
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/cli-runtime/pkg/genericclioptions"
	"k8s.io/client-go/dynamic"
	"sigs.k8s.io/cli-utils/pkg/inventory"

	"github.com/siderolabs/go-kubernetes/kubernetes/ssa"
)

const (
	namespaceManifest = `
apiVersion: v1
kind: Namespace
metadata:
  name: test-lab
  labels:
    app.kubernetes.io/part-of: test-lab
    my-label: "123"
  annotations:
    asd: asd`

	configMapManifest = `
apiVersion: v1
kind: ConfigMap
metadata:
  name: app-config
  namespace: test-lab
  annotations:
    asd: asd2
data:
  APP_MESSAGE: "hello from configmap"
  APP_PORT: "8080"`

	secretManifest = `
apiVersion: v1
kind: Secret
metadata:
  name: app-secret
  namespace: test-lab
type: Opaque
stringData:
  PASSWORD: "dummy-password"
`

	deploymentManifest = `
apiVersion: apps/v1
kind: Deployment
metadata:
  name: app-deployment
  namespace: test-lab
spec:
  replicas: 1
  minReadySeconds: 25
  selector:
    matchLabels:
      app: app-deployment
  template:
    metadata:
      labels:
        app: app-deployment
    spec:
      containers:
      - name: app
        image: busybox:1.36.0
        command: ["sh", "-c", "sleep 3600"]
        securityContext:
          allowPrivilegeEscalation: false
          capabilities:
            drop: ["ALL"]`
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
	flags := genericclioptions.NewConfigFlags(true)
	kubeconfig, err := flags.ToRESTConfig()
	require.NoError(t, err, "failed to retrieve kubeconfig")

	dynamicClient, err := dynamic.NewForConfig(kubeconfig)
	require.NoError(t, err, "failed to create a kubernetes client")

	// wait for cluster to be ready
	err = retry.Constant(2*time.Minute, retry.WithUnits(500*time.Millisecond)).RetryWithContext(t.Context(), func(ctx context.Context) error {
		_, err = dynamicClient.Resource(schema.GroupVersionResource{
			Version:  "v1",
			Resource: "namespaces",
		}).List(ctx, metav1.ListOptions{})

		return retry.ExpectedError(err)
	})
	require.NoError(t, err, "integration test cluster not ready, failed to list namespaces")

	inventoryNS := "test-inventory"
	inventoryName := "test-inventory"
	manager, err := ssa.NewManager(t.Context(), kubeconfig, "unit-test-field-manager", inventoryNS, inventoryName)
	require.NoError(t, err, "failed to create SSA manager")

	t.Log("clean up from previous runs")
	err = manager.Destroy(t.Context(), ssa.DestroyOptions{DeletePropagationPolicy: metav1.DeletePropagationForeground})
	require.NoError(t, err)

	ns, cm, secret, deploy := getTestObjects(t)

	t.Log("wait until the namespace from previous run has fully terminated")
	waitForResourceDeleted(t, dynamicClient, ns)

	if !skipCleanup {
		t.Log("cleaning up the kubernetes environment")

		t.Cleanup(func() {
			cleanupCtx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
			defer cancel()

			require.NoError(t, manager.Destroy(cleanupCtx, ssa.DestroyOptions{DeletePropagationPolicy: metav1.DeletePropagationForeground}))
		})
	}

	t.Log("deploy Namespace and ConfigMap")

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

	t.Log("deploy Namespace and ConfigMap a second time, expect no changes")
	results, err = manager.Apply(t.Context(), []*unstructured.Unstructured{ns, cm}, ssa.ApplyOptions{})
	require.NoError(t, err)
	require.Len(t, results, 2)

	for _, r := range results {
		assert.Equal(t, ssa.UnchangedAction, r.Action)
		assert.Equal(t, "", r.Diff)
	}

	t.Log("add a Secret to the existing set")

	results, err = manager.Apply(t.Context(), []*unstructured.Unstructured{ns, cm, secret}, ssa.ApplyOptions{})
	require.NoError(t, err)
	require.Len(t, results, 3)

	resultSubjects = xslices.Map(results, func(r ssa.Change) string { return r.Subject })
	require.Contains(t, resultSubjects, "Secret/test-lab/app-secret")

	for _, r := range results {
		if r.Subject == "Secret/test-lab/app-secret" {
			assert.Equal(t, ssa.CreatedAction, r.Action)
			assert.Contains(t, r.Diff, "+apiVersion: v1")
		}
	}

	assertResourceDeployed(t, dynamicClient, secret)

	t.Log("diff without the secret: namespace unchanged, configmap configured, secret pruned")

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

	t.Log("modify and reapply the secret and configmap")

	cmModified := cm.DeepCopy()
	cmModified.Object["data"] = map[string]any{
		"APP_MESSAGE": "hello from configmap - modified",
		"APP_PORT":    "9090",
	}

	secretModified := secret.DeepCopy()
	secretModified.Object["stringData"] = map[string]any{
		"PASSWORD": "new-password",
	}

	results, err = manager.Apply(t.Context(), []*unstructured.Unstructured{ns, cmModified, secretModified}, ssa.ApplyOptions{})
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

	t.Log("remove configmap from set and reapply")

	results, err = manager.Apply(t.Context(), []*unstructured.Unstructured{ns, secretModified}, ssa.ApplyOptions{})
	require.NoError(t, err)
	require.Len(t, results, 3)

	for _, r := range results {
		if r.Subject == "ConfigMap/test-lab/app-config" {
			assert.Equal(t, ssa.DeletedAction, r.Action)
			assert.Contains(t, r.Diff, "-kind: ConfigMap")
		}
	}

	waitForResourceDeleted(t, dynamicClient, cm)

	t.Log("test inventory policy: unowned objects cannot be adopted with the default policy")

	foreignCM := &unstructured.Unstructured{}
	foreignCM.SetAPIVersion("v1")
	foreignCM.SetKind("ConfigMap")
	foreignCM.SetName("foreign-config")
	foreignCM.SetNamespace("test-lab")
	foreignCM.Object["data"] = map[string]any{"KEY": "value"}

	cmGVR := foreignCM.GroupVersionKind().GroupVersion().WithResource(strings.ToLower(foreignCM.GetKind()) + "s")
	_, err = dynamicClient.Resource(cmGVR).Namespace(foreignCM.GetNamespace()).Create(t.Context(), foreignCM, metav1.CreateOptions{FieldManager: "external-manager"})
	require.NoError(t, err)

	_, err = manager.Apply(t.Context(), []*unstructured.Unstructured{ns, secretModified, foreignCM}, ssa.ApplyOptions{})
	require.ErrorContains(t, err, "inventory policy")

	results, err = manager.Apply(t.Context(), []*unstructured.Unstructured{ns, secretModified, foreignCM}, ssa.ApplyOptions{
		InventoryPolicy: inventory.PolicyAdoptIfNoInventory,
	})
	require.NoError(t, err)
	assertResourceDeployed(t, dynamicClient, foreignCM)
	require.Len(t, results, 3)

	t.Log("test wait logic")
	t.Log("apply a deployment that takes a while to get ready")
	results, err = manager.Apply(t.Context(), []*unstructured.Unstructured{ns, secretModified, foreignCM, deploy}, ssa.ApplyOptions{})
	require.NoError(t, err)
	require.Len(t, results, 4)

	t.Log("wait for deploy to become ready")

	// check that the manager.Wait call blocks for at least 25 secunds
	start := time.Now()

	err = manager.Wait(t.Context(), object.ObjMetadataSet{object.UnstructuredToObjMetadata(deploy)}, fluxssa.WaitOptions{FailFast: true, Timeout: 1 * time.Minute, Interval: 1 * time.Second})
	require.NoError(t, err)

	elapsed := time.Since(start)
	assert.GreaterOrEqual(t, elapsed.Seconds(), float64(20), "manager.Wait should have blocked for at least 25 seconds due to minReadySeconds")
}

func assertResourceDeployed(t *testing.T, dynamicClient *dynamic.DynamicClient, obj *unstructured.Unstructured) *unstructured.Unstructured {
	clusterObj, err := dynamicClient.Resource(
		obj.GroupVersionKind().GroupVersion().WithResource(strings.ToLower(obj.GetKind())+"s"),
	).Namespace(obj.GetNamespace()).Get(t.Context(), obj.GetName(), metav1.GetOptions{})
	require.NoError(t, err, "%s should exist in the cluster", obj.GetKind())

	return clusterObj
}

func waitForResourceDeleted(t *testing.T, dynamicClient *dynamic.DynamicClient, obj *unstructured.Unstructured) {
	gvk := obj.GroupVersionKind()
	gvr := gvk.GroupVersion().WithResource(strings.ToLower(gvk.Kind) + "s")

	assert.NoError(t, retry.Constant(2*time.Minute).RetryWithContext(t.Context(), func(ctx context.Context) error {
		_, err := dynamicClient.Resource(gvr).Namespace(obj.GetNamespace()).Get(ctx, obj.GetName(), metav1.GetOptions{})

		if errors.IsNotFound(err) {
			return nil
		}

		if err == nil {
			return retry.ExpectedErrorf("resource %s/%s still exists", obj.GetNamespace(), obj.GetName())
		}

		return err
	}))
}
