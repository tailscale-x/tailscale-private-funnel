package kube

import (
	"context"
	"net/netip"

	core "k8s.io/api/core/v1"
	discovery "k8s.io/api/discovery/v1"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/util/intstr"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

// EndpointSlices are not proof of a Pod backend: namespace writers may create
// slices pointing outside the cluster. Bind addresses and ports to a live,
// selected Pod rather than allowing a Service to become an external TCP tunnel.
func readyPodEndpoint(ctx context.Context, c client.Client, svc core.Service, port core.ServicePort, slice discovery.EndpointSlice, ep discovery.Endpoint) bool {
	if len(svc.Spec.Selector) == 0 || (slice.AddressType != discovery.AddressTypeIPv4 && slice.AddressType != discovery.AddressTypeIPv6) {
		return false
	}
	owned := false
	for _, o := range slice.OwnerReferences {
		owned = owned || (o.Kind == "Service" && o.Name == svc.Name && o.UID == svc.UID && o.Controller != nil && *o.Controller)
	}
	if !owned || (ep.Conditions.Ready != nil && !*ep.Conditions.Ready) || (ep.Conditions.Terminating != nil && *ep.Conditions.Terminating) {
		return false
	}
	ref := ep.TargetRef
	if ref == nil || ref.Kind != "Pod" || ref.Name == "" || ref.UID == "" || (ref.Namespace != "" && ref.Namespace != svc.Namespace) {
		return false
	}
	var pod core.Pod
	if c.Get(ctx, client.ObjectKey{Namespace: svc.Namespace, Name: ref.Name}, &pod) != nil || pod.UID != ref.UID || pod.Spec.HostNetwork || pod.DeletionTimestamp != nil || pod.Status.Phase != core.PodRunning || !labels.SelectorFromSet(svc.Spec.Selector).Matches(labels.Set(pod.Labels)) {
		return false
	}
	ready := false
	for _, condition := range pod.Status.Conditions {
		ready = ready || (condition.Type == core.PodReady && condition.Status == core.ConditionTrue)
	}
	if !ready || len(ep.Addresses) == 0 {
		return false
	}
	for _, address := range ep.Addresses {
		ip, err := netip.ParseAddr(address)
		if err != nil || !ip.IsGlobalUnicast() {
			return false
		}
		found := address == pod.Status.PodIP
		for _, p := range pod.Status.PodIPs {
			found = found || address == p.IP
		}
		if !found {
			return false
		}
	}
	target := podTargetPort(port, pod)
	if target < 1 || target > 65535 {
		return false
	}
	for _, p := range slice.Ports {
		if p.Port != nil && *p.Port == target && (p.Name == nil && port.Name == "" || p.Name != nil && *p.Name == port.Name) && (p.Protocol == nil || *p.Protocol == core.ProtocolTCP) {
			return true
		}
	}
	return false
}

func podTargetPort(port core.ServicePort, pod core.Pod) int32 {
	target := port.TargetPort.IntVal
	if port.TargetPort.Type == intstr.String {
		target = 0
		for _, container := range pod.Spec.Containers {
			for _, p := range container.Ports {
				if p.Name == port.TargetPort.StrVal && (p.Protocol == "" || p.Protocol == core.ProtocolTCP) {
					if target != 0 && target != p.ContainerPort {
						return 0
					}
					target = p.ContainerPort
				}
			}
		}
	} else if target == 0 {
		target = port.Port
	}

	return target
}
