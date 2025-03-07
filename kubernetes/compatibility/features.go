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

// KubeAPIServerSupportsAuthorizationConfigFile returns true if kube-apiserver supports authorization config file.
func (v Version) KubeAPIServerSupportsAuthorizationConfigFile() bool {
	// https://v1-29.docs.kubernetes.io/docs/reference/access-authn-authz/authorization/#configuring-the-api-server-using-an-authorization-config-file
	// v1.29 and above supports authorization config file
	return semver.Version(v).GTE(semver.Version{Major: 1, Minor: 29})
}

// FeatureFlagStructuredAuthorizationConfigurationEnabledByDefault returns true if structured authorization configuration is enabled by default.
func (v Version) FeatureFlagStructuredAuthorizationConfigurationEnabledByDefault() bool {
	// https://v1-29.docs.kubernetes.io/docs/reference/access-authn-authz/authorization/#configuring-the-api-server-using-an-authorization-config-file
	// https://v1-30.docs.kubernetes.io/docs/reference/access-authn-authz/authorization/#using-configuration-file-for-authorization
	// v1.30 and above enables structured authorization configuration by default
	return semver.Version(v).GTE(semver.Version{Major: 1, Minor: 30})
}

// KubeAPIServerAuthorizationConfigAPIVersion returns the API version of the kube-apiserver authorization config.
func (v Version) KubeAPIServerAuthorizationConfigAPIVersion() string {
	// https://v1-30.docs.kubernetes.io/docs/reference/access-authn-authz/authorization/#using-configuration-file-for-authorization
	// v1.30 and above supports v1beta1
	if semver.Version(v).GTE(semver.Version{Major: 1, Minor: 30}) {
		return "apiserver.config.k8s.io/v1beta1"
	}

	// see https://v1-29.docs.kubernetes.io/docs/reference/access-authn-authz/authorization/#configuring-the-api-server-using-an-authorization-config-file
	return "apiserver.config.k8s.io/v1alpha1"
}

// CloudProviderFlagRemoved returns true if the cloud provider flag is removed.
func (v Version) CloudProviderFlagRemoved() bool {
	// See https://github.com/kubernetes/kubernetes/pull/130162
	// v1.33 and above removes the cloud provider flag
	return semver.Version(v).GTE(semver.Version{Major: 1, Minor: 33})
}
