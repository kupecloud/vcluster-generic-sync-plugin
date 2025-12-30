package syncers

import (
	"fmt"
	"strings"

	"github.com/loft-sh/vcluster/pkg/syncer/synccontext"
	"k8s.io/apimachinery/pkg/api/meta"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/discovery"
	"k8s.io/client-go/rest"
)

// hasStatusSubresource checks if a resource has a status subresource by querying the API discovery.
// This works for both CRDs and core API resources (Pods, Services, etc.).
// Returns (hasStatus, error). On error, returns (false, err).
func hasStatusSubresource(config *rest.Config, gvk schema.GroupVersionKind) (bool, error) {
	discoveryClient, err := discovery.NewDiscoveryClientForConfig(config)
	if err != nil {
		return false, fmt.Errorf("failed to create discovery client: %w", err)
	}

	// Get the API group version resources
	var groupVersion string
	if gvk.Group == "" {
		groupVersion = gvk.Version // core API: "v1"
	} else {
		groupVersion = gvk.Group + "/" + gvk.Version // e.g., "apps/v1"
	}

	resourceList, err := discoveryClient.ServerResourcesForGroupVersion(groupVersion)
	if err != nil {
		return false, fmt.Errorf("failed to get resources for %s: %w", groupVersion, err)
	}

	// Find the plural name for this kind
	var pluralName string
	for _, r := range resourceList.APIResources {
		// Skip subresources when finding the main resource
		if strings.Contains(r.Name, "/") {
			continue
		}
		if r.Kind == gvk.Kind {
			pluralName = r.Name
			break
		}
	}

	if pluralName == "" {
		return false, fmt.Errorf("resource %s not found in %s", gvk.Kind, groupVersion)
	}

	// Look for {plural}/status subresource
	statusName := pluralName + "/status"
	for _, r := range resourceList.APIResources {
		if r.Name == statusName {
			return true, nil
		}
	}

	return false, nil
}

func resolveNamespaced(ctx *synccontext.RegisterContext, gvk schema.GroupVersionKind) (bool, error) {
	var lastErr error

	if ctx.VirtualManager != nil {
		mapping, err := ctx.VirtualManager.GetRESTMapper().RESTMapping(gvk.GroupKind(), gvk.Version)
		if err == nil {
			return mapping.Scope.Name() == meta.RESTScopeNameNamespace, nil
		}
		lastErr = err
	}

	if ctx.HostManager != nil {
		mapping, err := ctx.HostManager.GetRESTMapper().RESTMapping(gvk.GroupKind(), gvk.Version)
		if err == nil {
			return mapping.Scope.Name() == meta.RESTScopeNameNamespace, nil
		}
		lastErr = err
	}

	if lastErr == nil {
		lastErr = fmt.Errorf("unable to resolve REST mapping")
	}

	return true, lastErr
}
