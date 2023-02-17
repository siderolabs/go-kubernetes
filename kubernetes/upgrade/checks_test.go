// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at http://mozilla.org/MPL/2.0/.

package upgrade_test

import (
	"context"
	"errors"
	"fmt"
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
	ctx, ctxCancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer ctxCancel()

	resourceState := state.WrapCore(namespaced.NewState(inmem.Build))

	for _, id := range []string{k8s.APIServerID, k8s.ControllerManagerID, k8s.SchedulerID} {
		cfg := k8s.NewStaticPod(k8s.NamespaceName, id)
		cfg.TypedSpec().Pod = map[string]interface{}{
			"spec": map[string]interface{}{
				"containers": []interface{}{
					map[string]interface{}{
						"command": []string{
							fmt.Sprintf("/usr/local/bin/%s", id),
						},
					},
				},
			},
		}

		require.NoError(t, resourceState.Create(ctx, cfg))
	}

	path, err := upgrade.NewPath("1.24.3", "1.25.0")
	require.NoError(t, err)

	checks, err := upgrade.NewChecks(path, resourceState, nil, []string{"10.5.0.2"}, t.Logf)
	require.NoError(t, err)

	checkErrors := checks.Run(ctx)
	assert.NoError(t, checkErrors)
}

func TestK8sComponentRemovedItemsWithError(t *testing.T) {
	ctx, ctxCancel := context.WithTimeout(context.Background(), 3*time.Minute)
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
				"--feature-gates=RotateKubeletServerCertificate=true,CSIVolumeFSGroupPolicy",
				"--enable-admission-plugins=NodeRestriction,PodSecurityPolicy",
				"--service-account-api-audiences=api",
			},
		},
		k8s.ControllerManagerID: {
			cliFlags: []string{
				"/usr/local/bin/kube-controller-manager",
				"--bind-address=0.0.0.0",
				"--insecure-port=0",
				"--feature-gates=RotateKubeletServerCertificate=true,CSIVolumeFSGroupPolicy",
				"--register-retry-count=100",
			},
		},
		k8s.SchedulerID: {
			cliFlags: []string{
				"/usr/local/bin/kube-scheduler",
				"--bind-address=0.0.0.0",
				"--insecure-port=0",
				"--feature-gates=RotateKubeletServerCertificate=true,CSIVolumeFSGroupPolicy",
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
		cfg.TypedSpec().Pod = map[string]interface{}{
			"spec": map[string]interface{}{
				"containers": []interface{}{
					map[string]interface{}{
						"command": checkData[id].cliFlags,
					},
				},
			},
		}

		require.NoError(t, resourceState.Create(ctx, cfg))
	}

	path, err := upgrade.NewPath("1.24.3", "1.25.0")
	require.NoError(t, err)

	checks, err := upgrade.NewChecks(path, resourceState, nil, []string{"10.5.0.2"}, t.Logf)
	require.NoError(t, err)

	checkErrors := checks.Run(ctx)

	var removedItemsError upgrade.ComponentRemovedItemsError

	if !errors.As(checkErrors, &removedItemsError) {
		t.Fatal("expected K8sComponentRemovedItemsError")
	}

	assert.Equal(t, expected, removedItemsError)
}
