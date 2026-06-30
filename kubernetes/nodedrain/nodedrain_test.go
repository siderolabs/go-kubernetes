// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at http://mozilla.org/MPL/2.0/.

package nodedrain_test

import (
	"context"
	"errors"
	"slices"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/kubernetes/fake"
	ktesting "k8s.io/client-go/testing"

	"github.com/siderolabs/go-kubernetes/kubernetes/nodedrain"
)

const testNode = "worker-1"

func node(unschedulable bool) *corev1.Node {
	return &corev1.Node{
		ObjectMeta: metav1.ObjectMeta{Name: testNode},
		Spec:       corev1.NodeSpec{Unschedulable: unschedulable},
	}
}

func TestCordon(t *testing.T) {
	t.Parallel()

	ctx := t.Context()
	clientset := fake.NewSimpleClientset(node(false))

	require.NoError(t, nodedrain.Cordon(ctx, clientset, testNode))

	got, err := clientset.CoreV1().Nodes().Get(ctx, testNode, metav1.GetOptions{})
	require.NoError(t, err)

	assert.True(t, got.Spec.Unschedulable)
}

func TestUncordon(t *testing.T) {
	t.Parallel()

	t.Run("clears the unschedulable flag", func(t *testing.T) {
		t.Parallel()

		ctx := t.Context()
		clientset := fake.NewSimpleClientset(node(true))

		require.NoError(t, nodedrain.Uncordon(ctx, clientset, testNode))

		got, err := clientset.CoreV1().Nodes().Get(ctx, testNode, metav1.GetOptions{})
		require.NoError(t, err)

		assert.False(t, got.Spec.Unschedulable)
	})

	t.Run("missing node is a no-op", func(t *testing.T) {
		t.Parallel()

		require.NoError(t, nodedrain.Uncordon(t.Context(), fake.NewSimpleClientset(), "ghost"))
	})
}

// drainClientset builds a fake clientset whose discovery advertises the eviction subresource, so the
// drain helper's eviction-support check passes and it actually exercises eviction.
func drainClientset(objects ...runtime.Object) *fake.Clientset {
	clientset := fake.NewSimpleClientset(objects...)
	clientset.Resources = []*metav1.APIResourceList{
		{
			GroupVersion: "v1",
			APIResources: []metav1.APIResource{
				{Name: "pods", Namespaced: true, Kind: "Pod"},
				{Name: "pods/eviction", Namespaced: true, Kind: "Eviction", Group: "policy", Version: "v1"},
			},
		},
	}

	return clientset
}

func workloadPod() *corev1.Pod {
	return &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{Namespace: "default", Name: "workload"},
		Spec:       corev1.PodSpec{NodeName: testNode},
		Status:     corev1.PodStatus{Phase: corev1.PodRunning},
	}
}

// progressRecorder collects progress messages from concurrent eviction callbacks.
type progressRecorder struct {
	messages []string
	mu       sync.Mutex
}

func (p *progressRecorder) record(msg string) {
	p.mu.Lock()
	defer p.mu.Unlock()

	p.messages = append(p.messages, msg)
}

func (p *progressRecorder) snapshot() []string {
	p.mu.Lock()
	defer p.mu.Unlock()

	return slices.Clone(p.messages)
}

func TestDrain(t *testing.T) {
	t.Parallel()

	t.Run("evicts pods and reports each outcome", func(t *testing.T) {
		t.Parallel()

		ctx, cancel := context.WithTimeout(t.Context(), 30*time.Second)
		t.Cleanup(cancel)

		clientset := drainClientset(node(true), workloadPod())

		// Emulate a successful eviction by deleting the pod, so the drain's wait-for-deletion completes.
		clientset.PrependReactor("create", "pods", func(action ktesting.Action) (bool, runtime.Object, error) {
			if action.GetSubresource() != "eviction" {
				return false, nil, nil
			}

			return true, nil, clientset.Tracker().Delete(corev1.SchemeGroupVersion.WithResource("pods"), "default", "workload")
		})

		var progress progressRecorder

		require.NoError(t, nodedrain.Drain(ctx, clientset, testNode, nodedrain.DrainOptions{Progress: progress.record}))

		messages := progress.snapshot()
		assert.Contains(t, messages, "evicting pod default/workload")
		assert.Contains(t, messages, "evicted pod default/workload")
	})

	// A refused eviction (what a PodDisruptionBudget causes) is surfaced through the returned error naming
	// the pod, not the finished-callback: the kubectl drain helper invokes that callback only on the
	// wait-for-termination path, while a failed eviction request returns directly.
	t.Run("returns an error naming the pod a disruption budget blocks", func(t *testing.T) {
		t.Parallel()

		ctx, cancel := context.WithTimeout(t.Context(), 30*time.Second)
		t.Cleanup(cancel)

		clientset := drainClientset(node(true), workloadPod())

		clientset.PrependReactor("create", "pods", func(action ktesting.Action) (bool, runtime.Object, error) {
			if action.GetSubresource() != "eviction" {
				return false, nil, nil
			}

			return true, nil, errors.New("eviction refused by disruption budget")
		})

		err := nodedrain.Drain(ctx, clientset, testNode, nodedrain.DrainOptions{})
		require.Error(t, err)
		assert.Contains(t, err.Error(), "workload", "the error should name the pod that could not be evicted")
	})

	t.Run("rejects a grace period below -1", func(t *testing.T) {
		t.Parallel()

		err := nodedrain.Drain(t.Context(), drainClientset(node(true)), testNode, nodedrain.DrainOptions{GracePeriodSeconds: -2})
		require.Error(t, err)
		assert.Contains(t, err.Error(), "GracePeriodSeconds")
	})
}

func readyNode() *corev1.Node {
	return &corev1.Node{
		ObjectMeta: metav1.ObjectMeta{Name: testNode},
		Status: corev1.NodeStatus{
			Conditions: []corev1.NodeCondition{
				{Type: corev1.NodeReady, Status: corev1.ConditionTrue},
			},
		},
	}
}

func TestWaitForNodeReady(t *testing.T) {
	t.Parallel()

	t.Run("returns once the node reports Ready", func(t *testing.T) {
		t.Parallel()

		ctx, cancel := context.WithTimeout(t.Context(), 30*time.Second)
		t.Cleanup(cancel)

		clientset := fake.NewSimpleClientset(readyNode())

		require.NoError(t, nodedrain.WaitForNodeReady(ctx, clientset, testNode, 30*time.Second))
	})

	// A Forbidden response will not resolve by waiting, so it must surface at once rather than stalling the
	// caller until the timeout elapses.
	t.Run("fails fast on a non-transient error", func(t *testing.T) {
		t.Parallel()

		clientset := fake.NewSimpleClientset()
		clientset.PrependReactor("get", "nodes", func(ktesting.Action) (bool, runtime.Object, error) {
			return true, nil, apierrors.NewForbidden(corev1.Resource("nodes"), testNode, errors.New("forbidden"))
		})

		err := nodedrain.WaitForNodeReady(t.Context(), clientset, testNode, 30*time.Second)
		require.Error(t, err)
		assert.True(t, apierrors.IsForbidden(err), "the Forbidden error should surface instead of a timeout")
	})
}
