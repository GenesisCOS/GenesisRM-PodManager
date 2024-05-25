package appmanager

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"

	restful "github.com/emicklei/go-restful/v3"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
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

type PodList struct {
	Pods []*corev1.Pod `json:"pods"`
}

type Deployment struct {
	Deployment *appsv1.Deployment `json:"deployment"`
}

type DeploymentHelperService struct {
	appmanager *ApplicationManager
}

func NewDeploymentHelperWebService(appmanager *ApplicationManager) *DeploymentHelperService {
	return &DeploymentHelperService{
		appmanager: appmanager,
	}
}

func (s *DeploymentHelperService) getDeployment(request *restful.Request, response *restful.Response) {
	deploy, err := s.appmanager.deployLister.
		Deployments(request.PathParameter("namespace")).
		Get(request.PathParameter("name"))
	if err != nil {
		if errors.IsNotFound(err) {
			response.WriteError(http.StatusNotFound, err)
		} else {
			response.WriteError(http.StatusInternalServerError, err)
		}
		return
	}

	response.WriteEntity(Deployment{Deployment: deploy})
}

func (s *DeploymentHelperService) listPodsForDeployment(request *restful.Request, response *restful.Response) {
	deploy, err := s.appmanager.deployLister.
		Deployments(request.PathParameter("namespace")).
		Get(request.PathParameter("name"))
	if err != nil {
		response.WriteError(http.StatusInternalServerError, err)
		return
	}

	selector, err := metav1.LabelSelectorAsSelector(deploy.Spec.Selector)
	if err != nil {
		response.WriteError(http.StatusInternalServerError, err)
		return
	}
	pods, err := s.appmanager.podLister.Pods(deploy.Namespace).List(selector)
	if err != nil {
		response.WriteError(http.StatusInternalServerError, err)
		return
	}
	response.WriteEntity(PodList{Pods: pods})
}

func (s *DeploymentHelperService) WebService() *restful.WebService {
	ws := new(restful.WebService)

	ws.Path("/api/v1/deployments").Consumes(restful.MIME_JSON).Produces(restful.MIME_JSON)

	ws.Route(ws.GET("/pods/{namespace}/{name}").To(s.listPodsForDeployment).
		Doc("list pods for deployment").
		Param(ws.PathParameter("namespace", "namespace").DataType("string")).
		Param(ws.PathParameter("name", "name").DataType("string")).
		Writes(PodList{}).
		Returns(200, "OK", PodList{}).
		Returns(404, "Not Found", nil))

	ws.Route(ws.GET("/{namespace}/{name}").To(s.getDeployment).
		Doc("find a deployment").
		Param(ws.PathParameter("namespace", "namespace").DataType("string")).
		Param(ws.PathParameter("name", "name").DataType("string")).
		Writes(Deployment{}).
		Returns(200, "OK", Deployment{}).
		Returns(404, "Not Found", nil))

	return ws
}
