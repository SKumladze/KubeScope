package audit

import (
	"strings"

	"kubescope/internal/model"
)

// classify decides which component sent a request, based on the
// authenticated user and the user agent string.
//
// Examples of what the API server reports:
//
//	user=system:node:kubescope-worker           agent=kubelet/v1.35.0 ...
//	user=system:kube-scheduler                  agent=kube-scheduler/v1.35.0 ...
//	user=system:serviceaccount:kube-system:replicaset-controller
//	                                            agent=kube-controller-manager/v1.35.0 ...
//	user=kubernetes-admin                       agent=kubectl/v1.33.0 ...
func classify(user, agent string) (lane, instance string) {
	const saPrefix = "system:serviceaccount:"
	sa := ""
	if strings.HasPrefix(user, saPrefix) {
		// "kube-system:replicaset-controller" -> "replicaset-controller"
		parts := strings.SplitN(strings.TrimPrefix(user, saPrefix), ":", 2)
		if len(parts) == 2 {
			sa = parts[1]
		}
	}

	switch {
	case strings.HasPrefix(agent, "kubectl"):
		return model.LaneKubectl, ""
	case strings.HasPrefix(user, "system:node:"):
		return model.LaneKubelet, strings.TrimPrefix(user, "system:node:")
	case user == "system:kube-scheduler" || strings.HasPrefix(agent, "kube-scheduler"):
		return model.LaneScheduler, ""
	case strings.HasPrefix(agent, "kube-controller-manager") || user == "system:kube-controller-manager":
		return model.LaneControllerManager, sa
	case user == "system:kube-proxy" || strings.HasPrefix(agent, "kube-proxy"):
		return model.LaneKubeProxy, ""
	case sa == "coredns" || strings.HasPrefix(agent, "coredns"):
		return model.LaneCoreDNS, ""
	case sa == "kindnet" || strings.HasPrefix(agent, "kindnetd"):
		return model.LaneCNI, "kindnet"
	case sa == "local-path-provisioner-service-account" || strings.HasPrefix(agent, "local-path-provisioner"):
		return model.LaneStorage, "local-path"
	case user == "system:apiserver" || strings.HasPrefix(agent, "kube-apiserver"):
		return model.LaneAPIServer, ""
	case strings.HasPrefix(agent, "kube-probe"):
		return model.LaneProbe, ""
	case user == "system:anonymous" || user == "":
		// No credentials, or credentials the API server rejected (401).
		return model.LaneOther, strings.TrimSpace("anonymous " + firstWord(agent))
	}
	if sa != "" {
		return model.LaneOther, sa
	}
	return model.LaneOther, firstWord(agent)
}

// noiseOf tags requests that happen all the time and hide the interesting ones.
func noiseOf(e *model.Event) string {
	switch {
	case e.Verb == "watch":
		return model.NoiseWatch
	case e.Resource == "leases":
		return model.NoiseLease
	case e.From == model.LaneKubelet && e.Resource == "nodes" && (e.Subresource == "status" || e.Verb == "get"):
		return model.NoiseHeartbeat
	case e.Resource == "tokenreviews" || e.Resource == "subjectaccessreviews" ||
		e.Resource == "selfsubjectaccessreviews" || e.Subresource == "token":
		return model.NoiseAuth
	case e.Resource == "" && isHealthPath(e.URI):
		return model.NoiseHealth
	case e.Resource == "" && isDiscoveryPath(e.URI):
		return model.NoiseDiscovery
	case e.From == model.LaneAPIServer && !e.Write:
		// e.g. every 10s it checks the endpoints of its own "kubernetes" Service
		return model.NoiseInternal
	}
	return ""
}

func isHealthPath(uri string) bool {
	for _, p := range []string{"/healthz", "/livez", "/readyz", "/metrics"} {
		if strings.HasPrefix(uri, p) {
			return true
		}
	}
	return false
}

func isDiscoveryPath(uri string) bool {
	for _, p := range []string{"/api", "/apis", "/openapi", "/version", "/.well-known"} {
		if uri == p || strings.HasPrefix(uri, p+"?") || strings.HasPrefix(uri, p+"/") {
			return true
		}
	}
	return false
}

func firstWord(agent string) string {
	if i := strings.IndexAny(agent, "/ "); i > 0 {
		return agent[:i]
	}
	return agent
}
