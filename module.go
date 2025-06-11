package multipartHTTP

import (
	"go.k6.io/k6/js/common"
	"go.k6.io/k6/js/modules"
)

type (
	// RootModule is the global module instance that will create module
	// instances for each VU.
	RootModule struct{}
)

var (
	_ modules.Module   = &RootModule{}
	_ modules.Instance = &MultipartSubscriptionAPI{}
)

// New returns a pointer to a new RootModule instance.
func New() *RootModule {
	return &RootModule{}
}

// NewMultipartSubscription implements the modules.Module interface returning a new instance for each VU.
func (*RootModule) NewModuleInstance(vu modules.VU) modules.Instance {
	metrics := RegisterMetrics(vu.InitEnv().Registry)

	rt := vu.Runtime()
	mi := &MultipartSubscriptionAPI{
		vu:                   vu,
		exports:              rt.NewObject(),
		httpMultipartMetrics: metrics,
		holder:               &ObjectHolder{},
	}

	mustExport := func(name string, value interface{}) {
		if err := mi.exports.Set(name, value); err != nil {
			common.Throw(rt, err)
		}
	}

	mustExport("MultipartHttp", mi.multipartSubscription)
	mustExport("getInternalState", mi.GetInternalState)

	return mi
}

func init() {
	modules.Register("k6/x/multipartHTTP", New())
}
