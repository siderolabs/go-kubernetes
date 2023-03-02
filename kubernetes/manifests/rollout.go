// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at http://mozilla.org/MPL/2.0/.

package manifests

import (
	"context"
	"time"

	"github.com/siderolabs/gen/channel"
	"github.com/siderolabs/go-retry/retry"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/rest"

	"github.com/siderolabs/go-kubernetes/kubernetes"
)

// RolloutProgress indicates the current manifest rollout progress.
type RolloutProgress struct {
	Object Manifest
	Path   string
}

// WaitForRollout waits for the manifest rollout to be complete.
func WaitForRollout(ctx context.Context, config *rest.Config, objects []Manifest, resultCh chan<- RolloutProgress) error {
	var deployments, daemonsets []Manifest

	for _, object := range objects {
		switch {
		case object.GetKind() == "Deployment" && object.GroupVersionKind().Group == "apps":
			deployments = append(deployments, object)
		case object.GetKind() == "DaemonSet" && object.GroupVersionKind().Group == "apps":
			daemonsets = append(daemonsets, object)
		}
	}

	clientset, err := kubernetes.NewForConfig(config)
	if err != nil {
		return err
	}

	defer clientset.Close() //nolint:errcheck

	if err = waitForDeploymentsRollout(ctx, clientset, deployments, resultCh); err != nil {
		return err
	}

	if err = waitForDaemonSetsRollout(ctx, clientset, daemonsets, resultCh); err != nil {
		return err
	}

	return nil
}

func waitForDeploymentsRollout(ctx context.Context, clientset *kubernetes.Client, deployments []Manifest, resultCh chan<- RolloutProgress) error {
	for _, obj := range deployments {
		obj := obj

		if !channel.SendWithContext(ctx, resultCh,
			RolloutProgress{
				Object: obj,
				Path:   manifestPath(obj),
			}) {
			return ctx.Err()
		}

		err := retry.Constant(3*time.Minute, retry.WithUnits(10*time.Second)).Retry(func() error {
			deployment, err := clientset.AppsV1().Deployments(obj.GetNamespace()).Get(ctx, obj.GetName(), metav1.GetOptions{})
			if err != nil {
				if kubernetes.IsRetryableError(err) {
					return retry.ExpectedError(err)
				}

				return err
			}

			if deployment.Generation != deployment.Status.ObservedGeneration {
				return retry.ExpectedErrorf("deployment %s generation %d != observed generation %d", deployment.Name, deployment.Generation, deployment.Status.ObservedGeneration)
			}

			if deployment.Status.ReadyReplicas != deployment.Status.Replicas || deployment.Status.UpdatedReplicas != deployment.Status.Replicas {
				return retry.ExpectedErrorf("deployment %s ready replicas %d != replicas %d", deployment.Name, deployment.Status.ReadyReplicas, deployment.Status.Replicas)
			}

			return nil
		})
		if err != nil {
			return err
		}
	}

	return nil
}

func waitForDaemonSetsRollout(ctx context.Context, clientset *kubernetes.Client, daemonSets []Manifest, resultCh chan<- RolloutProgress) error {
	for _, obj := range daemonSets {
		obj := obj

		if !channel.SendWithContext(ctx, resultCh,
			RolloutProgress{
				Object: obj,
				Path:   manifestPath(obj),
			}) {
			return ctx.Err()
		}

		err := retry.Constant(5*time.Minute, retry.WithUnits(10*time.Second)).Retry(func() error {
			daemonSet, err := clientset.AppsV1().DaemonSets(obj.GetNamespace()).Get(ctx, obj.GetName(), metav1.GetOptions{})
			if err != nil {
				if kubernetes.IsRetryableError(err) {
					return retry.ExpectedError(err)
				}

				return err
			}

			if daemonSet.Generation != daemonSet.Status.ObservedGeneration {
				return retry.ExpectedErrorf("expected observed generation for %s to be %d, got %d",
					daemonSet.Name, daemonSet.Generation, daemonSet.Status.ObservedGeneration)
			}

			if daemonSet.Status.UpdatedNumberScheduled != daemonSet.Status.DesiredNumberScheduled {
				return retry.ExpectedErrorf("expected current number up-to-date for %s to be %d, got %d",
					daemonSet.Name, daemonSet.Status.UpdatedNumberScheduled, daemonSet.Status.CurrentNumberScheduled)
			}

			if daemonSet.Status.CurrentNumberScheduled != daemonSet.Status.DesiredNumberScheduled {
				return retry.ExpectedErrorf("expected current number scheduled for %s to be %d, got %d",
					daemonSet.Name, daemonSet.Status.DesiredNumberScheduled, daemonSet.Status.CurrentNumberScheduled)
			}

			if daemonSet.Status.NumberAvailable != daemonSet.Status.DesiredNumberScheduled {
				return retry.ExpectedErrorf("expected number available for %s to be %d, got %d",
					daemonSet.Name, daemonSet.Status.DesiredNumberScheduled, daemonSet.Status.NumberAvailable)
			}

			if daemonSet.Status.NumberReady != daemonSet.Status.DesiredNumberScheduled {
				return retry.ExpectedErrorf("expected number ready for %s to be %d, got %d",
					daemonSet.Name, daemonSet.Status.DesiredNumberScheduled, daemonSet.Status.NumberReady)
			}

			return nil
		})
		if err != nil {
			return err
		}
	}

	return nil
}
