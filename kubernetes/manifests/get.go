// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at http://mozilla.org/MPL/2.0/.

package manifests

import (
	"context"

	"github.com/cosi-project/runtime/pkg/resource"
	"github.com/cosi-project/runtime/pkg/safe"
	"github.com/cosi-project/runtime/pkg/state"
	"github.com/siderolabs/talos/pkg/machinery/resources/k8s"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
)

// GetBootstrapManifests fetches the bootstrap manifests from the cluster.
func GetBootstrapManifests(ctx context.Context, st state.State, filter func(Manifest) bool) ([]Manifest, error) {
	items, err := safe.StateList[*k8s.Manifest](ctx, st, resource.NewMetadata(k8s.ControlPlaneNamespaceName, k8s.ManifestType, "", resource.VersionUndefined))
	if err != nil {
		return nil, err
	}

	it := safe.IteratorFromList(items)

	objects := []Manifest{}

	for it.Next() {
		for _, o := range it.Value().TypedSpec().Items {
			obj := &unstructured.Unstructured{Object: o.Object}

			if filter != nil && !filter(obj) {
				continue
			}

			objects = append(objects, obj)
		}
	}

	return objects, nil
}
