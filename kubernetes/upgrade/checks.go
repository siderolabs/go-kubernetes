// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at http://mozilla.org/MPL/2.0/.

package upgrade

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"slices"
	"strings"
	"text/tabwriter"

	"github.com/cosi-project/runtime/pkg/safe"
	"github.com/cosi-project/runtime/pkg/state"
	"github.com/siderolabs/gen/xslices"
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
	workerNodes       []string
	log               func(string, ...any)

	upgradePath         string
	upgradeVersionCheck map[string]componentChecks
}

// ComponentRemovedItemsError is an error type for removed items.
type ComponentRemovedItemsError struct { //nolint:govet,recvcheck
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
	// checks specific to kubelet
	kubeletChecks componentCheck
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
func NewChecks(path *Path, state state.State, k8sConfig *rest.Config, controlPlaneNodes, workerNodes []string, logFunc func(string, ...any)) (*Checks, error) {
	return &Checks{
		state:             state,
		k8sConfig:         k8sConfig,
		log:               logFunc,
		upgradePath:       path.String(),
		controlPlaneNodes: controlPlaneNodes,
		workerNodes:       workerNodes,
		// https://kubernetes.io/docs/reference/using-api/deprecation-guide/
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
				kubeAPIServerChecks: apiServerCheck{
					componentCheck: componentCheck{
						removedFlags: []string{
							"master-service-namespace",
						},
					},
				},
				removedFeatureGates: []string{
					"DynamicKubeletConfig",
				},
			},
			// https://kubernetes.io/blog/2023/03/17/upcoming-changes-in-kubernetes-v1-27/
			"1.26->1.27": {
				kubeControllerManagerChecks: componentCheck{
					removedFlags: []string{
						"enable-taint-manager",
						"pod-eviction-timeout",
					},
				},
				kubeletChecks: componentCheck{
					removedFlags: []string{
						"container-runtime",
						"master-service-namespace",
					},
				},
				removedFeatureGates: []string{
					"ExpandCSIVolumes",
					"ExpandInUsePersistentVolumes",
					"ExpandPersistentVolumes",
					"ControllerManagerLeaderMigration",
					"CSIMigration",
					"CSIInlineVolume",
					"EphemeralContainers",
					"LocalStorageCapacityIsolation",
					"NetworkPolicyEndPort",
					"StatefulSetMinReadySeconds",
					"IdentifyPodOS",
					"DaemonSetUpdateSurge",
				},
			},
			// https://github.com/kubernetes/kubernetes/blob/master/CHANGELOG/CHANGELOG-1.28.md
			"1.27->1.28": {
				removedFeatureGates: []string{
					"AdvancedAuditing",
					"DelegateFSGroupToCSIDriver",
					"DevicePlugins",
					"DisableAcceleratorUsageMetrics",
					"EndpointSliceTerminatingCondition",
					"CSIStorageCapacity",
					"CSIMigrationGCE",
					"KubeletCredentialProviders",
					"MixedProtocolLBService",
					"ServiceInternalTrafficPolicy",
					"ServiceIPStaticSubrange",
					"WindowsHostProcessContainers",
				},
			},
			// https://github.com/kubernetes/kubernetes/blob/master/CHANGELOG/CHANGELOG-1.29.md
			"1.28->1.29": {
				kubeAPIServerChecks: apiServerCheck{
					removedAPIResources: []string{
						"clustercidrs.v1alpha1.networking.k8s.io", // https://github.com/kubernetes/kubernetes/pull/121229
					},
				},
			},
			// https://github.com/kubernetes/kubernetes/blob/master/CHANGELOG/CHANGELOG-1.30.md
			"1.29->1.30": {
				removedFeatureGates: []string{
					"ExpandedDNSConfig",
					"ExperimentalHostUserNamespaceDefaultingGate",
					"IPTablesOwnershipCleanup",
					"KubeletPodResources",
					"KubeletPodResourcesGetAllocatable",
					"MinimizeIPTablesRestore",
					"ProxyTerminatingEndpoints",
					"RemoveSelfLink",
				},
				kubeAPIServerChecks: apiServerCheck{
					removedAdmissionPlugins: []string{
						"SecurityContextDeny", // https://github.com/kubernetes/kubernetes/pull/122612
					},
				},
			},
			// https://github.com/kubernetes/kubernetes/blob/master/CHANGELOG/CHANGELOG-1.31.md
			"1.30->1.31": {
				removedFeatureGates: []string{
					"APIPriorityAndFairness", // https://github.com/kubernetes/kubernetes/pull/125846
					"CSINodeExpandSecret",
					"ConsistentHTTPGetHandlers",
					"DefaultHostNetworkHostPortsInPodTemplates",
					"ServiceNodePortStaticSubrange",
					"SkipReadOnlyValidationGCE",
				},
				kubeletChecks: componentCheck{
					removedFlags: []string{
						"keep-terminated-pod-volumes", // https://github.com/kubernetes/kubernetes/pull/122082
						"iptables-masquerade-bit",
						"iptables-drop-bit", // https://github.com/kubernetes/kubernetes/pull/122363
					},
				},
				kubeControllerManagerChecks: componentCheck{
					removedFlags: []string{
						"volume-host-cidr-denylist",
						"volume-host-allow-local-loopback", // https://github.com/kubernetes/kubernetes/pull/124017
						"horizontal-pod-autoscaler-upscale-delay",
						"horizontal-pod-autoscaler-downscale-delay", // https://github.com/kubernetes/kubernetes/pull/124948
					},
				},
			},
			// https://github.com/kubernetes/kubernetes/blob/master/CHANGELOG/CHANGELOG-1.32.md
			"1.31->1.32": {
				removedFeatureGates: []string{
					"AllowServiceLBStatusOnNonLB",         // https://github.com/kubernetes/kubernetes/pull/126786
					"CloudDualStackNodeIPs",               // https://github.com/kubernetes/kubernetes/pull/126840
					"DRAControlPlaneController",           // https://github.com/kubernetes/kubernetes/pull/128003
					"HPAContainerMetrics",                 // https://github.com/kubernetes/kubernetes/pull/126862
					"KMSv2",                               // https://github.com/kubernetes/kubernetes/pull/126698
					"KMSv2KDF",                            // https://github.com/kubernetes/kubernetes/pull/126698
					"LegacyServiceAccountTokenCleanUp",    // https://github.com/kubernetes/kubernetes/pull/126839
					"MinDomainsInPodTopologySpread",       // https://github.com/kubernetes/kubernetes/pull/126863
					"NewVolumeManagerReconstruction",      // https://github.com/kubernetes/kubernetes/pull/126775
					"NodeOutOfServiceVolumeDetach",        // https://github.com/kubernetes/kubernetes/pull/127019
					"ServerSideApply",                     // https://github.com/kubernetes/kubernetes/pull/127058
					"ServerSideFieldValidation",           // https://github.com/kubernetes/kubernetes/pull/127058
					"StableLoadBalancerNodeSet",           // https://github.com/kubernetes/kubernetes/pull/126841
					"ValidatingAdmissionPolicy",           // https://github.com/kubernetes/kubernetes/pull/126645
					"ZeroLimitedNominalConcurrencyShares", // https://github.com/kubernetes/kubernetes/pull/126894
				},
				kubeAPIServerChecks: apiServerCheck{
					removedAPIResources: []string{
						"podschedulingcontexts.v1alpha3.resource.k8s.io", // https://github.com/kubernetes/kubernetes/pull/128003
					},
				},
			},
			// https://github.com/kubernetes/kubernetes/blob/master/CHANGELOG/CHANGELOG-1.33.md
			"1.32->1.33": {
				removedFeatureGates: []string{
					"AppArmor",                               // https://github.com/kubernetes/kubernetes/pull/129375
					"AppArmorFields",                         // https://github.com/kubernetes/kubernetes/pull/129497
					"CPUManager",                             // https://github.com/kubernetes/kubernetes/pull/129296
					"DisableCloudProviders",                  // https://github.com/kubernetes/kubernetes/pull/130162
					"DisableKubeletCloudCredentialProviders", // https://github.com/kubernetes/kubernetes/pull/130162
					"DynamicResourceAllocation",
					"JobPodFailurePolicy",                     // https://github.com/kubernetes/kubernetes/pull/129498
					"KubeProxyDrainingTerminatingNodes",       // https://github.com/kubernetes/kubernetes/pull/129692
					"PDBUnhealthyPodEvictionPolicy",           // https://github.com/kubernetes/kubernetes/pull/129500
					"PersistentVolumeLastPhaseTransitionTime", // https://github.com/kubernetes/kubernetes/pull/129295
					"VolumeCapacityPriority",                  // https://github.com/kubernetes/kubernetes/pull/128184
				},
				kubeAPIServerChecks: apiServerCheck{
					componentCheck: componentCheck{
						removedFlags: []string{
							"cloud-config",
						},
					},
				},
			},
		},
	}, nil
}

// Run executes the checks.
//
//nolint:gocognit
func (checks *Checks) Run(ctx context.Context) error {
	var k8sComponentCheck ComponentRemovedItemsError

	if k8sComponentChecks, ok := checks.upgradeVersionCheck[checks.upgradePath]; ok {
		checks.log("checking for removed Kubernetes component flags")

		for _, node := range checks.controlPlaneNodes {
			for _, id := range []string{k8s.APIServerID, k8s.ControllerManagerID, k8s.SchedulerID} {
				staticPod, err := safe.StateGet[*k8s.StaticPod](client.WithNode(ctx, node), checks.state, k8s.NewStaticPod(k8s.NamespaceName, id).Metadata())
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
					k8sComponentCheck.PopulateRemovedCLIFlags(node, id, pod.Spec.Containers[0].Command, k8sComponentChecks.kubeAPIServerChecks.removedFlags)
				case k8s.ControllerManagerID:
					k8sComponentCheck.PopulateRemovedCLIFlags(node, id, pod.Spec.Containers[0].Command, k8sComponentChecks.kubeControllerManagerChecks.removedFlags)
				case k8s.SchedulerID:
					k8sComponentCheck.PopulateRemovedCLIFlags(node, id, pod.Spec.Containers[0].Command, k8sComponentChecks.kubeSchedulerChecks.removedFlags)
				}

				k8sComponentCheck.PopulateRemovedFeatureGates(node, id, pod.Spec.Containers[0].Command, k8sComponentChecks.removedFeatureGates)
			}
		}

		for _, node := range append(append([]string(nil), checks.controlPlaneNodes...), checks.workerNodes...) {
			kubeletSpec, err := safe.StateGet[*k8s.KubeletSpec](client.WithNode(ctx, node), checks.state, k8s.NewKubeletSpec(k8s.NamespaceName, k8s.KubeletID).Metadata())
			if err != nil {
				if state.IsNotFoundError(err) {
					continue
				}

				return err
			}

			k8sComponentCheck.PopulateRemovedCLIFlags(node, k8s.KubeletID, kubeletSpec.TypedSpec().Args, k8sComponentChecks.kubeletChecks.removedFlags)
		}

		checks.log("checking for removed Kubernetes API resource versions")

		if err := k8sComponentCheck.PopulateRemovedAPIResources(ctx, checks.k8sConfig, k8sComponentChecks.kubeAPIServerChecks.removedAPIResources); err != nil {
			return err
		}
	}

	return k8sComponentCheck.ErrorOrNil()
}

// PopulateRemovedCLIFlags populates the removed flags.
func (e *ComponentRemovedItemsError) PopulateRemovedCLIFlags(node, component string, cliFlags []string, removedFlags []string) {
	for _, removedFlag := range removedFlags {
		if slices.ContainsFunc(cliFlags, func(s string) bool {
			cliFlagKey, _, _ := strings.Cut(s, "=")

			return "--"+removedFlag == cliFlagKey
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
func (e *ComponentRemovedItemsError) PopulateRemovedFeatureGates(node, component string, cliFlags []string, removedFeatureGates []string) {
	featureGateFlags := xslices.Filter(cliFlags, func(s string) bool {
		return strings.HasPrefix(s, "--feature-gates")
	})

	if len(featureGateFlags) > 0 {
		featureGates := strings.Split(strings.TrimPrefix(featureGateFlags[0], "--feature-gates="), ",")

		for _, removedFeatureGate := range removedFeatureGates {
			if slices.ContainsFunc(featureGates, func(s string) bool {
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
func (e *ComponentRemovedItemsError) PopulateRemovedAdmissionPlugins(node, component string, cliFlags []string, removedAdmissionPlugins []string) {
	admissionFlags := xslices.Filter(cliFlags, func(s string) bool {
		return strings.HasPrefix(s, "--enable-admission-plugins")
	})

	if len(admissionFlags) > 0 {
		admissionPlugins := strings.Split(strings.TrimPrefix(admissionFlags[0], "--enable-admission-plugins="), ",")

		for _, removedAdmissionPlugin := range removedAdmissionPlugins {
			if slices.ContainsFunc(admissionPlugins, func(s string) bool {
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
		fmt.Fprintf(w, "\nNODE\tCOMPONENT\tREMOVED ADMISSION PLUGIN\n") //nolint:errcheck

		for _, item := range e.AdmissionFlags {
			fmt.Fprintf(w, "%s\t%s\t%s\n", item.Node, item.Component, item.Value) //nolint:errcheck
		}
	}

	if len(e.FeatureGates) > 0 {
		fmt.Fprintf(w, "\nNODE\tCOMPONENT\tREMOVED FEATURE GATE\n") //nolint:errcheck

		for _, item := range e.FeatureGates {
			fmt.Fprintf(w, "%s\t%s\t%s\n", item.Node, item.Component, item.Value) //nolint:errcheck
		}
	}

	if len(e.CLIFlags) > 0 {
		fmt.Fprintf(w, "\nNODE\tCOMPONENT\tREMOVED FLAG\n") //nolint:errcheck

		for _, item := range e.CLIFlags {
			fmt.Fprintf(w, "%s\t%s\t%s\n", item.Node, item.Component, item.Value) //nolint:errcheck
		}
	}

	if len(e.APIResources) > 0 {
		fmt.Fprintf(w, "\nREMOVED RESOURCE\tCOUNT\t\n") //nolint:errcheck

		for apiVersion, count := range e.APIResources {
			fmt.Fprintf(w, "%s\t%d\t\n", apiVersion, count) //nolint:errcheck
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
