package syncers

import (
	"testing"

	"k8s.io/apimachinery/pkg/runtime/schema"

	"github.com/kupecloud/vcluster-generic-sync-plugin/config"
)

func TestParseGVK(t *testing.T) {
	tests := []struct {
		name          string
		apiVersion    string
		kind          string
		expectedGroup string
		expectedVer   string
		expectedKind  string
		expectError   bool
	}{
		{
			name:          "core API (v1)",
			apiVersion:    "v1",
			kind:          "ConfigMap",
			expectedGroup: "",
			expectedVer:   "v1",
			expectedKind:  "ConfigMap",
		},
		{
			name:          "apps group",
			apiVersion:    "apps/v1",
			kind:          "Deployment",
			expectedGroup: "apps",
			expectedVer:   "v1",
			expectedKind:  "Deployment",
		},
		{
			name:          "networking.k8s.io group",
			apiVersion:    "networking.k8s.io/v1",
			kind:          "Ingress",
			expectedGroup: "networking.k8s.io",
			expectedVer:   "v1",
			expectedKind:  "Ingress",
		},
		{
			name:          "gateway.networking.k8s.io group",
			apiVersion:    "gateway.networking.k8s.io/v1",
			kind:          "HTTPRoute",
			expectedGroup: "gateway.networking.k8s.io",
			expectedVer:   "v1",
			expectedKind:  "HTTPRoute",
		},
		{
			name:          "beta version",
			apiVersion:    "gateway.networking.k8s.io/v1beta1",
			kind:          "Gateway",
			expectedGroup: "gateway.networking.k8s.io",
			expectedVer:   "v1beta1",
			expectedKind:  "Gateway",
		},
		{
			name:          "custom CRD",
			apiVersion:    "example.com/v1alpha1",
			kind:          "MyResource",
			expectedGroup: "example.com",
			expectedVer:   "v1alpha1",
			expectedKind:  "MyResource",
		},
		{
			name:          "empty apiVersion parses as empty version",
			apiVersion:    "",
			kind:          "Pod",
			expectedGroup: "",
			expectedVer:   "",
			expectedKind:  "Pod",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			gvk, err := parseGVK(tt.apiVersion, tt.kind)
			if tt.expectError {
				if err == nil {
					t.Errorf("expected error but got none")
				}
				return
			}
			if err != nil {
				t.Errorf("unexpected error: %v", err)
				return
			}
			if gvk.Group != tt.expectedGroup {
				t.Errorf("parseGVK().Group = %q, expected %q", gvk.Group, tt.expectedGroup)
			}
			if gvk.Version != tt.expectedVer {
				t.Errorf("parseGVK().Version = %q, expected %q", gvk.Version, tt.expectedVer)
			}
			if gvk.Kind != tt.expectedKind {
				t.Errorf("parseGVK().Kind = %q, expected %q", gvk.Kind, tt.expectedKind)
			}
		})
	}
}

func TestSyncerName(t *testing.T) {
	tests := []struct {
		name      string
		gvk       schema.GroupVersionKind
		direction config.SyncDirection
		expected  string
	}{
		{
			name:      "core resource toHost",
			gvk:       schema.GroupVersionKind{Group: "", Version: "v1", Kind: "ConfigMap"},
			direction: config.ToHost,
			expected:  "generic-toHost-configmap",
		},
		{
			name:      "core resource fromHost",
			gvk:       schema.GroupVersionKind{Group: "", Version: "v1", Kind: "Secret"},
			direction: config.FromHost,
			expected:  "generic-fromHost-secret",
		},
		{
			name:      "apps group toHost",
			gvk:       schema.GroupVersionKind{Group: "apps", Version: "v1", Kind: "Deployment"},
			direction: config.ToHost,
			expected:  "generic-toHost-apps-deployment",
		},
		{
			name:      "gateway API resource",
			gvk:       schema.GroupVersionKind{Group: "gateway.networking.k8s.io", Version: "v1", Kind: "HTTPRoute"},
			direction: config.ToHost,
			expected:  "generic-toHost-gateway.networking.k8s.io-httproute",
		},
		{
			name:      "custom CRD fromHost",
			gvk:       schema.GroupVersionKind{Group: "example.com", Version: "v1alpha1", Kind: "MyResource"},
			direction: config.FromHost,
			expected:  "generic-fromHost-example.com-myresource",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := syncerName(tt.gvk, tt.direction)
			if result != tt.expected {
				t.Errorf("syncerName() = %q, expected %q", result, tt.expected)
			}
		})
	}
}
