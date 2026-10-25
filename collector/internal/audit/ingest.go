// Package audit turns API server audit webhook payloads into model events.
package audit

import (
	"encoding/json"
	"fmt"
	"strings"

	auditv1 "k8s.io/apiserver/pkg/apis/audit/v1"

	"kubescope/internal/model"
)

// maxBody caps stored request/response bodies so one huge object can't eat memory.
const maxBody = 512 * 1024

// Convert maps one audit event to a model event plus its detail.
// ok is false for events we don't draw: the closing stage of a long-running
// request (its start was already drawn when it opened).
func Convert(a *auditv1.Event) (d model.Detail, ok bool) {
	if a.Stage == auditv1.StageResponseComplete && isLongRunning(a) {
		return d, false
	}

	lane, instance := classify(a.User.Username, a.UserAgent)
	e := model.Event{
		Time:         a.StageTimestamp.Time,
		Source:       "audit",
		AuditID:      string(a.AuditID),
		Stage:        string(a.Stage),
		From:         lane,
		FromInstance: instance,
		To:           model.LaneAPIServer,
		User:         a.User.Username,
		UserAgent:    a.UserAgent,
		Verb:         a.Verb,
		Method:       methodOf(a.Verb),
		URI:          a.RequestURI,
		LatencyMs:    float64(a.StageTimestamp.Sub(a.RequestReceivedTimestamp.Time).Microseconds()) / 1000,
	}
	if len(a.SourceIPs) > 0 {
		e.SourceIP = a.SourceIPs[0]
	}
	if r := a.ObjectRef; r != nil {
		e.APIGroup, e.APIVersion = r.APIGroup, r.APIVersion
		e.Resource, e.Subresource = r.Resource, r.Subresource
		e.Namespace, e.Name, e.UID = r.Namespace, r.Name, string(r.UID)
	}
	if a.ResponseStatus != nil {
		e.Code = int(a.ResponseStatus.Code)
	}
	e.Write = isWrite(a.Verb)
	e.Noise = noiseOf(&e)

	if a.RequestObject != nil {
		d.RequestObject = clip(a.RequestObject.Raw)
	}
	if a.ResponseObject != nil {
		d.ResponseObject = clip(a.ResponseObject.Raw)
	}
	// A create has no name in the URL (and may use generateName), so take
	// the final name and UID from the object the API server returned.
	if e.Name == "" || e.UID == "" {
		if m := metaOf(d.ResponseObject); m != nil && isObject(d.ResponseObject) {
			if e.Name == "" {
				e.Name = m.Name
			}
			if e.UID == "" {
				e.UID = m.UID
			}
		}
	}
	if a.ResponseStatus != nil {
		d.ResponseStatus, _ = json.Marshal(a.ResponseStatus)
	}
	e.HasBody = len(d.RequestObject) > 0 || len(d.ResponseObject) > 0
	e.Summary = summarize(&e, d.RequestObject)
	d.Annotations = a.Annotations
	d.Event = e
	return d, true
}

// isLongRunning matches the requests the API server reports twice: once when the
// response starts (ResponseStarted) and once when it ends (ResponseComplete).
// Same list as the API server's own long-running check.
func isLongRunning(a *auditv1.Event) bool {
	if a.Verb == "watch" || a.Verb == "proxy" {
		return true
	}
	if a.ObjectRef == nil {
		return false
	}
	switch a.ObjectRef.Subresource {
	case "log", "exec", "attach", "portforward", "proxy":
		return true
	}
	return false
}

func clip(raw []byte) json.RawMessage {
	if len(raw) == 0 {
		return nil
	}
	if len(raw) > maxBody {
		msg, _ := json.Marshal(map[string]any{"kubescope": fmt.Sprintf("body truncated, %d bytes", len(raw))})
		return msg
	}
	return json.RawMessage(raw)
}

type objectMeta struct {
	Name string `json:"name"`
	UID  string `json:"uid"`
}

func metaOf(raw json.RawMessage) *objectMeta {
	if len(raw) == 0 {
		return nil
	}
	var o struct {
		Metadata objectMeta `json:"metadata"`
	}
	if json.Unmarshal(raw, &o) != nil || o.Metadata.Name == "" {
		return nil
	}
	return &o.Metadata
}

// isObject guards against taking the name from a Status object
// (returned for deletes and errors) instead of the real resource.
func isObject(raw json.RawMessage) bool {
	var o struct {
		Kind string `json:"kind"`
	}
	if json.Unmarshal(raw, &o) != nil {
		return false
	}
	return o.Kind != "Status" && o.Kind != ""
}

func isWrite(verb string) bool {
	switch verb {
	case "create", "update", "patch", "delete", "deletecollection":
		return true
	}
	return false
}

func methodOf(verb string) string {
	switch verb {
	case "create":
		return "POST"
	case "update":
		return "PUT"
	case "patch":
		return "PATCH"
	case "delete", "deletecollection":
		return "DELETE"
	case "get", "list", "watch":
		return "GET"
	}
	return strings.ToUpper(verb)
}

var pastTense = map[string]string{
	"create":           "created",
	"update":           "updated",
	"patch":            "patched",
	"delete":           "deleted",
	"deletecollection": "deleted all",
	"get":              "read",
	"list":             "listed",
	"watch":            "started watching",
}

// tried is used for failed requests: "curl tried to create pod x, failed with 409".
var tried = map[string]string{
	"deletecollection": "delete all",
	"get":              "read",
	"watch":            "watch",
}

// summarize writes one line in plain words, e.g.
// "scheduler bound pod default/nginx-7c5d to node kubescope-worker2".
func summarize(e *model.Event, req json.RawMessage) string {
	s := describe(e, req)
	if e.Code >= 400 {
		s += fmt.Sprintf(", failed with %d", e.Code)
	}
	return s
}

func describe(e *model.Event, req json.RawMessage) string {
	actor := e.From
	if e.FromInstance != "" {
		actor = e.FromInstance
		if e.From == model.LaneKubelet {
			actor = "kubelet on " + e.FromInstance
		}
	}

	if e.Resource == "" {
		return fmt.Sprintf("%s called %s %s", actor, e.Method, e.URI)
	}

	obj := strings.TrimSuffix(e.Resource, "s")
	if strings.HasSuffix(e.Resource, "ies") {
		obj = strings.TrimSuffix(e.Resource, "ies") + "y"
	} else if strings.HasSuffix(e.Resource, "sses") || strings.HasSuffix(e.Resource, "ches") {
		obj = strings.TrimSuffix(e.Resource, "es")
	}
	target := e.Name
	if e.Namespace != "" && target != "" {
		target = e.Namespace + "/" + target
	}

	failed := e.Code >= 400
	switch {
	case failed:
		// No special wording: the generic "tried to ..." below fits every failure.
	case e.Resource == "pods" && e.Subresource == "binding":
		var b struct {
			Target struct{ Name string } `json:"target"`
		}
		_ = json.Unmarshal(req, &b)
		if b.Target.Name != "" {
			return fmt.Sprintf("%s bound pod %s to node %s", actor, target, b.Target.Name)
		}
	case e.Resource == "events" && e.Verb == "create":
		var ev struct {
			Reason  string `json:"reason"`
			Message string `json:"message"`
			Note    string `json:"note"`
		}
		_ = json.Unmarshal(req, &ev)
		msg := ev.Message
		if msg == "" {
			msg = ev.Note
		}
		if ev.Reason != "" {
			return fmt.Sprintf("%s recorded event %s: %s", actor, ev.Reason, msg)
		}
	case e.Subresource == "status" && (e.Verb == "patch" || e.Verb == "update"):
		return fmt.Sprintf("%s updated status of %s %s", actor, obj, target)
	}

	verb := pastTense[e.Verb]
	if failed {
		verb = tried[e.Verb]
		if verb == "" {
			verb = e.Verb
		}
		verb = "tried to " + verb
	}
	if verb == "" {
		verb = e.Verb
	}
	if e.Verb == "list" || e.Verb == "watch" || e.Verb == "deletecollection" {
		where := ""
		if e.Namespace != "" {
			where = " in " + e.Namespace
		}
		return fmt.Sprintf("%s %s %s%s", actor, verb, e.Resource, where)
	}
	sub := ""
	if e.Subresource != "" {
		sub = " (" + e.Subresource + ")"
	}
	if target == "" && e.Namespace != "" {
		target = "in " + e.Namespace // e.g. a create rejected before the name was read
	}
	return strings.TrimSpace(fmt.Sprintf("%s %s %s %s", actor, verb, obj, target)) + sub
}
