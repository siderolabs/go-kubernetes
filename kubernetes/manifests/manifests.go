// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at http://mozilla.org/MPL/2.0/.

// Package manifests provides support for syncing Talos bootstrap manifests.
package manifests

import "k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"

// Manifest is a generic Kubernetes object.
type Manifest = *unstructured.Unstructured
