// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at http://mozilla.org/MPL/2.0/.

// Package memory implements an in-memory inventory for unit tests.
package memory

import (
	"context"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"sigs.k8s.io/cli-utils/pkg/object"
)

// Inventory is an in-memory implementation of ssa.Inventory for unit tests.
type Inventory struct {
	id   string
	refs object.ObjMetadataSet
}

// NewInventory creates a new in-memory inventory.
func NewInventory(id string) *Inventory {
	return &Inventory{id: id}
}

func (i *Inventory) ID() string {
	return i.id
}

func (i *Inventory) Read(_ context.Context) (object.ObjMetadataSet, error) {
	return i.refs, nil
}

func (i *Inventory) Write(_ context.Context, refs object.ObjMetadataSet) error {
	i.refs = refs

	return nil
}

func (i *Inventory) GetPruneObjs(_ context.Context, objects object.UnstructuredSet) (object.UnstructuredSet, error) {
	currentMetas := make(object.ObjMetadataSet, 0, len(objects))
	for _, obj := range objects {
		currentMetas = append(currentMetas, object.ObjMetadata{
			Namespace: obj.GetNamespace(),
			Name:      obj.GetName(),
			GroupKind: obj.GroupVersionKind().GroupKind(),
		})
	}

	var pruneObjs object.UnstructuredSet

	for _, ref := range i.refs {
		if currentMetas.Contains(ref) {
			continue
		}

		obj := &unstructured.Unstructured{}
		obj.SetGroupVersionKind(schema.GroupVersionKind{
			Group: ref.GroupKind.Group,
			Kind:  ref.GroupKind.Kind,
		})
		obj.SetNamespace(ref.Namespace)
		obj.SetName(ref.Name)

		pruneObjs = append(pruneObjs, obj)
	}

	return pruneObjs, nil
}

func (i *Inventory) Delete(_ context.Context) error {
	i.refs = nil

	return nil
}
