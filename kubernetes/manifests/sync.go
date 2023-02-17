// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at http://mozilla.org/MPL/2.0/.

package manifests

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/hexops/gotextdiff"
	"github.com/hexops/gotextdiff/myers"
	"github.com/hexops/gotextdiff/span"
	"github.com/siderolabs/go-retry/retry"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/client-go/discovery"
	memory "k8s.io/client-go/discovery/cached"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/restmapper"
	k8syaml "sigs.k8s.io/yaml"

	"github.com/siderolabs/go-kubernetes/kubernetes"
)

// Sync applies the manifests to the cluster providing the results.
func Sync(ctx context.Context, objects []Manifest, config *rest.Config, dryRun bool, logFunc func(string, ...any)) error {
	dialer := kubernetes.NewDialer()
	config.Dial = dialer.DialContext

	defer dialer.CloseAll()

	k8sClient, err := dynamic.NewForConfig(config)
	if err != nil {
		return err
	}

	dc, err := discovery.NewDiscoveryClientForConfig(config)
	if err != nil {
		return err
	}

	mapper := restmapper.NewDeferredDiscoveryRESTMapper(memory.NewMemCacheClient(dc))

	// list of deployments to wait for to become ready after update
	var deployments []Manifest

	logFunc("updating manifests")

	for _, obj := range objects {
		logFunc(" > processing manifest %s %s", obj.GetKind(), obj.GetName())

		var (
			resp    *unstructured.Unstructured
			diff    string
			skipped bool
		)

		err = retry.Constant(3*time.Minute, retry.WithUnits(10*time.Second), retry.WithErrorLogging(true)).RetryWithContext(ctx, func(ctx context.Context) error {
			resp, diff, skipped, err = updateManifest(ctx, mapper, k8sClient, obj, dryRun)
			if kubernetes.IsRetryableError(err) || apierrors.IsConflict(err) {
				return retry.ExpectedError(err)
			}

			return err
		})

		if err != nil {
			return err
		}

		switch {
		case dryRun:
			var diffInfo string
			if diff != "" {
				diffInfo = fmt.Sprintf(", diff:\n%s", diff)
			}

			logFunc(" < apply skipped in dry run%s", diffInfo)

			continue
		case skipped:
			logFunc(" < apply skipped: nothing to update")

			continue
		}

		if resp.GetKind() == "Deployment" {
			deployments = append(deployments, resp)
		}

		logFunc(" < update applied, diff:\n%s", diff)
	}

	if len(deployments) == 0 {
		return nil
	}

	config.Dial = nil

	clientset, err := kubernetes.NewForConfig(config)
	if err != nil {
		return err
	}

	defer clientset.Close() //nolint:errcheck

	for _, obj := range deployments {
		obj := obj

		err := retry.Constant(3*time.Minute, retry.WithUnits(10*time.Second)).Retry(func() error {
			deployment, err := clientset.AppsV1().Deployments(obj.GetNamespace()).Get(ctx, obj.GetName(), metav1.GetOptions{})
			if err != nil {
				return err
			}

			if deployment.Status.ReadyReplicas != deployment.Status.Replicas || deployment.Status.UpdatedReplicas != deployment.Status.Replicas {
				return retry.ExpectedErrorf("deployment %s ready replicas %d != replicas %d", deployment.Name, deployment.Status.ReadyReplicas, deployment.Status.Replicas)
			}

			logFunc(" > updated %s", deployment.GetName())

			return nil
		})
		if err != nil {
			return err
		}
	}

	return nil
}

func updateManifest(
	ctx context.Context,
	mapper *restmapper.DeferredDiscoveryRESTMapper,
	k8sClient dynamic.Interface,
	obj Manifest,
	dryRun bool,
) (
	resp Manifest,
	diff string,
	skipped bool,
	err error,
) {
	mapping, err := mapper.RESTMapping(obj.GroupVersionKind().GroupKind(), obj.GroupVersionKind().Version)
	if err != nil {
		err = fmt.Errorf("error creating mapping for object %s: %w", obj.GetName(), err)

		return nil, "", false, err
	}

	var dr dynamic.ResourceInterface
	if mapping.Scope.Name() == meta.RESTScopeNameNamespace {
		// namespaced resources should specify the namespace
		dr = k8sClient.Resource(mapping.Resource).Namespace(obj.GetNamespace())
	} else {
		// for cluster-wide resources
		dr = k8sClient.Resource(mapping.Resource)
	}

	exists := true

	diff, err = getResourceDiff(ctx, dr, obj)
	if err != nil {
		if !apierrors.IsNotFound(err) {
			return nil, "", false, err
		}

		exists = false
		diff = "resource is going to be created"
	}

	switch {
	case dryRun:
		return nil, diff, exists, nil
	case !exists:
		resp, err = dr.Create(ctx, obj, metav1.CreateOptions{})
	case diff != "":
		resp, err = dr.Update(ctx, obj, metav1.UpdateOptions{})
	default:
		skipped = true
	}

	return resp, diff, skipped, err
}

func getResourceDiff(ctx context.Context, dr dynamic.ResourceInterface, obj Manifest) (string, error) {
	current, err := dr.Get(ctx, obj.GetName(), metav1.GetOptions{})
	if err != nil {
		return "", err
	}

	obj.SetResourceVersion(current.GetResourceVersion())

	resp, err := dr.Update(ctx, obj, metav1.UpdateOptions{
		DryRun: []string{"All"},
	})
	if err != nil {
		return "", err
	}

	ignoreKey := func(key ...string) {
		unstructured.RemoveNestedField(current.Object, key...)
		unstructured.RemoveNestedField(resp.Object, key...)
	}

	// drop fields which are not relevant and updated by Kubernetes
	ignoreKey("metadata", "uid")
	ignoreKey("metadata", "resourceVersion")
	ignoreKey("metadata", "generation")
	ignoreKey("metadata", "creationTimestamp")
	ignoreKey("metadata", "deletionTimestamp")
	ignoreKey("metadata", "deletionGracePeriodSeconds")
	ignoreKey("metadata", "managedFields")
	ignoreKey("metadata", "finalizers")
	ignoreKey("metadata", "selfLink")
	ignoreKey("metadata", "ownerReferences")

	// filter annotations from annotations set by Kubernetes
	filterAnnotations := func(obj Manifest) {
		annotations := obj.GetAnnotations()
		if annotations != nil {
			for k := range annotations {
				if strings.Contains(k, "kubernetes.io/") {
					// kubernetes annotation, drop it
					delete(annotations, k)
				}
			}

			if len(annotations) == 0 {
				annotations = nil
			}

			obj.SetAnnotations(annotations)
		}
	}

	filterAnnotations(current)
	filterAnnotations(resp)

	if resp.GetKind() == "ServiceAccount" {
		ignoreKey("secrets") // injected by Kubernetes in ServiceAccount objects
	}

	x, err := k8syaml.Marshal(current)
	if err != nil {
		return "", err
	}

	y, err := k8syaml.Marshal(resp)
	if err != nil {
		return "", err
	}

	resourceID := fmt.Sprintf("%s/%s/%s", obj.GetObjectKind().GroupVersionKind(), obj.GetNamespace(), obj.GetName())

	edits := myers.ComputeEdits(span.URIFromPath(resourceID), string(x), string(y))
	diff := gotextdiff.ToUnified(resourceID, resourceID, string(x), edits)

	return fmt.Sprint(diff), nil
}
