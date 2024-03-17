package app

import (
	"encoding/json"
	"io"
	"net/http"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/selection"
)

type listerResponse struct {
	Status string        `json:"status"`
	Reason string        `json:"reason"`
	Pods   []*corev1.Pod `json:"pods"`
}

type listerRequest struct {
	Namespace string `json:"namespace"`
	Label     string `json:"label"`
	Value     string `json:"value"`
}

type listerHandler struct {
	manager *ApplicationManager
}

func (c *listerHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {

	resp := listerResponse{
		Status: "success",
		Reason: "",
		Pods:   []*corev1.Pod{},
	}

	if r.Method != http.MethodPost {
		resp.Status = "error"
		resp.Reason = "Must be POST request"
		b, _ := json.Marshal(resp)
		w.Write(b)
		return
	}

	data, err := io.ReadAll(r.Body)
	if err != nil {
		resp.Status = "error"
		resp.Reason = err.Error()
		b, _ := json.Marshal(resp)
		w.Write(b)
		return
	}

	var req listerRequest
	err = json.Unmarshal(data, &req)
	if err != nil {
		resp.Status = "error"
		resp.Reason = err.Error()
		b, _ := json.Marshal(resp)
		w.Write(b)
		return
	}

	namespace := req.Namespace
	selector := labels.NewSelector()

	requirement, err := labels.NewRequirement(req.Label, selection.Equals, []string{req.Value})
	if err != nil {
		resp.Status = "error"
		resp.Reason = err.Error()
		b, _ := json.Marshal(resp)
		w.Write(b)
		return
	}

	selector = selector.Add(*requirement)

	pods, err := c.manager.podLister.Pods(namespace).List(selector)
	if err != nil {
		resp.Status = "error"
		resp.Reason = err.Error()
		b, _ := json.Marshal(resp)
		w.Write(b)
		return
	}

	resp.Pods = pods
	b, _ := json.Marshal(resp)
	w.Write(b)
}
