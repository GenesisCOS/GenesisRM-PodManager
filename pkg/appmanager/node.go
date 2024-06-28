package appmanager

import (
	"net/http"

	"github.com/emicklei/go-restful/v3"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/selection"
	"k8s.io/cri-api/pkg/errors"
)

type Node struct {
	Node *corev1.Node `json:"node"`
}

type NodeList struct {
	Nodes []*corev1.Node `json:"nodes"`
}

type NodeHelperService struct {
	appmanager *ApplicationManager
}

func NewNodeHelperWebService(appmanager *ApplicationManager) *NodeHelperService {
	return &NodeHelperService{
		appmanager: appmanager,
	}
}

func (s *NodeHelperService) getNode(request *restful.Request, response *restful.Response) {
	node, err := s.appmanager.nodeLister.Get(request.PathParameter("name"))
	if err != nil {
		if errors.IsNotFound(err) {
			response.WriteError(http.StatusNotFound, err)
		} else {
			response.WriteError(http.StatusInternalServerError, err)
		}
		return
	}

	response.WriteEntity(Node{Node: node})
}

func (s *NodeHelperService) listNodesByRole(request *restful.Request, response *restful.Response) {
	selector := labels.NewSelector()
	requirement, err := labels.NewRequirement("kubernetes.io/role", selection.Equals, []string{request.PathParameter("role")})
	if err != nil {
		response.WriteError(http.StatusInternalServerError, err)
		return
	}
	selector = selector.Add(*requirement)
	nodes, err := s.appmanager.nodeLister.List(selector)
	if err != nil {
		response.WriteError(http.StatusInternalServerError, err)
		return
	}
	response.WriteEntity(NodeList{Nodes: nodes})
}

func (s *NodeHelperService) WebService() *restful.WebService {
	ws := new(restful.WebService)

	ws.Path("/api/v1/nodes").Consumes(restful.MIME_JSON).Produces(restful.MIME_JSON)

	ws.Route(ws.GET("/list-by-role/{role}").To(s.listNodesByRole).
		Doc("list nodes").
		Writes(NodeList{}).
		Returns(200, "OK", NodeList{}).
		Returns(404, "Not Found", nil))

	ws.Route(ws.GET("/node/{name}").To(s.getNode).
		Doc("find a node").
		Param(ws.PathParameter("name", "name").DataType("string")).
		Writes(Node{}).
		Returns(200, "OK", Node{}).
		Returns(404, "Not Found", nil))

	return ws
}
