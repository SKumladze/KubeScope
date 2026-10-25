// Package model holds the unified event shape every data source is converted into.
package model

import (
	"encoding/json"
	"time"
)

// Lanes are the columns of the sequence diagram.
const (
	LaneEtcd              = "etcd"
	LaneAPIServer         = "apiserver"
	LaneKubectl           = "kubectl"
	LaneControllerManager = "controller-manager"
	LaneScheduler         = "scheduler"
	LaneKubelet           = "kubelet"
	LaneKubeProxy         = "kube-proxy"
	LaneCoreDNS           = "coredns"
	LaneCNI               = "cni"
	LaneStorage           = "storage"
	LaneProbe             = "probes"
	LaneOther             = "other"
)

// Noise categories. Events with a non-empty Noise are hidden in the UI by default.
const (
	NoiseLease     = "lease"
	NoiseHeartbeat = "heartbeat"
	NoiseWatch     = "watch"
	NoiseDiscovery = "discovery"
	NoiseHealth    = "health"
	NoiseAuth      = "auth"
	NoiseInternal  = "internal" // the API server reading its own state
)

// SourceMarker is a labelled divider (from the UI or the tour script), not an API call.
const SourceMarker = "marker"

// Event is one arrow in the diagram. It is sent to the browser as is,
// so it carries no request or response bodies (see Detail).
type Event struct {
	ID           uint64    `json:"id"`
	Time         time.Time `json:"time"`
	Source       string    `json:"source"`
	AuditID      string    `json:"auditId,omitempty"`
	Stage        string    `json:"stage,omitempty"`
	From         string    `json:"from"`
	FromInstance string    `json:"fromInstance,omitempty"`
	To           string    `json:"to"`
	User         string    `json:"user,omitempty"`
	UserAgent    string    `json:"userAgent,omitempty"`
	SourceIP     string    `json:"sourceIp,omitempty"`
	Verb         string    `json:"verb"`
	Method       string    `json:"method,omitempty"`
	APIGroup     string    `json:"apiGroup,omitempty"`
	APIVersion   string    `json:"apiVersion,omitempty"`
	Resource     string    `json:"resource,omitempty"`
	Subresource  string    `json:"subresource,omitempty"`
	Namespace    string    `json:"namespace,omitempty"`
	Name         string    `json:"name,omitempty"`
	UID          string    `json:"uid,omitempty"`
	URI          string    `json:"uri,omitempty"`
	Code         int       `json:"code"`
	LatencyMs    float64   `json:"latencyMs"`
	Write        bool      `json:"write"`
	Noise        string    `json:"noise,omitempty"`
	Summary      string    `json:"summary"`
	HasBody      bool      `json:"hasBody"`
}

// Detail is an event plus everything needed for the details panel.
type Detail struct {
	Event
	RequestObject  json.RawMessage   `json:"requestObject,omitempty"`
	ResponseObject json.RawMessage   `json:"responseObject,omitempty"`
	ResponseStatus json.RawMessage   `json:"responseStatus,omitempty"`
	Annotations    map[string]string `json:"annotations,omitempty"`
}
