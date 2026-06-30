// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at http://mozilla.org/MPL/2.0/.

// Package nodedrain provides reusable Kubernetes node cordon, drain, and uncordon operations for the
// client side of a Talos upgrade or reboot.
package nodedrain

import (
	"context"
	"fmt"
	"io"
	"time"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/wait"
	"k8s.io/client-go/kubernetes"
	"k8s.io/kubectl/pkg/drain"

	gokubernetes "github.com/siderolabs/go-kubernetes/kubernetes"
)

// DefaultDrainTimeout caps pod eviction when DrainOptions.Timeout is zero.
const DefaultDrainTimeout = 5 * time.Minute

// nodeReadyPollInterval is how often WaitForNodeReady polls.
const nodeReadyPollInterval = 5 * time.Second

// DrainOptions configures Drain. The zero value applies the canonical defaults: evict standalone pods,
// honor each pod's own terminationGracePeriodSeconds, skip DaemonSet pods, allow emptyDir deletion, and
// use DefaultDrainTimeout.
type DrainOptions struct {
	// Progress, if set, receives a human-readable message as each pod is evicted or deleted.
	Progress func(string)
	// Timeout caps pod eviction. Zero uses DefaultDrainTimeout.
	Timeout time.Duration
	// GracePeriodSeconds overrides the pod termination grace period. Zero (the default) maps to -1, which
	// honors each pod's own terminationGracePeriodSeconds. A positive value overrides it. Values below -1
	// are rejected.
	GracePeriodSeconds int
}

// Cordon marks the node unschedulable. Idempotent: a no-op if the node is already cordoned.
func Cordon(ctx context.Context, clientset kubernetes.Interface, nodeName string) error {
	node, err := clientset.CoreV1().Nodes().Get(ctx, nodeName, metav1.GetOptions{})
	if err != nil {
		return fmt.Errorf("failed to get node %q: %w", nodeName, err)
	}

	if err = drain.RunCordonOrUncordon(&drain.Helper{Ctx: ctx, Client: clientset}, node, true); err != nil {
		return fmt.Errorf("failed to cordon node %q: %w", nodeName, err)
	}

	return nil
}

// Drain evicts the node's pods with the kubectl drain library: it honors each pod's grace period and
// PodDisruptionBudgets, skips DaemonSet, mirror and static pods, and negotiates the eviction API version
// (policy/v1, falling back to v1beta1 on older servers). The caller must cordon the node first.
//
// A drain that does not finish within the timeout returns an error; the caller decides whether that is
// fatal or whether to proceed (e.g. to reboot anyway).
func Drain(ctx context.Context, clientset kubernetes.Interface, nodeName string, opts DrainOptions) error {
	timeout := opts.Timeout
	if timeout == 0 {
		timeout = DefaultDrainTimeout
	}

	if opts.GracePeriodSeconds < -1 {
		return fmt.Errorf("invalid GracePeriodSeconds %d: must be -1, 0, or positive", opts.GracePeriodSeconds)
	}

	gracePeriod := opts.GracePeriodSeconds
	if gracePeriod == 0 {
		gracePeriod = -1
	}

	drainCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	helper := &drain.Helper{
		Ctx:                 drainCtx,
		Client:              clientset,
		Force:               true,
		GracePeriodSeconds:  gracePeriod,
		IgnoreAllDaemonSets: true,
		DeleteEmptyDirData:  true,
		Timeout:             timeout,
		Out:                 io.Discard,
		ErrOut:              io.Discard,
	}

	if opts.Progress != nil {
		helper.OnPodDeletionOrEvictionStarted = func(pod *corev1.Pod, usingEviction bool) {
			verb := "deleting"
			if usingEviction {
				verb = "evicting"
			}

			opts.Progress(fmt.Sprintf("%s pod %s/%s", verb, pod.Namespace, pod.Name))
		}

		// Report each pod's outcome. The failure branch names the pod that could not be evicted, which is
		// the diagnostic a caller needs when a PodDisruptionBudget stalls the drain.
		helper.OnPodDeletionOrEvictionFinished = func(pod *corev1.Pod, usingEviction bool, err error) {
			present, past := "delete", "deleted"
			if usingEviction {
				present, past = "evict", "evicted"
			}

			if err != nil {
				opts.Progress(fmt.Sprintf("failed to %s pod %s/%s: %v", present, pod.Namespace, pod.Name, err))

				return
			}

			opts.Progress(fmt.Sprintf("%s pod %s/%s", past, pod.Namespace, pod.Name))
		}
	}

	if err := drain.RunNodeDrain(helper, nodeName); err != nil {
		return fmt.Errorf("failed to drain node %q: %w", nodeName, err)
	}

	return nil
}

// Uncordon marks the node schedulable. A missing node is a no-op, so the call is safe to retry after a
// node has been removed.
func Uncordon(ctx context.Context, clientset kubernetes.Interface, nodeName string) error {
	node, err := clientset.CoreV1().Nodes().Get(ctx, nodeName, metav1.GetOptions{})
	if err != nil {
		if apierrors.IsNotFound(err) {
			return nil
		}

		return fmt.Errorf("failed to get node %q: %w", nodeName, err)
	}

	if err = drain.RunCordonOrUncordon(&drain.Helper{Ctx: ctx, Client: clientset}, node, false); err != nil {
		return fmt.Errorf("failed to uncordon node %q: %w", nodeName, err)
	}

	return nil
}

// WaitForNodeReady polls until the node reports Ready=True or the timeout elapses. Retryable failures
// keep the poll going, anything else is returned at once.
func WaitForNodeReady(ctx context.Context, clientset kubernetes.Interface, nodeName string, timeout time.Duration) error {
	return wait.PollUntilContextTimeout(ctx, nodeReadyPollInterval, timeout, true, func(ctx context.Context) (bool, error) {
		node, err := clientset.CoreV1().Nodes().Get(ctx, nodeName, metav1.GetOptions{})
		if err != nil {
			if gokubernetes.IsRetryableError(err) {
				return false, nil //nolint:nilerr
			}

			return false, fmt.Errorf("failed to get node %q: %w", nodeName, err)
		}

		for _, cond := range node.Status.Conditions {
			if cond.Type == corev1.NodeReady {
				return cond.Status == corev1.ConditionTrue, nil
			}
		}

		return false, nil
	})
}
