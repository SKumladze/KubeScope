package audit

import (
	"testing"

	auditinternal "k8s.io/apiserver/pkg/apis/audit"
	"k8s.io/apiserver/pkg/audit/policy"
	"k8s.io/apiserver/pkg/authentication/user"
	"k8s.io/apiserver/pkg/authorization/authorizer"
)

// The policy files in cluster/ are loaded and evaluated with the same code the
// API server uses, so a typo or a rule in the wrong order fails here, not on a cluster.

type req struct {
	what   string
	user   string
	groups []string
	verb   string
	group  string
	res    string
	sub    string
	path   string // non-resource requests only
}

func (r req) attrs() authorizer.Attributes {
	a := authorizer.AttributesRecord{
		User: &user.DefaultInfo{Name: r.user, Groups: r.groups},
		Verb: r.verb,
	}
	if r.path != "" {
		a.Path = r.path
		return a
	}
	a.ResourceRequest = true
	a.APIGroup, a.Resource, a.Subresource = r.group, r.res, r.sub
	return a
}

var (
	admin   = "kubernetes-admin"
	kubelet = "system:node:worker-1"
	nodes   = []string{"system:nodes"}
)

func evaluate(t *testing.T, file string, cases []struct {
	r    req
	want auditinternal.Level
}) {
	t.Helper()
	p, err := policy.LoadPolicyFromFile(file)
	if err != nil {
		t.Fatalf("load %s: %v", file, err)
	}
	ev := policy.NewPolicyRuleEvaluator(p)
	for _, c := range cases {
		if got := ev.EvaluatePolicyRule(c.r.attrs()).Level; got != c.want {
			t.Errorf("%s: %s got level %s, want %s", file, c.r.what, got, c.want)
		}
	}
}

func TestProductionPolicy(t *testing.T) {
	evaluate(t, "../../../cluster/audit-policy-production.yaml", []struct {
		r    req
		want auditinternal.Level
	}{
		{req{what: "probe /readyz", user: "system:anonymous", verb: "get", path: "/readyz"}, auditinternal.LevelNone},
		{req{what: "probe /livez?verbose", user: "system:anonymous", verb: "get", path: "/livez/etcd"}, auditinternal.LevelNone},
		{req{what: "discovery /apis/apps/v1", user: admin, verb: "get", path: "/apis/apps/v1"}, auditinternal.LevelNone},
		{req{what: "lease renew", user: kubelet, groups: nodes, verb: "update", group: "coordination.k8s.io", res: "leases"}, auditinternal.LevelNone},
		{req{what: "apiserver self-check", user: "system:apiserver", verb: "get", res: "endpoints"}, auditinternal.LevelNone},
		{req{what: "node heartbeat", user: kubelet, groups: nodes, verb: "patch", res: "nodes", sub: "status"}, auditinternal.LevelNone},
		{req{what: "kubelet pod status", user: kubelet, groups: nodes, verb: "patch", res: "pods", sub: "status"}, auditinternal.LevelRequestResponse},
		{req{what: "create secret", user: admin, verb: "create", res: "secrets"}, auditinternal.LevelMetadata},
		{req{what: "list pods", user: admin, verb: "list", res: "pods"}, auditinternal.LevelMetadata},
		{req{what: "create pod", user: admin, verb: "create", res: "pods"}, auditinternal.LevelRequestResponse},
		{req{what: "scheduler binding", user: "system:kube-scheduler", verb: "create", res: "pods", sub: "binding"}, auditinternal.LevelRequestResponse},
		{req{what: "patch deployment", user: admin, verb: "patch", group: "apps", res: "deployments"}, auditinternal.LevelRequestResponse},
		{req{what: "create rolebinding", user: admin, verb: "create", group: "rbac.authorization.k8s.io", res: "rolebindings"}, auditinternal.LevelRequestResponse},
		{req{what: "create CRD object", user: admin, verb: "create", group: "cilium.io", res: "ciliumendpoints"}, auditinternal.LevelMetadata},
	})
}

func TestLearningPolicy(t *testing.T) {
	evaluate(t, "../../../cluster/audit-policy.yaml", []struct {
		r    req
		want auditinternal.Level
	}{
		{req{what: "probe /readyz", user: "system:anonymous", verb: "get", path: "/readyz"}, auditinternal.LevelMetadata},
		{req{what: "lease renew", user: kubelet, groups: nodes, verb: "update", group: "coordination.k8s.io", res: "leases"}, auditinternal.LevelRequestResponse},
		{req{what: "create secret", user: admin, verb: "create", res: "secrets"}, auditinternal.LevelMetadata},
		{req{what: "list pods", user: admin, verb: "list", res: "pods"}, auditinternal.LevelMetadata},
		{req{what: "create pod", user: admin, verb: "create", res: "pods"}, auditinternal.LevelRequestResponse},
	})
}
