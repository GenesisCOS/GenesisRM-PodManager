package appmanager

import (
	"net/http"

	"github.com/emicklei/go-restful/v3"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/cri-api/pkg/errors"
)

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

	ws.Route(ws.GET("/deployment/{namespace}/{name}").To(s.getDeployment).
		Doc("find a deployment").
		Param(ws.PathParameter("namespace", "namespace").DataType("string")).
		Param(ws.PathParameter("name", "name").DataType("string")).
		Writes(Deployment{}).
		Returns(200, "OK", Deployment{}).
		Returns(404, "Not Found", nil))

	return ws
}
