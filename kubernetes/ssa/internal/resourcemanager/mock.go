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
	"k8s.io/apimachinery/pkg/api/meta"
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
// It tracks known GroupVersionKinds and rejects resources whose kind
// is not registered (either as a built-in or via a CRD apply).
type Mock struct {
	objects   map[objKey]*unstructured.Unstructured
	knownGVKs map[schema.GroupKind]bool
}

type objKey struct {
	Group     string
	Kind      string
	Namespace string
	Name      string
}

// builtinGroupKinds are the core Kubernetes types that are always available.
var builtinGroupKinds = []schema.GroupKind{
	{Group: "", Kind: "ConfigMap"},
	{Group: "", Kind: "Secret"},
	{Group: "", Kind: "Service"},
	{Group: "", Kind: "ServiceAccount"},
	{Group: "", Kind: "Namespace"},
	{Group: "", Kind: "Pod"},
	{Group: "apps", Kind: "Deployment"},
	{Group: "apps", Kind: "DaemonSet"},
	{Group: "apps", Kind: "StatefulSet"},
	{Group: "apps", Kind: "ReplicaSet"},
	{Group: "batch", Kind: "Job"},
	{Group: "batch", Kind: "CronJob"},
	{Group: "rbac.authorization.k8s.io", Kind: "Role"},
	{Group: "rbac.authorization.k8s.io", Kind: "RoleBinding"},
	{Group: "rbac.authorization.k8s.io", Kind: "ClusterRole"},
	{Group: "rbac.authorization.k8s.io", Kind: "ClusterRoleBinding"},
	{Group: "apiextensions.k8s.io", Kind: "CustomResourceDefinition"},
}

// NewMock creates a new mock resource manager with an empty object store.
func NewMock() *Mock {
	known := make(map[schema.GroupKind]bool, len(builtinGroupKinds))
	for _, gk := range builtinGroupKinds {
		known[gk] = true
	}

	return &Mock{
		objects:   make(map[objKey]*unstructured.Unstructured),
		knownGVKs: known,
	}
}

// checkKnownKind returns a NoKindMatchError if the object's GroupKind is not registered.
// If knownGVKs is nil (e.g. when Mock is embedded without using NewMock), the check is skipped.
func (m *Mock) checkKnownKind(obj *unstructured.Unstructured) error {
	if m.knownGVKs == nil {
		return nil
	}

	gvk := obj.GroupVersionKind()
	gk := gvk.GroupKind()

	if m.knownGVKs[gk] {
		return nil
	}

	return &meta.NoKindMatchError{
		GroupKind:        gk,
		SearchedVersions: []string{gvk.Version},
	}
}

// registerCRD extracts the custom resource GroupKind from a CRD object and registers it.
func (m *Mock) registerCRD(obj *unstructured.Unstructured) {
	gvk := obj.GroupVersionKind()
	if gvk.Group != "apiextensions.k8s.io" || gvk.Kind != "CustomResourceDefinition" {
		return
	}

	spec, ok := obj.Object["spec"].(map[string]any)
	if !ok {
		return
	}

	group, ok := spec["group"].(string)
	if !ok {
		return
	}

	names, ok := spec["names"].(map[string]any)
	if !ok {
		return
	}

	kind, ok := names["kind"].(string)
	if !ok {
		return
	}

	if group != "" && kind != "" {
		m.knownGVKs[schema.GroupKind{Group: group, Kind: kind}] = true
	}
}

func (m *Mock) Get(ctx context.Context, meta object.ObjMetadata) (*unstructured.Unstructured, error) {
	obj := m.objects[objKey{Group: meta.GroupKind.Group, Kind: meta.GroupKind.Kind, Namespace: meta.Namespace, Name: meta.Name}]
	if obj == nil {
		return nil, apierrors.NewNotFound(schema.GroupResource{
			Group:    meta.GroupKind.Group,
			Resource: meta.GroupKind.Kind,
		}, meta.Name)
	}

	return obj.DeepCopy(), nil
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
	if err := m.checkKnownKind(obj); err != nil {
		return nil, err
	}

	key := keyFromObj(obj)

	action := fluxssa.CreatedAction
	if _, exists := m.objects[key]; exists {
		action = fluxssa.ConfiguredAction
	}

	if m.objects == nil {
		m.objects = map[objKey]*unstructured.Unstructured{}
	}

	m.objects[key] = obj.DeepCopy()

	m.registerCRD(obj)

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
	if err := m.checkKnownKind(obj); err != nil {
		return nil, nil, nil, err
	}

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
		GroupVersion: obj.GroupVersionKind().Version,
		Subject:      utils.FmtUnstructured(obj),
		Action:       action,
	}
}
