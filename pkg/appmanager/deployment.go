package appmanager

import (
	"encoding/json"
	"net/http"
	"time"

	"github.com/emicklei/go-restful/v3"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/cri-api/pkg/errors"
	"k8s.io/klog/v2"

	genesistypes "swiftkube.io/swiftkube/pkg/appmanager/types"
)

type PodList struct {
	Pods []*genesistypes.PodInfo `json:"pods"`
}

type PodListForDeployment struct {
	Namespace string                  `json:"namespace"`
	Name      string                  `json:"name"` // deployment name
	Pods      []*genesistypes.PodInfo `json:"pods"`
}

type PodListForDeployments struct {
	Pods []*PodListForDeployment `json:"pods"`
}

type RequestObjectMetaInfo struct {
	Name      string `json:"name"`
	Namespace string `json:"namespace"`
}

type PodListForDeploymentsRequest struct {
	Metadatas []*RequestObjectMetaInfo `json:"metadatas"`
}

type DeploymentWithMetadataInfo struct {
	Deployment *genesistypes.DeploymentInfo `json:"deployment"`
	Name       string                       `json:"name"`
	Namespace  string                       `json:"namespace"`
}

type DeploymentList struct {
	Deployments []*DeploymentWithMetadataInfo `json:"deployments"`
}

type GetDeploymentListRequest struct {
	Metadatas []*RequestObjectMetaInfo `json:"metadatas"`
}

type Deployment struct {
	Deployment *genesistypes.DeploymentInfo `json:"deployment"`
}

type DeploymentHelperService struct {
	appmanager *ApplicationManager
}

func NewDeploymentHelperWebService(appmanager *ApplicationManager) *DeploymentHelperService {
	klog.InfoS("new DeploymentHelperService")
	retval := &DeploymentHelperService{
		appmanager: appmanager,
	}

	return retval
}

func (s *DeploymentHelperService) getDeployment(name string, namespace string) (*genesistypes.DeploymentInfo, error) {
	deploy, err := s.appmanager.deployLister.Deployments(namespace).Get(name)
	if err != nil {
		return nil, err
	}
	return genesistypes.NewDeploymentInfo(deploy), nil
}

func (s *DeploymentHelperService) getDeploymentsHandler(request *restful.Request, response *restful.Response) {
	decoder := json.NewDecoder(request.Request.Body)
	var deployListReq GetDeploymentListRequest
	err := decoder.Decode(&deployListReq)
	if err != nil {
		response.WriteError(http.StatusInternalServerError, err)
		return
	}
	deployList := DeploymentList{
		Deployments: make([]*DeploymentWithMetadataInfo, 0),
	}
	for _, metadata := range deployListReq.Metadatas {
		deploy, err := s.getDeployment(metadata.Name, metadata.Namespace)
		if err != nil {
			response.WriteError(http.StatusInternalServerError, err)
			return
		}
		deployWithMetadata := &DeploymentWithMetadataInfo{
			Name:       metadata.Name,
			Namespace:  metadata.Namespace,
			Deployment: deploy,
		}
		deployList.Deployments = append(deployList.Deployments, deployWithMetadata)
	}
	response.WriteEntity(deployList)
}

func (s *DeploymentHelperService) getDeploymentHandler(request *restful.Request, response *restful.Response) {
	deploy, err := s.getDeployment(request.PathParameter("name"), request.PathParameter("namespace"))
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

func (s *DeploymentHelperService) listPodsForDeploymentsHandler(request *restful.Request, response *restful.Response) {
	t0 := time.Now()
	decoder := json.NewDecoder(request.Request.Body)
	var podListForDeploymentsRequest PodListForDeploymentsRequest
	err := decoder.Decode(&podListForDeploymentsRequest)
	if err != nil {
		response.WriteError(http.StatusInternalServerError, err)
		return
	}
	podListForDeployments := PodListForDeployments{
		Pods: make([]*PodListForDeployment, 0),
	}
	for _, metadata := range podListForDeploymentsRequest.Metadatas {
		pods, err := s.listPodsForDeployment(metadata.Name, metadata.Namespace)
		if err != nil {
			response.WriteError(http.StatusInternalServerError, err)
			return
		}
		podInfos := make([]*genesistypes.PodInfo, 0)
		for _, pod := range pods {
			podInfos = append(podInfos, genesistypes.NewPodInfo(pod))
		}
		podListForDeployment := &PodListForDeployment{
			Name:      metadata.Name,
			Namespace: metadata.Namespace,
			Pods:      podInfos,
		}
		podListForDeployments.Pods = append(podListForDeployments.Pods, podListForDeployment)
	}
	t1 := time.Now()
	t := float64((t1.Second()*1000000000 + t1.Nanosecond()) - (t0.Second()*1000000000 + t0.Nanosecond()))
	klog.InfoS("list pods for deployments", "time", t/1000000000)
	response.WriteEntity(podListForDeployments)
}

func (s *DeploymentHelperService) listPodsForDeployment(name string, namespace string) ([]*corev1.Pod, error) {
	deploy, err := s.appmanager.deployLister.Deployments(namespace).Get(name)
	if err != nil {
		return make([]*corev1.Pod, 0), err
	}

	selector, err := metav1.LabelSelectorAsSelector(deploy.Spec.Selector)
	if err != nil {
		return make([]*corev1.Pod, 0), err
	}

	pods, err := s.appmanager.podLister.Pods(deploy.Namespace).List(selector)
	if err != nil {
		return make([]*corev1.Pod, 0), err
	}

	return pods, nil
}

func (s *DeploymentHelperService) listPodsForDeploymentHandler(request *restful.Request, response *restful.Response) {
	pods, err := s.listPodsForDeployment(request.PathParameter("name"), request.PathParameter("namespace"))
	if err != nil {
		response.WriteError(http.StatusInternalServerError, err)
		return
	}
	podInfos := make([]*genesistypes.PodInfo, 0)
	for _, pod := range pods {
		podInfos = append(podInfos, genesistypes.NewPodInfo(pod))
	}
	response.WriteEntity(PodList{Pods: podInfos})
}

func (s *DeploymentHelperService) WebService() *restful.WebService {
	ws := new(restful.WebService)

	ws.Path("/api/v1/deployments").Consumes(restful.MIME_JSON).Produces(restful.MIME_JSON)

	ws.Route(ws.GET("/pods/{namespace}/{name}").To(s.listPodsForDeploymentHandler).
		Doc("list pods for deployment").
		Param(ws.PathParameter("namespace", "namespace").DataType("string")).
		Param(ws.PathParameter("name", "name").DataType("string")).
		Writes(PodList{}).
		Returns(200, "OK", PodList{}).
		Returns(404, "Not Found", nil))

	ws.Route(ws.POST("/pods").To(s.listPodsForDeploymentsHandler).
		Doc("list pods for deployments").
		Writes(PodListForDeployments{}).
		Returns(200, "OK", PodListForDeployments{}).
		Returns(404, "Not Found", nil))

	ws.Route(ws.GET("/deployment/{namespace}/{name}").To(s.getDeploymentHandler).
		Doc("find a deployment").
		Param(ws.PathParameter("namespace", "namespace").DataType("string")).
		Param(ws.PathParameter("name", "name").DataType("string")).
		Writes(Deployment{}).
		Returns(200, "OK", Deployment{}).
		Returns(404, "Not Found", nil))

	ws.Route(ws.POST("/deployments").To(s.getDeploymentsHandler).
		Doc("find deployments").
		Writes(DeploymentList{}).
		Returns(200, "OK", DeploymentList{}).
		Returns(404, "Not Found", nil))

	return ws
}
