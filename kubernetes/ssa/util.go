// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at http://mozilla.org/MPL/2.0/.

package ssa

import (
	"fmt"

	"github.com/fluxcd/cli-utils/pkg/object"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
)

// FormatObjectPath returns a string with the format <kind>/<namespace>/<name> for a given object.
func FormatObjectPath(obj *unstructured.Unstructured) string {
	kind := obj.GetObjectKind().GroupVersionKind().Kind
	name := obj.GetName()
	namespace := obj.GetNamespace()

	return formatPath(namespace, name, kind)
}

func formatPath(namespace string, name string, kind string) string {
	if namespace != "" {
		name = namespace + "/" + name
	}

	return fmt.Sprintf("%s/%s", kind, name)
}

// FormatObjectPathWithGV returns a string with the format <group/version>.<kind>/<namespace>/<name> for a given object.
func FormatObjectPathWithGV(obj *unstructured.Unstructured) string {
	version := obj.GetObjectKind().GroupVersionKind().Version
	group := obj.GetObjectKind().GroupVersionKind().Group
	groupKind := obj.GetObjectKind().GroupVersionKind().Kind
	name := obj.GetName()
	namespace := obj.GetNamespace()

	return formatPathGV(version, group, namespace, name, groupKind)
}

func FormatObjectMetaPath(meta object.ObjMetadata, version string) string {
	return formatPathGV(version, meta.GroupKind.Group, meta.Namespace, meta.Name, meta.GroupKind.Kind)
}

func formatPathGV(version string, group string, namespace string, name string, groupKind string) string {
	if version == "" {
		version = "v1"
	}

	gv := version
	if group != "" {
		gv = group + "/" + gv
	}

	if namespace != "" {
		name = namespace + "/" + name
	}

	return fmt.Sprintf("%s.%s/%s", gv, groupKind, name)
}
