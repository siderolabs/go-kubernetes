// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at http://mozilla.org/MPL/2.0/.

package manifests

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/siderolabs/gen/channel"
	"github.com/siderolabs/go-retry/retry"
	"github.com/siderolabs/talos/pkg/machinery/textdiff"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/client-go/discovery"
	"k8s.io/client-go/discovery/cached/memory"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/restmapper"
	k8syaml "sigs.k8s.io/yaml"

	"github.com/siderolabs/go-kubernetes/kubernetes"
)

// SyncResult describes the result of a single manifest sync.
type SyncResult struct {
	Path    string
	Object  Manifest
	Diff    string
	Skipped bool
}

// Sync applies the manifests to the cluster providing the results.
func Sync(ctx context.Context, objects []Manifest, config *rest.Config, dryRun bool, resultCh chan<- SyncResult) error {
	httpClient, err := rest.HTTPClientFor(config)
	if err != nil {
		return fmt.Errorf("error creating HTTP client: %w", err)
	}

	defer httpClient.CloseIdleConnections()

	k8sClient, err := dynamic.NewForConfigAndClient(config, httpClient)
	if err != nil {
		return fmt.Errorf("error creating dynamic client: %w", err)
	}

	dc, err := discovery.NewDiscoveryClientForConfigAndClient(config, httpClient)
	if err != nil {
		return fmt.Errorf("error creating discovery client: %w", err)
	}

	mapper := restmapper.NewDeferredDiscoveryRESTMapper(memory.NewMemCacheClient(dc))

	for _, obj := range objects {
		var (
			resp    Manifest
			diff    string
			skipped bool
		)

		if err = retry.Constant(3*time.Minute, retry.WithUnits(10*time.Second), retry.WithErrorLogging(true)).RetryWithContext(ctx, func(ctx context.Context) error {
			resp, diff, skipped, err = updateManifest(ctx, mapper, k8sClient, obj, dryRun)
			if kubernetes.IsRetryableError(err) || apierrors.IsConflict(err) {
				return retry.ExpectedError(err)
			}

			return err
		}); err != nil {
			return err
		}

		if !channel.SendWithContext(ctx, resultCh, SyncResult{
			Path:    getManifestPath(resp),
			Object:  resp,
			Diff:    diff,
			Skipped: skipped,
		}) {
			return ctx.Err()
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
	}

	switch {
	case dryRun:
		return obj, diff, diff == "", nil
	case !exists:
		resp, err = dr.Create(ctx, obj, metav1.CreateOptions{})
	case diff != "":
		resp, err = dr.Update(ctx, obj, metav1.UpdateOptions{})
	default:
		skipped = true
		resp = obj
	}

	return resp, diff, skipped, err
}

func getResourceDiff(ctx context.Context, dr dynamic.ResourceInterface, obj Manifest) (string, error) {
	current, err := dr.Get(ctx, obj.GetName(), metav1.GetOptions{})
	if err != nil {
		if apierrors.IsNotFound(err) {
			diff, diffErr := manifestDiff(nil, obj)
			if diffErr != nil {
				return "", diffErr
			}

			return diff, err
		}

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

	return manifestDiff(current, resp)
}

func getManifestPath(obj Manifest) string {
	version := obj.GetObjectKind().GroupVersionKind().Version
	group := obj.GetObjectKind().GroupVersionKind().Group
	groupKind := obj.GetObjectKind().GroupVersionKind().Kind
	name := obj.GetName()
	namespace := obj.GetNamespace()

	return formatManifestPath(version, group, namespace, name, groupKind)
}

func formatManifestPath(version string, group string, namespace string, name string, groupKind string) string {
	gv := version
	if group != "" {
		gv = group + "/" + gv
	}

	if namespace != "" {
		name = namespace + "/" + name
	}

	return fmt.Sprintf("%s.%s/%s", gv, groupKind, name)
}

func manifestDiff(a, b Manifest) (string, error) {
	var (
		ma, mb []byte
		path   string
		err    error
	)

	if a != nil {
		path = getManifestPath(a)

		ma, err = k8syaml.Marshal(a)
		if err != nil {
			return "", err
		}
	}

	if b != nil {
		path = getManifestPath(b)

		mb, err = k8syaml.Marshal(b)
		if err != nil {
			return "", err
		}
	}

	return computeDiff(path, string(ma), string(mb))
}

func computeDiff(path string, a, b string) (string, error) {
	return textdiff.DiffWithCustomPaths(a, b, "a/"+path, "b/"+path)
}
