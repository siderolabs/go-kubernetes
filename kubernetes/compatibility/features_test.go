// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at http://mozilla.org/MPL/2.0/.

package compatibility_test

import (
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/siderolabs/go-kubernetes/kubernetes/compatibility"
)

func TestFeatures(t *testing.T) {
	for _, test := range []struct { //nolint:govet
		versions []compatibility.Version

		expectedSupportsKubeletConfigContainerRuntimeEndpoint bool
		expectedFeatureFlagSeccompDefaultEnabledByDefault     bool
		expectedKubeSchedulerConfigurationAPIVersion          string
	}{
		{
			versions: []compatibility.Version{
				{Major: 1, Minor: 24},
			},

			expectedSupportsKubeletConfigContainerRuntimeEndpoint: false,
			expectedFeatureFlagSeccompDefaultEnabledByDefault:     false,
			expectedKubeSchedulerConfigurationAPIVersion:          "kubescheduler.config.k8s.io/v1beta3",
		},
		{
			versions: []compatibility.Version{
				{Major: 1, Minor: 25},
				{Major: 1, Minor: 26},
			},
			expectedSupportsKubeletConfigContainerRuntimeEndpoint: false,
			expectedFeatureFlagSeccompDefaultEnabledByDefault:     true,
			expectedKubeSchedulerConfigurationAPIVersion:          "kubescheduler.config.k8s.io/v1",
		},
		{
			versions: []compatibility.Version{
				{Major: 1, Minor: 27},
				{Major: 1, Minor: 28},
				{Major: 1, Minor: 29},
				{Major: 1, Minor: 99},
			},
			expectedSupportsKubeletConfigContainerRuntimeEndpoint: true,
			expectedFeatureFlagSeccompDefaultEnabledByDefault:     true,
			expectedKubeSchedulerConfigurationAPIVersion:          "kubescheduler.config.k8s.io/v1",
		},
	} {
		for _, version := range test.versions {
			t.Run(version.String(), func(t *testing.T) {
				assert.Equal(t, test.expectedSupportsKubeletConfigContainerRuntimeEndpoint, version.SupportsKubeletConfigContainerRuntimeEndpoint())
				assert.Equal(t, test.expectedFeatureFlagSeccompDefaultEnabledByDefault, version.FeatureFlagSeccompDefaultEnabledByDefault())
			})
		}
	}
}
