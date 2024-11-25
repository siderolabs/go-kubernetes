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

		expectedSupportsKubeletConfigContainerRuntimeEndpoint                   bool
		expectedFeatureFlagSeccompDefaultEnabledByDefault                       bool
		expectedKubeAPIServerSupportsAuthorizationConfigFile                    bool
		expectedFeatureFlagStructuredAuthorizationConfigurationEnabledByDefault bool
		expectedKubeSchedulerConfigurationAPIVersion                            string
		expectedKubeSchedulerLivenessEndpoint                                   string
		expectedKubeSchedulerReadinessEndpoint                                  string
		expectedKubeSchedulerStartupEndpoint                                    string
	}{
		{
			versions: []compatibility.Version{
				{Major: 1, Minor: 24},
			},

			expectedSupportsKubeletConfigContainerRuntimeEndpoint:                   false,
			expectedFeatureFlagSeccompDefaultEnabledByDefault:                       false,
			expectedKubeAPIServerSupportsAuthorizationConfigFile:                    false,
			expectedFeatureFlagStructuredAuthorizationConfigurationEnabledByDefault: false,
			expectedKubeSchedulerConfigurationAPIVersion:                            "kubescheduler.config.k8s.io/v1beta3",
			expectedKubeSchedulerLivenessEndpoint:                                   "/healthz",
			expectedKubeSchedulerReadinessEndpoint:                                  "/healthz",
			expectedKubeSchedulerStartupEndpoint:                                    "/healthz",
		},
		{
			versions: []compatibility.Version{
				{Major: 1, Minor: 25},
				{Major: 1, Minor: 26},
			},
			expectedSupportsKubeletConfigContainerRuntimeEndpoint:                   false,
			expectedFeatureFlagSeccompDefaultEnabledByDefault:                       true,
			expectedKubeAPIServerSupportsAuthorizationConfigFile:                    false,
			expectedFeatureFlagStructuredAuthorizationConfigurationEnabledByDefault: false,
			expectedKubeSchedulerConfigurationAPIVersion:                            "kubescheduler.config.k8s.io/v1",
			expectedKubeSchedulerLivenessEndpoint:                                   "/healthz",
			expectedKubeSchedulerReadinessEndpoint:                                  "/healthz",
			expectedKubeSchedulerStartupEndpoint:                                    "/healthz",
		},
		{
			versions: []compatibility.Version{
				{Major: 1, Minor: 27},
				{Major: 1, Minor: 28},
			},
			expectedSupportsKubeletConfigContainerRuntimeEndpoint:                   true,
			expectedFeatureFlagSeccompDefaultEnabledByDefault:                       true,
			expectedKubeAPIServerSupportsAuthorizationConfigFile:                    false,
			expectedFeatureFlagStructuredAuthorizationConfigurationEnabledByDefault: false,
			expectedKubeSchedulerConfigurationAPIVersion:                            "kubescheduler.config.k8s.io/v1",
			expectedKubeSchedulerLivenessEndpoint:                                   "/healthz",
			expectedKubeSchedulerReadinessEndpoint:                                  "/healthz",
			expectedKubeSchedulerStartupEndpoint:                                    "/healthz",
		},
		{
			versions: []compatibility.Version{
				{Major: 1, Minor: 29},
			},
			expectedSupportsKubeletConfigContainerRuntimeEndpoint:                   true,
			expectedFeatureFlagSeccompDefaultEnabledByDefault:                       true,
			expectedKubeAPIServerSupportsAuthorizationConfigFile:                    true,
			expectedFeatureFlagStructuredAuthorizationConfigurationEnabledByDefault: false,
			expectedKubeSchedulerConfigurationAPIVersion:                            "kubescheduler.config.k8s.io/v1",
			expectedKubeSchedulerLivenessEndpoint:                                   "/healthz",
			expectedKubeSchedulerReadinessEndpoint:                                  "/healthz",
			expectedKubeSchedulerStartupEndpoint:                                    "/healthz",
		},
		{
			versions: []compatibility.Version{
				{Major: 1, Minor: 30},
			},
			expectedSupportsKubeletConfigContainerRuntimeEndpoint:                   true,
			expectedFeatureFlagSeccompDefaultEnabledByDefault:                       true,
			expectedKubeAPIServerSupportsAuthorizationConfigFile:                    true,
			expectedFeatureFlagStructuredAuthorizationConfigurationEnabledByDefault: true,
			expectedKubeSchedulerConfigurationAPIVersion:                            "kubescheduler.config.k8s.io/v1",
			expectedKubeSchedulerLivenessEndpoint:                                   "/healthz",
			expectedKubeSchedulerReadinessEndpoint:                                  "/healthz",
			expectedKubeSchedulerStartupEndpoint:                                    "/healthz",
		},
		{
			versions: []compatibility.Version{
				{Major: 1, Minor: 31},
				{Major: 1, Minor: 99},
			},
			expectedSupportsKubeletConfigContainerRuntimeEndpoint:                   true,
			expectedFeatureFlagSeccompDefaultEnabledByDefault:                       true,
			expectedKubeAPIServerSupportsAuthorizationConfigFile:                    true,
			expectedFeatureFlagStructuredAuthorizationConfigurationEnabledByDefault: true,
			expectedKubeSchedulerConfigurationAPIVersion:                            "kubescheduler.config.k8s.io/v1",
			expectedKubeSchedulerLivenessEndpoint:                                   "/livez",
			expectedKubeSchedulerReadinessEndpoint:                                  "/readyz",
			expectedKubeSchedulerStartupEndpoint:                                    "/livez",
		},
	} {
		for _, version := range test.versions {
			t.Run(version.String(), func(t *testing.T) {
				assert.Equal(t, test.expectedSupportsKubeletConfigContainerRuntimeEndpoint, version.SupportsKubeletConfigContainerRuntimeEndpoint())
				assert.Equal(t, test.expectedFeatureFlagSeccompDefaultEnabledByDefault, version.FeatureFlagSeccompDefaultEnabledByDefault())
				assert.Equal(t, test.expectedKubeAPIServerSupportsAuthorizationConfigFile, version.KubeAPIServerSupportsAuthorizationConfigFile())
				assert.Equal(t, test.expectedFeatureFlagStructuredAuthorizationConfigurationEnabledByDefault, version.FeatureFlagStructuredAuthorizationConfigurationEnabledByDefault())
				assert.Equal(t, test.expectedKubeSchedulerConfigurationAPIVersion, version.KubeSchedulerConfigurationAPIVersion())
				assert.Equal(t, test.expectedKubeSchedulerLivenessEndpoint, version.KubeSchedulerHealthLivenessEndpoint())
				assert.Equal(t, test.expectedKubeSchedulerReadinessEndpoint, version.KubeSchedulerHealthReadinessEndpoint())
			})
		}
	}
}
