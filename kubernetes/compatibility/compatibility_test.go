// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at http://mozilla.org/MPL/2.0/.

package compatibility_test

import (
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/siderolabs/go-kubernetes/kubernetes/compatibility"
)

func TestVersionFromImageRef(t *testing.T) {
	for _, test := range []struct {
		name     string
		imageRef string

		expectedVersion compatibility.Version
	}{
		{
			name:     "just tag",
			imageRef: "k8s.gcr.io/kube-apiserver:v1.18.0",

			expectedVersion: compatibility.Version{Major: 1, Minor: 18},
		},
		{
			name:     "just tag 2",
			imageRef: "ghcr.io/siderolabs/kubelet:v1.27.9",

			expectedVersion: compatibility.Version{Major: 1, Minor: 27, Patch: 9},
		},
		{
			name:     "tag and digest",
			imageRef: "ghcr.io/siderolabs/kubelet:v1.27.9@sha256:3f226f5b385960e311f19d6c9c3ea1778e86e6ad2e98a7bbbf1b1a63fe963916",

			expectedVersion: compatibility.Version{Major: 1, Minor: 27, Patch: 9},
		},
		{
			name:     "only digest",
			imageRef: "ghcr.io/siderolabs/kubelet@sha256:3f226f5b385960e311f19d6c9c3ea1778e86e6ad2e98a7bbbf1b1a63fe963916",

			expectedVersion: compatibility.Version{Major: 1, Minor: 99},
		},
		{
			name:     "no tag or digest",
			imageRef: "ghcr.io/siderolabs/kubelet",

			expectedVersion: compatibility.Version{Major: 1, Minor: 99},
		},
		{
			name:     "invalid version",
			imageRef: "ghcr.io/siderolabs/kubelet:alpha",

			expectedVersion: compatibility.Version{Major: 1, Minor: 99},
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			actualVersion := compatibility.VersionFromImageRef(test.imageRef)
			assert.Equal(t, test.expectedVersion, actualVersion)
		})
	}
}
