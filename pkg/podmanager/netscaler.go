package podmanager

import "context"

type NetScaler struct {
	podmanager *PodManager
}

func NewNetScaler(podmanager *PodManager) *NetScaler {
	return &NetScaler{
		podmanager: podmanager,
	}
}

func (s *NetScaler) Start(ctx context.Context) {

}
