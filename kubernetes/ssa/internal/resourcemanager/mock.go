// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at http://mozilla.org/MPL/2.0/.

// Package resourcemanager implements a mock resource manager for unit tests.
package resourcemanager

import (
	"context"
	"reflect"

	"github.com/fluxcd/cli-utils/pkg/object"
	fluxssa "github.com/fluxcd/pkg/ssa"
	"github.com/fluxcd/pkg/ssa/utils"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"

	"github.com/siderolabs/go-kubernetes/kubernetes/ssa"
)

type MockResourceManager interface {
	SetObjects(objects ...*unstructured.Unstructured)

	ssa.ResourceManager
}

// Mock is an in-memory implementation of ssa.ResourceManager for unit tests.
//
// It simulates a Kubernetes cluster by storing objects in a map.
type Mock struct {
	objects map[objKey]*unstructured.Unstructured
}

type objKey struct {
	Group     string
	Kind      string
	Namespace string
	Name      string
}

// NewMock creates a new mock resource manager with an empty object store.
func NewMock() *Mock {
	return &Mock{
		objects: make(map[objKey]*unstructured.Unstructured),
	}
}

// SetObjects pre-populates the store with the given objects.
func (m *Mock) SetObjects(objects ...*unstructured.Unstructured) {
	if m.objects == nil {
		m.objects = map[objKey]*unstructured.Unstructured{}
	}

	for _, obj := range objects {
		m.objects[keyFromObj(obj)] = obj.DeepCopy()
	}
}

// GetObject returns a stored object by key, or nil if not found.
func (m *Mock) GetObject(group, kind, namespace, name string) *unstructured.Unstructured {
	obj := m.objects[objKey{Group: group, Kind: kind, Namespace: namespace, Name: name}]
	if obj == nil {
		return nil
	}

	return obj.DeepCopy()
}

func (m *Mock) Apply(_ context.Context, obj *unstructured.Unstructured, _ fluxssa.ApplyOptions) (*fluxssa.ChangeSetEntry, error) {
	key := keyFromObj(obj)

	action := fluxssa.CreatedAction
	if _, exists := m.objects[key]; exists {
		action = fluxssa.ConfiguredAction
	}

	if m.objects == nil {
		m.objects = map[objKey]*unstructured.Unstructured{}
	}

	m.objects[key] = obj.DeepCopy()

	return entryFromObj(obj, action), nil
}

func (m *Mock) ApplyAllStaged(ctx context.Context, objects []*unstructured.Unstructured, opts fluxssa.ApplyOptions) (*fluxssa.ChangeSet, error) {
	cs := fluxssa.NewChangeSet()

	for _, obj := range objects {
		entry, err := m.Apply(ctx, obj, opts)
		if err != nil {
			return cs, err
		}

		cs.Add(*entry)
	}

	return cs, nil
}

func (m *Mock) Delete(_ context.Context, obj *unstructured.Unstructured, _ fluxssa.DeleteOptions) (*fluxssa.ChangeSetEntry, error) {
	key := keyFromObj(obj)

	if _, exists := m.objects[key]; !exists {
		return nil, apierrors.NewNotFound(schema.GroupResource{
			Group:    key.Group,
			Resource: key.Kind,
		}, key.Name)
	}

	delete(m.objects, key)

	return entryFromObj(obj, fluxssa.DeletedAction), nil
}

func (m *Mock) WaitForSetWithContext(ctx context.Context, set object.ObjMetadataSet, opts fluxssa.WaitOptions) error {
	panic("not implemented")
}

func (m *Mock) Diff(_ context.Context, obj *unstructured.Unstructured, _ fluxssa.DiffOptions) (
	*fluxssa.ChangeSetEntry, *unstructured.Unstructured, *unstructured.Unstructured, error,
) {
	key := keyFromObj(obj)

	existing, exists := m.objects[key]
	if !exists {
		return entryFromObj(obj, fluxssa.CreatedAction), nil, obj.DeepCopy(), nil
	}

	action := fluxssa.UnchangedAction
	if !reflect.DeepEqual(existing.Object, obj.Object) {
		action = fluxssa.ConfiguredAction
	}

	return entryFromObj(obj, action), existing.DeepCopy(), obj.DeepCopy(), nil
}

func keyFromObj(obj *unstructured.Unstructured) objKey {
	gvk := obj.GroupVersionKind()

	return objKey{
		Group:     gvk.Group,
		Kind:      gvk.Kind,
		Namespace: obj.GetNamespace(),
		Name:      obj.GetName(),
	}
}

func entryFromObj(obj *unstructured.Unstructured, action fluxssa.Action) *fluxssa.ChangeSetEntry {
	return &fluxssa.ChangeSetEntry{
		ObjMetadata: object.ObjMetadata{
			Namespace: obj.GetNamespace(),
			Name:      obj.GetName(),
			GroupKind: obj.GroupVersionKind().GroupKind(),
		},
		GroupVersion: obj.GetAPIVersion(),
		Subject:      utils.FmtUnstructured(obj),
		Action:       action,
	}
}
