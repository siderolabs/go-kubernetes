// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at http://mozilla.org/MPL/2.0/.

// Package object provides base types for handling Kubernetes resources.
package object

import (
	fluxobj "github.com/fluxcd/cli-utils/pkg/object"
	"k8s.io/apimachinery/pkg/runtime"
)

// ObjMetadata is a single object metadata.
type ObjMetadata = fluxobj.ObjMetadata

// ObjMetadataSet is a set of object metadata.
type ObjMetadataSet = fluxobj.ObjMetadataSet

// UnstructuredSet is a set of unstructured objects.
type UnstructuredSet = fluxobj.UnstructuredSet

// ParseObjMetadata parses the given string into an ObjMetadata struct.
func ParseObjMetadata(s string) (ObjMetadata, error) {
	return fluxobj.ParseObjMetadata(s)
}

// RuntimeToObjMeta extracts the object metadata information from a
// runtime.Object and returns it as ObjMetadata.
func RuntimeToObjMeta(obj runtime.Object) (ObjMetadata, error) {
	return fluxobj.RuntimeToObjMeta(obj)
}
