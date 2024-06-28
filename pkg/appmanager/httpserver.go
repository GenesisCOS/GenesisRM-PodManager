package appmanager

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/selection"
	"k8s.io/klog/v2"
	"swiftkube.io/swiftkube/pkg/helper"
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

type ListPodsForDeploymentRequest struct {
	Namespace string `json:"namespace"`
	Name      string `json:"name"`
}

type ListPodsForDeploymentResponse struct {
	Status string        `json:"status"`
	Reason string        `json:"reason"`
	Pods   []*corev1.Pod `json:"pods"`
}

type ListPodsForDeploymentHandler struct {
	appmanager *ApplicationManager
}

func (c *ListPodsForDeploymentHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	resp := ListPodsForDeploymentResponse{
		Status: "success",
		Reason: "",
		Pods:   []*corev1.Pod{},
	}
	var req ListPodsForDeploymentRequest

	if r.Method != http.MethodPost {
		resp.Status = "error"
		resp.Reason = "not a `POST` request"
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

	err = json.Unmarshal(data, &req)
	if err != nil {
		resp.Status = "error"
		resp.Reason = err.Error()
		b, _ := json.Marshal(resp)
		w.Write(b)
		return
	}

	deploy, err := c.appmanager.deployLister.Deployments(req.Namespace).Get(req.Name)
	if err != nil {
		resp.Status = "error"
		resp.Reason = err.Error()
		b, _ := json.Marshal(resp)
		w.Write(b)
		return
	}

	pods, err := c.getPodsForDeployment(deploy)
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

func (c *ListPodsForDeploymentHandler) getPodsForDeployment(d *appsv1.Deployment) ([]*corev1.Pod, error) {
	selector, err := metav1.LabelSelectorAsSelector(d.Spec.Selector)
	if err != nil {
		return nil, err
	}
	pods, err := c.appmanager.podLister.Pods(d.Namespace).List(selector)
	if err != nil {
		return nil, err
	}
	return pods, nil
}

type statHandler struct {
	appmanager *ApplicationManager
}

func (c *statHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	podMap := make(map[string]map[string]map[string]int64)

	pods, err := c.appmanager.podLister.List(labels.Everything())
	if err != nil {
		klog.ErrorS(err, "list pods error")
		return
	}
	for _, pod := range pods {
		if pod.Status.Phase != corev1.PodRunning {
			continue
		}
		enabled, ok := pod.GetLabels()["swiftkube.io/enabled"]
		if !ok {
			continue
		}
		if enabled == "false" {
			continue
		}
		service, ok := pod.GetLabels()["swiftkube.io/service"]
		if !ok {
			continue
		}
		namespace := pod.Namespace
		_, ok = podMap[namespace]
		if !ok {
			podMap[namespace] = make(map[string]map[string]int64)
		}
		_, ok = podMap[namespace][service]
		if !ok {
			podMap[namespace][service] = make(map[string]int64)
		}
		state := helper.GetPodState(pod)
		_, ok = podMap[namespace][service][state.String()]
		if !ok {
			podMap[namespace][service][state.String()] = 0
		}
		_, ok = podMap[namespace][service]["__all__"]
		if !ok {
			podMap[namespace][service]["__all__"] = 0
		}
		podMap[namespace][service][state.String()] += 1
		podMap[namespace][service]["__all__"] += 1
	}

	out := ""

	for namespace, services := range podMap {
		for service, states := range services {
			for state, number := range states {
				out += fmt.Sprintf("genesis_pods_number{service=\"%s\", namespace=\"%s\", state=\"%s\"} %d\n",
					service, namespace, state, number)
			}
		}
	}

	w.Write([]byte(out))
}
