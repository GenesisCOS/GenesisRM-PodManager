package app

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/selection"
	"k8s.io/klog/v2"
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

type statHandler struct {
	appmanager *ApplicationManager
}

func (c *statHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	selector := labels.NewSelector()
	requirement, err := labels.NewRequirement("swiftkube.io/state", selection.Equals, []string{"Ready-Running"})
	if err != nil {
		return
	}
	selector = selector.Add(*requirement)

	podMap := make(map[string]map[string]int64)

	rr_pods, err := c.appmanager.podLister.List(selector)
	if err != nil {
		klog.ErrorS(err, "list pods error")
		return
	}
	for _, pod := range rr_pods {
		if pod.Status.Phase != corev1.PodRunning {
			continue
		}
		namespace := pod.Namespace
		service, ok := pod.GetLabels()["swiftkube.io/service"]
		if !ok {
			continue
		}
		_, ok = podMap[namespace]
		if !ok {
			podMap[namespace] = make(map[string]int64)
		}
		_, ok = podMap[namespace][service]
		if !ok {
			podMap[namespace][service] = 0
		}
		podMap[namespace][service] += 1
	}

	out := ""

	for namespace, services := range podMap {
		for service, nr_rr_pod := range services {
			out += fmt.Sprintf("genesis_rr_pods_number{service=\"%s\", namespace=\"%s\"} %d\n",
				service, namespace, nr_rr_pod)
		}
	}

	w.Write([]byte(out))
}
