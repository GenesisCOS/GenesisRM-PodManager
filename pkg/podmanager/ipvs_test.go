package podmanager

import (
	"testing"

	"github.com/moby/ipvs"
)

func TestIPVSList(t *testing.T) {
	t.Logf("Test IPVS")
	handle, err := ipvs.New("")
	if err != nil {
		t.Fatalf(err.Error())
	}
	svcs, err := handle.GetServices()
	if err != nil {
		t.Fatalf(err.Error())
	}
	for _, svc := range svcs {
		t.Logf("%s:%d", svc.Address.String(), svc.Port)
	}
}
