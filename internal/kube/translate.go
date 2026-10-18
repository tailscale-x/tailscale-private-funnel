// Package kube translates only explicitly supported Kubernetes HTTP routing.
package kube

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"net/netip"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/tailscale-x/tailscale-private-funnel/internal/nodeconfig"
	"github.com/tailscale-x/tailscale-private-funnel/internal/policy"
	"github.com/tailscale-x/tailscale-private-funnel/internal/proxy"
	core "k8s.io/api/core/v1"
	discovery "k8s.io/api/discovery/v1"
	networking "k8s.io/api/networking/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
	"sigs.k8s.io/controller-runtime/pkg/client"
	v1 "sigs.k8s.io/gateway-api/apis/v1"
	v1beta1 "sigs.k8s.io/gateway-api/apis/v1beta1"
)

const ControllerName = "tailscale-private-funnel.io/controller"
const AnnotationPrefix = "tailscale-private-funnel.io/"

type Options struct {
	GatewayClass, IngressClass string
	Namespaces                 []string
	ClusterID                  string
	ControllerName             string
}
type Result struct {
	Hosts    []nodeconfig.KubernetesHost
	Problems map[string]string
	Objects  []client.Object
	Parents  map[string][]v1.ParentReference
}

func resourceID(o client.Object) string {
	return fmt.Sprintf("%T:%s/%s:%s", o, o.GetNamespace(), o.GetName(), o.GetUID())
}
func wanted(ns string, options Options) bool {
	if len(options.Namespaces) == 0 {
		return true
	}
	for _, n := range options.Namespaces {
		if n == ns {
			return true
		}
	}
	return false
}

func Translate(ctx context.Context, c client.Client, options Options) (Result, error) {
	result := Result{Problems: map[string]string{}, Parents: map[string][]v1.ParentReference{}}
	var routes v1.HTTPRouteList
	var ingresses networking.IngressList
	if err := c.List(ctx, &routes); err != nil {
		return result, err
	}
	if err := c.List(ctx, &ingresses); err != nil {
		return result, err
	}
	sort.SliceStable(routes.Items, func(i, j int) bool {
		a, b := routes.Items[i], routes.Items[j]
		if !a.CreationTimestamp.Equal(&b.CreationTimestamp) {
			return a.CreationTimestamp.Before(&b.CreationTimestamp)
		}
		return a.Namespace+"/"+a.Name < b.Namespace+"/"+b.Name
	})
	sort.SliceStable(ingresses.Items, func(i, j int) bool {
		a, b := ingresses.Items[i], ingresses.Items[j]
		if !a.CreationTimestamp.Equal(&b.CreationTimestamp) {
			return a.CreationTimestamp.Before(&b.CreationTimestamp)
		}
		return a.Namespace+"/"+a.Name < b.Namespace+"/"+b.Name
	})
	hosts := map[string]*nodeconfig.KubernetesHost{}
	claims := map[string]string{}
	add := func(host string, rule proxy.HTTPRule, backends []proxy.Backend, object client.Object) error {
		if err := policy.ValidateHostname(host); err != nil || strings.Contains(host, "*") {
			return fmt.Errorf("explicit public hostname required")
		}
		if err := proxy.ValidateHTTPRule(rule, false); err != nil {
			return err
		}
		id := resourceID(object)
		for _, match := range rule.Matches {
			key := host + fmt.Sprintf("%#v", match)
			if previous, ok := claims[key]; ok && previous != id && strings.SplitN(previous, ":", 2)[0] != strings.SplitN(id, ":", 2)[0] {
				return fmt.Errorf("hostname/match already claimed by %s", previous)
			}
		}
		for _, match := range rule.Matches {
			claims[host+fmt.Sprintf("%#v", match)] = id
		}
		h := hosts[host]
		if h == nil {
			h = &nodeconfig.KubernetesHost{Hostname: host}
			hosts[host] = h
		}
		// Order each alternative match independently. A specific alternative must
		// not lift the same rule's catch-all above another route's specific match.
		for _, match := range rule.Matches {
			individual := rule
			individual.Matches = []proxy.Match{match}
			h.Rules = append(h.Rules, individual)
			h.ResourceIDs = append(h.ResourceIDs, id)
		}
		h.Backends = append(h.Backends, backends...)
		return nil
	}
	for i := range routes.Items {
		route := &routes.Items[i]
		if !wanted(route.Namespace, options) {
			continue
		}
		var defaults map[string]string
		attached := false
		var problem error
		var listenerHosts []string
		for _, parent := range route.Spec.ParentRefs {
			if parent.Group != nil && *parent.Group != "gateway.networking.k8s.io" || parent.Kind != nil && *parent.Kind != "Gateway" {
				continue
			}
			ns := route.Namespace
			if parent.Namespace != nil {
				ns = string(*parent.Namespace)
			}
			var gw v1.Gateway
			if err := c.Get(ctx, client.ObjectKey{Namespace: ns, Name: string(parent.Name)}, &gw); err != nil {
				problem = fmt.Errorf("Gateway reference missing")
				continue
			}
			if string(gw.Spec.GatewayClassName) != options.GatewayClass {
				continue
			}
			var class v1.GatewayClass
			if err := c.Get(ctx, client.ObjectKey{Name: options.GatewayClass}, &class); err != nil || string(class.Spec.ControllerName) != options.controllerName() {
				continue
			}
			result.Parents[resourceID(route)] = append(result.Parents[resourceID(route)], parent)
			result.Objects = appendUniqueObject(result.Objects, &gw)
			result.Objects = appendUniqueObject(result.Objects, &class)
			for _, listener := range gw.Spec.Listeners {
				if parent.SectionName != nil && *parent.SectionName != listener.Name || parent.Port != nil && *parent.Port != listener.Port {
					continue
				}
				if listener.Protocol != "HTTP" || listener.Port != 80 || listener.TLS != nil {
					problem = fmt.Errorf("only HTTP listeners on port 80 are supported; public TLS is Caddy-managed")
					continue
				}
				if err := allowedRoute(ctx, c, gw, listener, *route); err != nil {
					problem = err
					continue
				}
				attached = true
				defaults = gw.Annotations
				if listener.Hostname != nil {
					listenerHosts = append(listenerHosts, string(*listener.Hostname))
				} else {
					listenerHosts = append(listenerHosts, "")
				}
			}
		}
		if len(result.Parents[resourceID(route)]) == 0 {
			continue
		}
		result.Objects = append(result.Objects, route)
		if !attached {
			if problem == nil {
				problem = fmt.Errorf("route not permitted by listener")
			}
			result.Problems[resourceID(route)] = problem.Error()
			continue
		}
		if len(route.Spec.Hostnames) == 0 {
			result.Problems[resourceID(route)] = "explicit hostnames required"
			continue
		}
		staged := []nodeconfig.KubernetesHost{}
		for _, rr := range route.Spec.Rules {
			rule, backends, err := translateRule(ctx, c, route.Namespace, rr, defaults, route.Annotations)
			if err != nil {
				problem = err
				break
			}
			for _, host := range route.Spec.Hostnames {
				matchesListener := false
				for _, lh := range listenerHosts {
					if lh == "" || lh == string(host) {
						matchesListener = true
					}
				}
				if !matchesListener {
					problem = fmt.Errorf("hostname does not match Gateway listener")
					break
				}
				staged = append(staged, nodeconfig.KubernetesHost{Hostname: string(host), Rules: []proxy.HTTPRule{rule}, Backends: backends})
			}
		}
		if problem != nil {
			result.Problems[resourceID(route)] = problem.Error()
			continue
		}
		for _, h := range staged {
			if err := add(h.Hostname, h.Rules[0], h.Backends, route); err != nil {
				result.Problems[resourceID(route)] = err.Error()
			}
		}
	}
	for i := range ingresses.Items {
		ing := &ingresses.Items[i]
		if !wanted(ing.Namespace, options) || ing.Spec.IngressClassName == nil || *ing.Spec.IngressClassName != options.IngressClass {
			continue
		}
		var class networking.IngressClass
		if err := c.Get(ctx, client.ObjectKey{Name: options.IngressClass}, &class); err != nil || class.Spec.Controller != options.controllerName() {
			continue
		}
		result.Objects = append(result.Objects, ing)
		if len(ing.Spec.TLS) > 0 {
			result.Problems[resourceID(ing)] = "TLS Secrets are unsupported; public TLS is Caddy-managed"
			continue
		}
		var problem error
		if len(ing.Spec.Rules) == 0 {
			result.Problems[resourceID(ing)] = "explicit host rules required"
			continue
		}
		var staged []nodeconfig.KubernetesHost
		for _, ir := range ing.Spec.Rules {
			if ir.HTTP == nil || ir.Host == "" {
				problem = fmt.Errorf("explicit HTTP host required")
				break
			}
			paths := append([]networking.HTTPIngressPath{}, ir.HTTP.Paths...)
			if ing.Spec.DefaultBackend != nil {
				t := networking.PathTypePrefix
				paths = append(paths, networking.HTTPIngressPath{Path: "/", PathType: &t, Backend: *ing.Spec.DefaultBackend})
			}
			for _, path := range paths {
				if path.Backend.Service == nil {
					problem = fmt.Errorf("only Service backends supported")
					break
				}
				ref := path.Backend.Service
				port := ref.Port.Number
				if ref.Port.Name != "" {
					var svc core.Service
					if e := c.Get(ctx, client.ObjectKey{Namespace: ing.Namespace, Name: ref.Name}, &svc); e != nil {
						problem = e
						break
					}
					port = 0
					for _, p := range svc.Spec.Ports {
						if p.Name == ref.Port.Name {
							port = p.Port
						}
					}
				}
				backend, e := resolveBackend(ctx, c, ing.Namespace, "Ingress", ing.Namespace, ref.Name, port, 1)
				if e != nil {
					problem = e
					break
				}
				rule := proxy.HTTPRule{Matches: []proxy.Match{{Path: path.Path, Exact: path.PathType != nil && *path.PathType == networking.PathTypeExact}}, Backends: []proxy.Backend{backend}}
				annotations := map[string]string{}
				for k, v := range ing.Annotations {
					if k != AnnotationPrefix+"replace-prefix" {
						annotations[k] = v
					}
				}
				if replacement, ok := ing.Annotations[AnnotationPrefix+"replace-prefix"]; ok {
					if path.PathType == nil || *path.PathType != networking.PathTypePrefix {
						problem = fmt.Errorf("replace-prefix requires Prefix paths")
						break
					}
					rule.Rewrite = &proxy.PrefixRewrite{ReplacePrefix: replacement}
				}
				if e = optionsFromAnnotations(&rule, nil, annotations); e != nil {
					problem = e
					break
				}
				staged = append(staged, nodeconfig.KubernetesHost{Hostname: ir.Host, Rules: []proxy.HTTPRule{rule}, Backends: []proxy.Backend{backend}})
			}
		}
		if problem != nil {
			result.Problems[resourceID(ing)] = problem.Error()
			continue
		}
		for _, h := range staged {
			if e := add(h.Hostname, h.Rules[0], h.Backends, ing); e != nil {
				result.Problems[resourceID(ing)] = e.Error()
			}
		}
	}
	// Reject a resource atomically rather than partially programming its rules.
	for host, h := range hosts {
		var kept []proxy.HTTPRule
		var ids []string
		for i, id := range h.ResourceIDs {
			if result.Problems[id] == "" {
				kept = append(kept, h.Rules[i])
				ids = append(ids, id)
			}
		}
		h.Rules = kept
		h.ResourceIDs = ids
		if len(kept) == 0 {
			delete(hosts, host)
		}
	}
	keys := []string{}
	for host := range hosts {
		keys = append(keys, host)
	}
	sort.Strings(keys)
	for _, host := range keys {
		h := hosts[host]
		sort.SliceStable(h.Rules, func(i, j int) bool { return matchPrecedes(h.Rules[i].Matches[0], h.Rules[j].Matches[0]) })
		result.Hosts = append(result.Hosts, *h)
	}
	return result, nil
}
func matchPrecedes(a, b proxy.Match) bool { return proxy.MatchPrecedes(a, b) }
func appendUniqueObject(objects []client.Object, o client.Object) []client.Object {
	for _, x := range objects {
		if resourceID(x) == resourceID(o) {
			return objects
		}
	}
	return append(objects, o)
}
func allowedRoute(ctx context.Context, c client.Client, gw v1.Gateway, l v1.Listener, r v1.HTTPRoute) error {
	if l.AllowedRoutes != nil && len(l.AllowedRoutes.Kinds) > 0 {
		ok := false
		for _, k := range l.AllowedRoutes.Kinds {
			if k.Kind == "HTTPRoute" && (k.Group == nil || *k.Group == "gateway.networking.k8s.io") {
				ok = true
			}
		}
		if !ok {
			return fmt.Errorf("HTTPRoute kind not allowed")
		}
	}
	from := v1.NamespacesFromSame
	if l.AllowedRoutes != nil && l.AllowedRoutes.Namespaces != nil && l.AllowedRoutes.Namespaces.From != nil {
		from = *l.AllowedRoutes.Namespaces.From
	}
	if from == v1.NamespacesFromAll {
		return nil
	}
	if from == v1.NamespacesFromSame && gw.Namespace == r.Namespace {
		return nil
	}
	if from == v1.NamespacesFromSelector && l.AllowedRoutes.Namespaces.Selector != nil {
		selector, e := metav1.LabelSelectorAsSelector(l.AllowedRoutes.Namespaces.Selector)
		if e != nil {
			return e
		}
		var ns core.Namespace
		if e = c.Get(ctx, client.ObjectKey{Name: r.Namespace}, &ns); e == nil && selector.Matches(labels.Set(ns.Labels)) {
			return nil
		}
	}
	return fmt.Errorf("route namespace not allowed by Gateway")
}
func resolveBackend(ctx context.Context, c client.Client, from, kind, ns, name string, port int32, weight int) (proxy.Backend, error) {
	if ns != from {
		var grants v1beta1.ReferenceGrantList
		if e := c.List(ctx, &grants, client.InNamespace(ns)); e != nil {
			return proxy.Backend{}, e
		}
		allowed := false
		for _, g := range grants.Items {
			for _, f := range g.Spec.From {
				if string(f.Namespace) != from || string(f.Kind) != kind || string(f.Group) != "gateway.networking.k8s.io" {
					continue
				}
				for _, t := range g.Spec.To {
					if t.Group == "" && t.Kind == "Service" && (t.Name == nil || string(*t.Name) == name) {
						allowed = true
					}
				}
			}
		}
		if !allowed {
			return proxy.Backend{}, fmt.Errorf("RefNotPermitted: backend requires ReferenceGrant")
		}
	}
	var svc core.Service
	if e := c.Get(ctx, client.ObjectKey{Namespace: ns, Name: name}, &svc); e != nil {
		return proxy.Backend{}, fmt.Errorf("backend Service missing")
	}
	ip, e := netip.ParseAddr(svc.Spec.ClusterIP)
	if e != nil || svc.Spec.Type == core.ServiceTypeExternalName || len(svc.Spec.ExternalIPs) > 0 {
		return proxy.Backend{}, fmt.Errorf("only internal ClusterIP backends are supported")
	}
	valid := false
	var servicePort core.ServicePort
	for _, p := range svc.Spec.Ports {
		if p.Port == port && (p.Protocol == "" || p.Protocol == core.ProtocolTCP) {
			if p.AppProtocol != nil && *p.AppProtocol != "http" {
				return proxy.Backend{}, fmt.Errorf("only HTTP backend protocol supported")
			}
			valid = true
			servicePort = p
		}
	}
	if !valid {
		return proxy.Backend{}, fmt.Errorf("backend Service port missing or non-TCP")
	}
	var slices discovery.EndpointSliceList
	if e = c.List(ctx, &slices, client.InNamespace(ns), client.MatchingLabels{discovery.LabelServiceName: name}); e != nil {
		return proxy.Backend{}, e
	}
	ready := false
	invalid := false
	endpoints := []proxy.PodEndpoint{}
	for _, slice := range slices.Items {
		for _, endpoint := range slice.Endpoints {
			if endpoint.Conditions.Ready != nil && !*endpoint.Conditions.Ready {
				continue
			}
			if readyPodEndpoint(ctx, c, svc, servicePort, slice, endpoint) {
				ready = true
				var pod core.Pod
				if c.Get(ctx, client.ObjectKey{Namespace: ns, Name: endpoint.TargetRef.Name}, &pod) != nil {
					continue
				}
				var node core.Node
				if pod.Spec.NodeName == "" || c.Get(ctx, client.ObjectKey{Name: pod.Spec.NodeName}, &node) != nil || node.UID == "" {
					continue
				}
				for _, address := range endpoint.Addresses {
					for _, p := range slice.Ports {
						if p.Port != nil && *p.Port == podTargetPort(servicePort, pod) && (p.Protocol == nil || *p.Protocol == core.ProtocolTCP) && (p.Name == nil && servicePort.Name == "" || p.Name != nil && *p.Name == servicePort.Name) {
							endpoints = append(endpoints, proxy.PodEndpoint{Address: net.JoinHostPort(address, strconv.Itoa(int(*p.Port))), PodUID: string(pod.UID), NodeName: pod.Spec.NodeName, NodeUID: string(node.UID)})
						}
					}
				}
			} else {
				invalid = true
			}
		}
	}
	if len(svc.Spec.Selector) == 0 || !ready && invalid {
		return proxy.Backend{}, fmt.Errorf("backend endpoint failed Pod identity validation")
	}
	if weight < 0 || weight > 1000 {
		return proxy.Backend{}, fmt.Errorf("backend weight must be 0–1000")
	}
	sort.Slice(endpoints, func(i, j int) bool {
		a, b := endpoints[i], endpoints[j]
		return a.NodeUID+a.PodUID+a.Address < b.NodeUID+b.PodUID+b.Address
	})
	return proxy.Backend{Key: string(svc.UID) + ":" + strconv.Itoa(int(port)), Endpoints: endpoints, Address: net.JoinHostPort(ip.String(), strconv.Itoa(int(port))), Port: uint16(port), Weight: weight}, nil
}
func translateRule(ctx context.Context, c client.Client, ns string, rr v1.HTTPRouteRule, defaults, annotations map[string]string) (proxy.HTTPRule, []proxy.Backend, error) {
	r := proxy.HTTPRule{}
	if e := optionsFromAnnotations(&r, defaults, annotations); e != nil {
		return r, nil, e
	}
	for _, match := range rr.Matches {
		m := proxy.Match{Headers: map[string]string{}, Query: map[string]string{}}
		if match.Path != nil {
			if match.Path.Type != nil && *match.Path.Type != v1.PathMatchExact && *match.Path.Type != v1.PathMatchPathPrefix {
				return r, nil, fmt.Errorf("regex paths unsupported")
			}
			m.Exact = match.Path.Type != nil && *match.Path.Type == v1.PathMatchExact
			if match.Path.Value != nil {
				m.Path = *match.Path.Value
			}
		}
		if match.Method != nil {
			m.Method = string(*match.Method)
		}
		for _, h := range match.Headers {
			if h.Type != nil && *h.Type != v1.HeaderMatchExact {
				return r, nil, fmt.Errorf("only exact header matches supported")
			}
			m.Headers[string(h.Name)] = h.Value
		}
		for _, q := range match.QueryParams {
			if q.Type != nil && *q.Type != v1.QueryParamMatchExact {
				return r, nil, fmt.Errorf("only exact query matches supported")
			}
			m.Query[string(q.Name)] = q.Value
		}
		r.Matches = append(r.Matches, m)
	}
	if len(r.Matches) == 0 {
		r.Matches = []proxy.Match{{Path: "/"}}
	}
	seenFilters := map[v1.HTTPRouteFilterType]bool{}
	for _, filter := range rr.Filters {
		if seenFilters[filter.Type] {
			return r, nil, fmt.Errorf("duplicate filter type unsupported")
		}
		seenFilters[filter.Type] = true
		switch filter.Type {
		case v1.HTTPRouteFilterRequestHeaderModifier:
			if filter.RequestHeaderModifier == nil {
				return r, nil, fmt.Errorf("missing header modifier")
			}
			r.RequestHeaders = headers(*filter.RequestHeaderModifier)
		case v1.HTTPRouteFilterResponseHeaderModifier:
			if filter.ResponseHeaderModifier == nil {
				return r, nil, fmt.Errorf("missing header modifier")
			}
			r.ResponseHeaders = headers(*filter.ResponseHeaderModifier)
		case v1.HTTPRouteFilterURLRewrite:
			f := filter.URLRewrite
			if f == nil || f.Hostname != nil || f.Path == nil || f.Path.Type != v1.PrefixMatchHTTPPathModifier || f.Path.ReplacePrefixMatch == nil || f.Path.ReplaceFullPath != nil {
				return r, nil, fmt.Errorf("URLRewrite supports only path ReplacePrefixMatch")
			}
			if len(rr.Matches) != 1 || rr.Matches[0].Path == nil || (rr.Matches[0].Path.Type != nil && *rr.Matches[0].Path.Type != v1.PathMatchPathPrefix) {
				return r, nil, fmt.Errorf("ReplacePrefixMatch requires exactly one PathPrefix match")
			}
			r.Rewrite = &proxy.PrefixRewrite{ReplacePrefix: *f.Path.ReplacePrefixMatch}
		case v1.HTTPRouteFilterRequestRedirect:
			f := filter.RequestRedirect
			if f == nil || f.Path != nil {
				return r, nil, fmt.Errorf("redirect path rewriting unsupported")
			}
			d := proxy.Redirect{Status: 302}
			if f.StatusCode != nil {
				d.Status = int(*f.StatusCode)
			}
			if f.Scheme != nil {
				d.Scheme = *f.Scheme
			}
			if f.Hostname != nil {
				d.Host = string(*f.Hostname)
			}
			if f.Port != nil {
				d.Port = uint16(*f.Port)
			}
			r.Redirect = &d
		default:
			return r, nil, fmt.Errorf("unsupported HTTPRoute filter %s", filter.Type)
		}
	}
	if rr.Timeouts != nil {
		if rr.Timeouts.Request != nil {
			r.RequestTimeout = string(*rr.Timeouts.Request)
		}
		if rr.Timeouts.BackendRequest != nil {
			r.BackendRequestTimeout = string(*rr.Timeouts.BackendRequest)
			r.BackendTimeout = ""
		}
	}
	for _, ref := range rr.BackendRefs {
		if ref.Group != nil && *ref.Group != "" || ref.Kind != nil && *ref.Kind != "Service" || ref.Port == nil || len(ref.Filters) > 0 {
			return r, nil, fmt.Errorf("only HTTP Service port references without backend filters supported")
		}
		backendNS := ns
		if ref.Namespace != nil {
			backendNS = string(*ref.Namespace)
		}
		weight := 1
		if ref.Weight != nil {
			weight = int(*ref.Weight)
		}
		b, e := resolveBackend(ctx, c, ns, "HTTPRoute", backendNS, string(ref.Name), int32(*ref.Port), weight)
		if e != nil {
			return r, nil, e
		}
		r.Backends = append(r.Backends, b)
	}
	if r.Redirect != nil && len(r.Backends) > 0 {
		return r, nil, fmt.Errorf("redirect cannot have backends")
	}
	if r.Redirect == nil && len(r.Backends) == 0 {
		return r, nil, fmt.Errorf("HTTP route requires backends")
	}
	if e := proxy.ValidateHTTPRule(r, false); e != nil {
		return r, nil, e
	}
	return r, r.Backends, nil
}
func headers(h v1.HTTPHeaderFilter) proxy.HeaderFilter {
	f := proxy.HeaderFilter{Set: map[string]string{}, Add: map[string]string{}}
	for _, v := range h.Set {
		f.Set[http.CanonicalHeaderKey(string(v.Name))] = v.Value
	}
	for _, v := range h.Add {
		f.Add[http.CanonicalHeaderKey(string(v.Name))] = v.Value
	}
	for _, v := range h.Remove {
		f.Remove = append(f.Remove, string(v))
	}
	return f
}
func optionsFromAnnotations(r *proxy.HTTPRule, defaults, annotations map[string]string) error {
	for _, values := range []map[string]string{defaults, annotations} {
		for key, value := range values {
			if !strings.HasPrefix(key, AnnotationPrefix) {
				continue
			}
			switch strings.TrimPrefix(key, AnnotationPrefix) {
			case "request-timeout":
				r.RequestTimeout = value
			case "backend-timeout":
				r.BackendTimeout = value
			case "max-request-body-bytes":
				n, e := strconv.ParseInt(value, 10, 64)
				if e != nil || n <= 0 {
					return fmt.Errorf("max-request-body-bytes requires positive bytes")
				}
				r.MaxRequestBodyBytes = n
			default:
				return fmt.Errorf("unsupported annotation %s", key)
			}
		}
	}
	for _, value := range []string{r.RequestTimeout, r.BackendTimeout} {
		if value != "" {
			d, e := time.ParseDuration(value)
			if e != nil || d < 0 || d > 24*time.Hour {
				return fmt.Errorf("timeout requires a duration between 0 and 24h")
			}
		}
	}
	return nil
}

func (o Options) controllerName() string {
	if o.ControllerName != "" {
		return o.ControllerName
	}
	return ControllerName
}
