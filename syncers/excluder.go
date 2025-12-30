// Package syncers provides resource syncers for bidirectional sync between virtual and host clusters.
package syncers

import (
	synctypes "github.com/loft-sh/vcluster/pkg/syncer/types"
	"github.com/loft-sh/vcluster/pkg/util/translate"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

const controlledByLabelValue = "generic-sync"

func shouldExcludeObject(obj client.Object) bool {
	if obj == nil {
		return false
	}

	if labels := obj.GetLabels(); labels != nil {
		if val := labels[translate.ControllerLabel]; val != "" && val != controlledByLabelValue {
			return true
		}
	}

	if annotations := obj.GetAnnotations(); annotations != nil {
		if val := annotations[translate.ControllerLabel]; val != "" && val != controlledByLabelValue {
			return true
		}
	}

	return false
}

var _ synctypes.ObjectExcluder = &ToHostSyncer{}

// ExcludeVirtual returns true if the virtual object should be excluded from syncing.
func (s *ToHostSyncer) ExcludeVirtual(vObj client.Object) bool {
	return shouldExcludeObject(vObj)
}

// ExcludePhysical returns true if the physical (host) object should be excluded from syncing.
func (s *ToHostSyncer) ExcludePhysical(pObj client.Object) bool {
	return shouldExcludeObject(pObj)
}

var _ synctypes.ObjectExcluder = &FromHostSyncer{}

// ExcludeVirtual returns true if the virtual object should be excluded from syncing.
func (s *FromHostSyncer) ExcludeVirtual(vObj client.Object) bool {
	return shouldExcludeObject(vObj)
}

// ExcludePhysical returns true if the physical (host) object should be excluded from syncing.
func (s *FromHostSyncer) ExcludePhysical(pObj client.Object) bool {
	return shouldExcludeObject(pObj)
}
