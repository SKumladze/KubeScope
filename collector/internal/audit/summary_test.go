package audit

import (
	"testing"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	auditv1 "k8s.io/apiserver/pkg/apis/audit/v1"

	"kubescope/internal/model"
)

func TestClassify(t *testing.T) {
	cases := []struct{ user, agent, lane, instance string }{
		{"kubernetes-admin", "kubectl/v1.33.0 (linux/amd64)", model.LaneKubectl, ""},
		{"system:node:kubescope-worker", "kubelet/v1.35.0", model.LaneKubelet, "kubescope-worker"},
		{"system:serviceaccount:kube-system:replicaset-controller", "kube-controller-manager/v1.35.0", model.LaneControllerManager, "replicaset-controller"},
		{"system:anonymous", "kube-probe/1.35", model.LaneProbe, ""},
		{"system:anonymous", "curl/8.10.1", model.LaneOther, "anonymous curl"},
		{"", "curl/7.81.0", model.LaneOther, "anonymous curl"},
		{"system:serviceaccount:default:default", "curl/8.10.1", model.LaneOther, "default"},
		{"kubernetes-admin", "Helm/3.19.2", model.LaneOther, "Helm"},
	}
	for _, c := range cases {
		lane, inst := classify(c.user, c.agent)
		if lane != c.lane || inst != c.instance {
			t.Errorf("classify(%q, %q) = %q, %q; want %q, %q", c.user, c.agent, lane, inst, c.lane, c.instance)
		}
	}
}

func TestSummaries(t *testing.T) {
	base := model.Event{From: model.LaneOther, FromInstance: "curl"}
	cases := []struct {
		name string
		edit func(e *model.Event)
		want string
	}{
		{"created", func(e *model.Event) { e.Verb, e.Resource, e.Namespace, e.Name, e.Code = "create", "pods", "default", "web", 201 }, "curl created pod default/web"},
		{"conflict", func(e *model.Event) { e.Verb, e.Resource, e.Name, e.Code = "create", "namespaces", "podinfo", 409 }, "curl tried to create namespace podinfo, failed with 409"},
		{"bad body", func(e *model.Event) { e.Verb, e.Resource, e.Namespace, e.Code = "create", "pods", "default", 400 }, "curl tried to create pod in default, failed with 400"},
		{"not found", func(e *model.Event) { e.Verb, e.Resource, e.Namespace, e.Name, e.Code = "get", "pods", "default", "x", 404 }, "curl tried to read pod default/x, failed with 404"},
		{"forbidden list", func(e *model.Event) { e.Verb, e.Resource, e.Namespace, e.Code = "list", "pods", "default", 403 }, "curl tried to list pods in default, failed with 403"},
		{"status ok", func(e *model.Event) { e.Verb, e.Resource, e.Subresource, e.Namespace, e.Name, e.Code = "patch", "pods", "status", "ns", "p", 200 }, "curl updated status of pod ns/p"},
	}
	for _, c := range cases {
		e := base
		c.edit(&e)
		if got := summarize(&e, nil); got != c.want {
			t.Errorf("%s: got %q, want %q", c.name, got, c.want)
		}
	}
}

func TestLongRunningDrawnOnce(t *testing.T) {
	ev := func(verb, sub string, stage auditv1.Stage) *auditv1.Event {
		a := &auditv1.Event{Verb: verb, Stage: stage, ObjectRef: &auditv1.ObjectReference{Resource: "pods", Subresource: sub}}
		a.ResponseStatus = &metav1.Status{Code: 200}
		return a
	}
	cases := []struct {
		verb, sub string
		stage     auditv1.Stage
		drawn     bool
	}{
		{"get", "log", auditv1.StageResponseStarted, true},
		{"get", "log", auditv1.StageResponseComplete, false},
		{"create", "portforward", auditv1.StageResponseComplete, false},
		{"get", "proxy", auditv1.StageResponseComplete, false},
		{"create", "exec", auditv1.StageResponseComplete, false},
		{"watch", "", auditv1.StageResponseStarted, true},
		{"watch", "", auditv1.StageResponseComplete, false},
		{"create", "", auditv1.StageResponseComplete, true}, // normal request: only one stage
		{"patch", "status", auditv1.StageResponseComplete, true},
	}
	for _, c := range cases {
		if _, ok := Convert(ev(c.verb, c.sub, c.stage)); ok != c.drawn {
			t.Errorf("%s %s %s: drawn=%v, want %v", c.verb, c.sub, c.stage, ok, c.drawn)
		}
	}
}
