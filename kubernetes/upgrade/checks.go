// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at http://mozilla.org/MPL/2.0/.

package upgrade

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"strings"
	"text/tabwriter"

	"github.com/cosi-project/runtime/pkg/safe"
	"github.com/cosi-project/runtime/pkg/state"
	"github.com/siderolabs/gen/slices"
	"github.com/siderolabs/talos/pkg/machinery/client"
	"github.com/siderolabs/talos/pkg/machinery/resources/k8s"
	v1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/rest"
)

// Checks is a set of checks to run before upgrading k8s components.
type Checks struct { //nolint:govet
	state             state.State
	k8sConfig         *rest.Config
	controlPlaneNodes []string
	log               func(string, ...any)

	upgradePath         string
	upgradeVersionCheck map[string]componentChecks
}

// ComponentRemovedItemsError is an error type for removed items.
type ComponentRemovedItemsError struct { //nolint:govet
	AdmissionFlags []ComponentItem
	CLIFlags       []ComponentItem
	FeatureGates   []ComponentItem
	APIResources   map[string]int
}

// ComponentItem represents a component item.
type ComponentItem struct {
	Node      string
	Component string
	Value     string
}

type componentChecks struct {
	// feature gates are common to kube-apiserver, kube-controller-manager and kube-scheduler
	removedFeatureGates []string
	// checks specific to kube-apiserver
	kubeAPIServerChecks apiServerCheck
	// checks specific to kube-controller-manager
	kubeControllerManagerChecks componentCheck
	// checks specific to kube-scheduler
	kubeSchedulerChecks componentCheck
}

type apiServerCheck struct {
	// removedAPIResources represent the Kuberenetes API resources that are removed in the upgrade version
	removedAPIResources []string
	// removedAdmissionPlugins represent the Kuberenetes Admission Plugins that are removed in the upgrade version
	removedAdmissionPlugins []string
	componentCheck
}

type componentCheck struct {
	// removedFlags represent the Kuberenetes API server flags that are removed in the upgrade version
	removedFlags []string
}

// NewChecks initializes and returns Checks.
func NewChecks(path *Path, state state.State, k8sConfig *rest.Config, controlPlaneNodes []string, logFunc func(string, ...any)) (*Checks, error) {
	return &Checks{
		state:             state,
		k8sConfig:         k8sConfig,
		log:               logFunc,
		upgradePath:       path.String(),
		controlPlaneNodes: controlPlaneNodes,
		upgradeVersionCheck: map[string]componentChecks{
			"1.24->1.25": {
				kubeAPIServerChecks: apiServerCheck{
					removedAPIResources: []string{
						"podsecuritypolicies.v1beta1.policy",
					},
					componentCheck: componentCheck{
						removedFlags: []string{
							"service-account-api-audiences",
						},
					},
					removedAdmissionPlugins: []string{
						"PodSecurityPolicy",
					},
				},
				kubeControllerManagerChecks: componentCheck{
					removedFlags: []string{
						"deleting-pods-qps",
						"deleting-pods-burst",
						"register-retry-count",
					},
				},
				// https://kubernetes.io/docs/reference/command-line-tools-reference/feature-gates-removed/
				removedFeatureGates: []string{
					"CSIVolumeFSGroupPolicy",
					"ConfigurableFSGroupPolicy",
					"PodDisruptionBudget",
					"SelectorIndex",
				},
			},
			"1.25->1.26": {
				removedFeatureGates: []string{
					"DynamicKubeletConfig",
				},
			},
		},
	}, nil
}

// Run executes the checks.
func (checks *Checks) Run(ctx context.Context) error {
	var k8sComponentCheck ComponentRemovedItemsError

	if k8sComponentChecks, ok := checks.upgradeVersionCheck[checks.upgradePath]; ok {
		checks.log("checking for removed Kubernetes component flags")

		for _, node := range checks.controlPlaneNodes {
			ctx = client.WithNode(ctx, node)

			for _, id := range []string{k8s.APIServerID, k8s.ControllerManagerID, k8s.SchedulerID} {
				staticPod, err := safe.StateGet[*k8s.StaticPod](ctx, checks.state, k8s.NewStaticPod(k8s.NamespaceName, id).Metadata())
				if err != nil {
					if state.IsNotFoundError(err) {
						continue
					}

					return err
				}

				pod, err := staticPodTypedResourceToK8sPodSpec(staticPod)
				if err != nil {
					return err
				}

				switch id {
				case k8s.APIServerID:
					k8sComponentCheck.PopulateRemovedAdmissionPlugins(node, id, pod.Spec.Containers[0].Command, k8sComponentChecks.kubeAPIServerChecks.removedAdmissionPlugins)
					k8sComponentCheck.PopulateRemovedCLIFlags(node, id, pod.Spec.Containers[0].Command, k8sComponentChecks.kubeAPIServerChecks.componentCheck.removedFlags)
				case k8s.ControllerManagerID:
					k8sComponentCheck.PopulateRemovedCLIFlags(node, id, pod.Spec.Containers[0].Command, k8sComponentChecks.kubeControllerManagerChecks.removedFlags)
				case k8s.SchedulerID:
					k8sComponentCheck.PopulateRemovedCLIFlags(node, id, pod.Spec.Containers[0].Command, k8sComponentChecks.kubeSchedulerChecks.removedFlags)
				}

				k8sComponentCheck.PopulateRemovedFeatureGates(node, id, pod.Spec.Containers[0].Command, k8sComponentChecks.removedFeatureGates)
			}
		}

		checks.log("checking for removed Kubernetes API resource versions")

		if err := k8sComponentCheck.PopulateRemovedAPIResources(ctx, checks.k8sConfig, k8sComponentChecks.kubeAPIServerChecks.removedAPIResources); err != nil {
			return err
		}
	}

	return k8sComponentCheck.ErrorOrNil()
}

// PopulateRemovedCLIFlags populates the removed flags.
func (e *ComponentRemovedItemsError) PopulateRemovedCLIFlags(node, component string, apiServerCLIFlags []string, removedFlags []string) {
	for _, removedFlag := range removedFlags {
		if slices.Contains(apiServerCLIFlags, func(s string) bool {
			return strings.HasPrefix(s, "--"+removedFlag)
		}) {
			e.CLIFlags = append(e.CLIFlags, ComponentItem{
				Node:      node,
				Component: component,
				Value:     removedFlag,
			})
		}
	}
}

// PopulateRemovedFeatureGates populates the removed feature gates.
func (e *ComponentRemovedItemsError) PopulateRemovedFeatureGates(node, component string, apiServerCLIFlags []string, removedFeatureGates []string) {
	featureGateFlags := slices.Filter(apiServerCLIFlags, func(s string) bool {
		return strings.HasPrefix(s, "--feature-gates")
	})

	if len(featureGateFlags) > 0 {
		featureGates := strings.Split(strings.TrimPrefix(featureGateFlags[0], "--feature-gates="), ",")

		for _, removedFeatureGate := range removedFeatureGates {
			if slices.Contains(featureGates, func(s string) bool {
				return removedFeatureGate == strings.Split(s, "=")[0]
			}) {
				e.FeatureGates = append(e.FeatureGates, ComponentItem{
					Node:      node,
					Component: component,
					Value:     removedFeatureGate,
				})
			}
		}
	}
}

// PopulateRemovedAdmissionPlugins populates the removed admission plugins.
func (e *ComponentRemovedItemsError) PopulateRemovedAdmissionPlugins(node, component string, apiServerCLIFlags []string, removedAdmissionPlugins []string) {
	admissionFlags := slices.Filter(apiServerCLIFlags, func(s string) bool {
		return strings.HasPrefix(s, "--enable-admission-plugins")
	})

	if len(admissionFlags) > 0 {
		admissionPlugins := strings.Split(strings.TrimPrefix(admissionFlags[0], "--enable-admission-plugins="), ",")

		for _, removedAdmissionPlugin := range removedAdmissionPlugins {
			if slices.Contains(admissionPlugins, func(s string) bool {
				return removedAdmissionPlugin == s
			}) {
				e.AdmissionFlags = append(e.AdmissionFlags, ComponentItem{
					Node:      node,
					Component: component,
					Value:     removedAdmissionPlugin,
				})
			}
		}
	}
}

// PopulateRemovedAPIResources populates the removed API resources.
func (e *ComponentRemovedItemsError) PopulateRemovedAPIResources(ctx context.Context, k8sConfig *rest.Config, removedAPIResources []string) error {
	if len(removedAPIResources) == 0 || k8sConfig == nil {
		return nil
	}

	// copy the config to avoid mutating input argument
	k8sConfigCopy := *k8sConfig
	k8sConfigCopy.WarningHandler = rest.NewWarningWriter(io.Discard, rest.WarningWriterOptions{})

	k8sClient, err := dynamic.NewForConfig(&k8sConfigCopy)
	if err != nil {
		return fmt.Errorf("error building kubernetes client: %w", err)
	}

	for _, resource := range removedAPIResources {
		gvr, _ := schema.ParseResourceArg(resource)

		if gvr == nil {
			return fmt.Errorf("failed to parse group version resource %s", resource)
		}

		res, err := k8sClient.Resource(*gvr).List(ctx, metav1.ListOptions{})
		if err != nil {
			if apierrors.IsNotFound(err) {
				continue
			}

			return err
		}

		count := len(res.Items)

		if count > 0 {
			if e.APIResources == nil {
				e.APIResources = make(map[string]int)
			}

			e.APIResources[resource] = count
		}
	}

	return nil
}

func staticPodTypedResourceToK8sPodSpec(staticPod *k8s.StaticPod) (*v1.Pod, error) {
	var spec v1.Pod

	jsonSerialized, err := json.Marshal(staticPod.TypedSpec().Pod)
	if err != nil {
		return nil, err
	}

	err = json.Unmarshal(jsonSerialized, &spec)

	return &spec, err
}

// Error returns the error message.
func (e ComponentRemovedItemsError) Error() string {
	var buf strings.Builder

	w := tabwriter.NewWriter(&buf, 0, 0, 3, ' ', 0)

	if len(e.AdmissionFlags) > 0 {
		fmt.Fprintf(w, "\nNODE\tCOMPONENT\tREMOVED ADMISSION PLUGIN\n")

		for _, item := range e.AdmissionFlags {
			fmt.Fprintf(w, "%s\t%s\t%s\n", item.Node, item.Component, item.Value)
		}
	}

	if len(e.FeatureGates) > 0 {
		fmt.Fprintf(w, "\nNODE\tCOMPONENT\tREMOVED FEATURE GATE\n")

		for _, item := range e.FeatureGates {
			fmt.Fprintf(w, "%s\t%s\t%s\n", item.Node, item.Component, item.Value)
		}
	}

	if len(e.CLIFlags) > 0 {
		fmt.Fprintf(w, "\nNODE\tCOMPONENT\tREMOVED FLAG\n")

		for _, item := range e.CLIFlags {
			fmt.Fprintf(w, "%s\t%s\t%s\n", item.Node, item.Component, item.Value)
		}
	}

	if len(e.APIResources) > 0 {
		fmt.Fprintf(w, "\nREMOVED RESOURCE\tCOUNT\t\n")

		for apiVersion, count := range e.APIResources {
			fmt.Fprintf(w, "%s\t%d\t\n", apiVersion, count)
		}
	}

	//nolint:errcheck
	w.Flush()

	return buf.String()
}

// ErrorOrNil returns the error if it exists.
func (e ComponentRemovedItemsError) ErrorOrNil() error {
	if e.Error() != "" {
		return e
	}

	return nil
}
