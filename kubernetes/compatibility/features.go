// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at http://mozilla.org/MPL/2.0/.

package compatibility

import "github.com/blang/semver/v4"

const (
	kubeSchedulerPre131HealthzEndpoint = "/healthz"
)

// SupportsKubeletConfigContainerRuntimeEndpoint returns true if kubelet supports ContainerRuntimEndpoint in kubelet config.
func (v Version) SupportsKubeletConfigContainerRuntimeEndpoint() bool {
	// see https://github.com/kubernetes/kubernetes/pull/112136
	return semver.Version(v).GTE(semver.Version{Major: 1, Minor: 27})
}

// FeatureFlagSeccompDefaultEnabledByDefault returns true if a SeccompDefault feature flag is enabled by default.
func (v Version) FeatureFlagSeccompDefaultEnabledByDefault() bool {
	// see https://github.com/kubernetes/kubernetes/pull/110805
	return semver.Version(v).GTE(semver.Version{Major: 1, Minor: 25})
}

// KubeSchedulerConfigurationAPIVersion returns the API version of the kube-scheduler configuration.
func (v Version) KubeSchedulerConfigurationAPIVersion() string {
	// https://v1-25.docs.kubernetes.io/docs/reference/scheduling/config/
	// v1.25 and above supports v1
	if semver.Version(v).GTE(semver.Version{Major: 1, Minor: 25}) {
		return "kubescheduler.config.k8s.io/v1"
	}

	// see https://v1-24.docs.kubernetes.io/docs/reference/scheduling/config/
	return "kubescheduler.config.k8s.io/v1beta3"
}

// KubeSchedulerHealthLivenessEndpoint returns the liveness endpoint for the kube-scheduler health check.
func (v Version) KubeSchedulerHealthLivenessEndpoint() string {
	// https://github.com/kubernetes/kubernetes/pull/118148
	// v1.31 and above supports /livez
	if semver.Version(v).GTE(semver.Version{Major: 1, Minor: 31}) {
		return "/livez"
	}

	return kubeSchedulerPre131HealthzEndpoint
}

// KubeSchedulerHealthReadinessEndpoint returns the readiness endpoint for the kube-scheduler health check.
func (v Version) KubeSchedulerHealthReadinessEndpoint() string {
	// https://github.com/kubernetes/kubernetes/pull/118148
	// v1.31 and above supports /readyz
	if semver.Version(v).GTE(semver.Version{Major: 1, Minor: 31}) {
		return "/readyz"
	}

	return kubeSchedulerPre131HealthzEndpoint
}

// KubeSchedulerHealthStartupEndpoint returns the startup endpoint for the kube-scheduler health check.
func (v Version) KubeSchedulerHealthStartupEndpoint() string {
	// https://github.com/kubernetes/kubernetes/pull/118148
	// v1.31 and above supports /livez
	if semver.Version(v).GTE(semver.Version{Major: 1, Minor: 31}) {
		return "/livez"
	}

	return kubeSchedulerPre131HealthzEndpoint
}
