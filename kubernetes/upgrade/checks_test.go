// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at http://mozilla.org/MPL/2.0/.

package upgrade_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/cosi-project/runtime/pkg/state"
	"github.com/cosi-project/runtime/pkg/state/impl/inmem"
	"github.com/cosi-project/runtime/pkg/state/impl/namespaced"
	"github.com/siderolabs/talos/pkg/machinery/resources/k8s"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/siderolabs/go-kubernetes/kubernetes/upgrade"
)

func TestK8sComponentRemovedItemsNoError(t *testing.T) {
	ctx, ctxCancel := context.WithTimeout(t.Context(), 3*time.Minute)
	defer ctxCancel()

	resourceState := state.WrapCore(namespaced.NewState(inmem.Build))

	for _, id := range []string{k8s.APIServerID, k8s.ControllerManagerID, k8s.SchedulerID} {
		cfg := k8s.NewStaticPod(k8s.NamespaceName, id)
		cfg.TypedSpec().Pod = map[string]any{
			"spec": map[string]any{
				"containers": []any{
					map[string]any{
						"command": []string{
							"/usr/local/bin/" + id,
						},
					},
				},
			},
		}

		require.NoError(t, resourceState.Create(ctx, cfg))
	}

	path, err := upgrade.NewPath("1.24.3", "1.25.0")
	require.NoError(t, err)

	checks, err := upgrade.NewChecks(path, resourceState, nil, []string{"10.5.0.2"}, nil, t.Logf)
	require.NoError(t, err)

	checkErrors := checks.Run(ctx)
	assert.NoError(t, checkErrors)
}

func TestK8sComponentRemovedItemsWithError(t *testing.T) {
	ctx, ctxCancel := context.WithTimeout(t.Context(), 3*time.Minute)
	defer ctxCancel()

	resourceState := state.WrapCore(namespaced.NewState(inmem.Build))

	checkData := map[string]struct {
		cliFlags []string
	}{
		k8s.APIServerID: {
			cliFlags: []string{
				"/usr/local/bin/kube-apiserver",
				"--bind-address=0.0.0.0",
				"--insecure-port=0",
				"--feature-gates=RotateKubeletServerCertificate=true,CSIVolumeFSGroupPolicy=true",
				"--enable-admission-plugins=NodeRestriction,PodSecurityPolicy",
				"--service-account-api-audiences=api",
			},
		},
		k8s.ControllerManagerID: {
			cliFlags: []string{
				"/usr/local/bin/kube-controller-manager",
				"--bind-address=0.0.0.0",
				"--insecure-port=0",
				"--feature-gates=RotateKubeletServerCertificate=true,CSIVolumeFSGroupPolicy=true",
				"--register-retry-count=100",
			},
		},
		k8s.SchedulerID: {
			cliFlags: []string{
				"/usr/local/bin/kube-scheduler",
				"--bind-address=0.0.0.0",
				"--insecure-port=0",
				"--feature-gates=RotateKubeletServerCertificate=true,CSIVolumeFSGroupPolicy=true",
			},
		},
	}

	expected := upgrade.ComponentRemovedItemsError{
		AdmissionFlags: []upgrade.ComponentItem{
			{
				Node:      "10.5.0.2",
				Component: "kube-apiserver",
				Value:     "PodSecurityPolicy",
			},
		},
		CLIFlags: []upgrade.ComponentItem{
			{
				Node:      "10.5.0.2",
				Component: "kube-apiserver",
				Value:     "service-account-api-audiences",
			},
			{
				Node:      "10.5.0.2",
				Component: "kube-controller-manager",
				Value:     "register-retry-count",
			},
		},
		FeatureGates: []upgrade.ComponentItem{
			{
				Node:      "10.5.0.2",
				Component: "kube-apiserver",
				Value:     "CSIVolumeFSGroupPolicy",
			},
			{
				Node:      "10.5.0.2",
				Component: "kube-controller-manager",
				Value:     "CSIVolumeFSGroupPolicy",
			},
			{
				Node:      "10.5.0.2",
				Component: "kube-scheduler",
				Value:     "CSIVolumeFSGroupPolicy",
			},
		},
	}

	for _, id := range []string{k8s.APIServerID, k8s.ControllerManagerID, k8s.SchedulerID} {
		cfg := k8s.NewStaticPod(k8s.NamespaceName, id)
		cfg.TypedSpec().Pod = map[string]any{
			"spec": map[string]any{
				"containers": []any{
					map[string]any{
						"command": checkData[id].cliFlags,
					},
				},
			},
		}

		require.NoError(t, resourceState.Create(ctx, cfg))
	}

	path, err := upgrade.NewPath("1.24.3", "1.25.0")
	require.NoError(t, err)

	checks, err := upgrade.NewChecks(path, resourceState, nil, []string{"10.5.0.2"}, []string{"10.5.0.3"}, t.Logf)
	require.NoError(t, err)

	checkErrors := checks.Run(ctx)

	var removedItemsError upgrade.ComponentRemovedItemsError
	if !errors.As(checkErrors, &removedItemsError) {
		t.Fatal("expected K8sComponentRemovedItemsError")
	}

	assert.Equal(t, expected, removedItemsError)
}

func TestK8sComponentRemovedItemsWithKubeletError(t *testing.T) {
	ctx, ctxCancel := context.WithTimeout(t.Context(), 3*time.Minute)
	defer ctxCancel()

	resourceState := state.WrapCore(namespaced.NewState(inmem.Build))

	checkData := map[string]struct {
		cliFlags []string
	}{
		k8s.APIServerConfigID: {
			cliFlags: []string{
				"/usr/local/bin/kube-apiserver",
				"--bind-address=0.0.0.0",
				"--insecure-port=0",
				"--feature-gates=ExpandCSIVolumes=true,StatefulSetMinReadySeconds=true",
			},
		},
		k8s.ControllerManagerID: {
			cliFlags: []string{
				"/usr/local/bin/kube-controller-manager",
				"--bind-address=0.0.0.0",
				"--insecure-port=0",
				"--feature-gates=ExpandCSIVolumes=true,StatefulSetMinReadySeconds=true",
				"--pod-eviction-timeout=100s",
				"--enable-taint-manager",
			},
		},
		k8s.SchedulerID: {
			cliFlags: []string{
				"/usr/local/bin/kube-scheduler",
				"--bind-address=0.0.0.0",
				"--insecure-port=0",
				"--feature-gates=ExpandCSIVolumes=true,StatefulSetMinReadySeconds=true",
			},
		},
	}

	expected := upgrade.ComponentRemovedItemsError{
		CLIFlags: []upgrade.ComponentItem{
			{
				Node:      "10.5.0.2",
				Component: "kube-controller-manager",
				Value:     "enable-taint-manager",
			},
			{
				Node:      "10.5.0.2",
				Component: "kube-controller-manager",
				Value:     "pod-eviction-timeout",
			},
			{
				Node:      "10.5.0.2",
				Component: "kubelet",
				Value:     "container-runtime",
			},
			{
				Node:      "10.5.0.2",
				Component: "kubelet",
				Value:     "master-service-namespace",
			},
			{
				Node:      "10.5.0.3",
				Component: "kubelet",
				Value:     "container-runtime",
			},
			{
				Node:      "10.5.0.3",
				Component: "kubelet",
				Value:     "master-service-namespace",
			},
		},
		FeatureGates: []upgrade.ComponentItem{
			{
				Node:      "10.5.0.2",
				Component: "kube-apiserver",
				Value:     "ExpandCSIVolumes",
			},
			{
				Node:      "10.5.0.2",
				Component: "kube-apiserver",
				Value:     "StatefulSetMinReadySeconds",
			},
			{
				Node:      "10.5.0.2",
				Component: "kube-controller-manager",
				Value:     "ExpandCSIVolumes",
			},
			{
				Node:      "10.5.0.2",
				Component: "kube-controller-manager",
				Value:     "StatefulSetMinReadySeconds",
			},
			{
				Node:      "10.5.0.2",
				Component: "kube-scheduler",
				Value:     "ExpandCSIVolumes",
			},
			{
				Node:      "10.5.0.2",
				Component: "kube-scheduler",
				Value:     "StatefulSetMinReadySeconds",
			},
		},
	}

	for _, id := range []string{k8s.APIServerID, k8s.ControllerManagerID, k8s.SchedulerID} {
		cfg := k8s.NewStaticPod(k8s.NamespaceName, id)
		cfg.TypedSpec().Pod = map[string]any{
			"spec": map[string]any{
				"containers": []any{
					map[string]any{
						"command": checkData[id].cliFlags,
					},
				},
			},
		}

		require.NoError(t, resourceState.Create(ctx, cfg))
	}

	cfg := k8s.NewKubeletSpec(k8s.NamespaceName, k8s.KubeletID)
	cfg.TypedSpec().Args = []string{
		"--container-runtime=containerd",
		"--container-runtime-endpoint=unix:///run/containerd/containerd.sock",
		"--hostname-override=talos-default-worker-1",
		"--kubeconfig=/etc/kubernetes/kubeconfig-kubelet",
		"--master-service-namespace=default",
		"--node-ip=10.5.0.3",
	}

	require.NoError(t, resourceState.Create(ctx, cfg))

	path, err := upgrade.NewPath("1.26.3", "1.27.0")
	require.NoError(t, err)

	checks, err := upgrade.NewChecks(path, resourceState, nil, []string{"10.5.0.2"}, []string{"10.5.0.3"}, t.Logf)
	require.NoError(t, err)

	checkErrors := checks.Run(ctx)

	var removedItemsError upgrade.ComponentRemovedItemsError
	if !errors.As(checkErrors, &removedItemsError) {
		t.Fatal("expected K8sComponentRemovedItemsError")
	}

	assert.Equal(t, expected, removedItemsError)
}
