// Package patches provides reference translation and patching for synced resources.
package patches

import (
	"encoding/json"
	"fmt"
	"regexp"
	"strconv"
	"strings"

	"github.com/loft-sh/vcluster/pkg/syncer/synccontext"
	"github.com/loft-sh/vcluster/pkg/util/translate"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/klog/v2"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/kupecloud/vcluster-generic-sync-plugin/config"
)

// PathSegment represents a single segment in a parsed path
type PathSegment struct {
	Field      string // The field name (e.g., "spec", "rules", "name")
	IsArray    bool   // Whether this segment is an array access
	ArrayIndex int    // -1 for wildcard [*], >= 0 for specific index [0], [1], etc.
}

// arrayIndexRegex matches array notation like [*], [0], [1], etc.
var arrayIndexRegex = regexp.MustCompile(`^(.+)\[(\*|\d+)\]$`)

const originalRefsAnnotation = "kupe.cloud/original-refs"

// Patcher applies patches to translate object references
type Patcher struct {
	patches                    []compiledPatch
	vclusterName               string
	hostNs                     string
	includeSelectorOwnerLabels bool
}

type refMapping struct {
	Name      string `json:"name,omitempty"`
	Namespace string `json:"namespace,omitempty"`
}

type refMap map[string]refMapping

type compiledPatch struct {
	patch    config.Patch
	segments []PathSegment
}

// NewPatcher creates a new patcher with the given configuration
func NewPatcher(patches []config.Patch, vclusterName, hostNamespace string, includeSelectorOwnerLabels bool) *Patcher {
	compiled := make([]compiledPatch, 0, len(patches))
	for _, patch := range patches {
		compiled = append(compiled, compiledPatch{
			patch:    patch,
			segments: parsePath(patch.Path),
		})
	}
	return &Patcher{
		patches:                    compiled,
		vclusterName:               vclusterName,
		hostNs:                     hostNamespace,
		includeSelectorOwnerLabels: includeSelectorOwnerLabels,
	}
}

func getRefMap(obj *unstructured.Unstructured) refMap {
	if obj == nil {
		return nil
	}
	annotations := obj.GetAnnotations()
	if annotations == nil {
		return nil
	}
	raw := annotations[originalRefsAnnotation]
	if raw == "" {
		return nil
	}
	var refs refMap
	if err := json.Unmarshal([]byte(raw), &refs); err != nil {
		klog.V(4).InfoS("Failed to unmarshal original-refs annotation", "err", err)
		return nil
	}
	return refs
}

func setRefMap(obj *unstructured.Unstructured, refs refMap) {
	if obj == nil || refs == nil {
		return
	}
	data, err := json.Marshal(refs)
	if err != nil {
		klog.V(4).InfoS("Failed to marshal original-refs annotation", "err", err)
		return
	}
	annotations := obj.GetAnnotations()
	if annotations == nil {
		annotations = map[string]string{}
	}
	annotations[originalRefsAnnotation] = string(data)
	obj.SetAnnotations(annotations)
}

// clearRefMap removes the original-refs annotation so a subsequent pass rebuilds it from
// scratch. Without this the refMap only ever grows: recordOriginalRef merges into the
// existing map, so refs that no longer appear in the object (e.g. churned blue/green
// backendRefs) linger forever, bloating every list/watch payload and eventually pressing
// against the 256KiB metadata limit.
func clearRefMap(obj *unstructured.Unstructured) {
	if obj == nil {
		return
	}
	annotations := obj.GetAnnotations()
	if _, ok := annotations[originalRefsAnnotation]; !ok {
		return
	}
	delete(annotations, originalRefsAnnotation)
	obj.SetAnnotations(annotations)
}

func (p *Patcher) recordOriginalRef(obj *unstructured.Unstructured, hostName, originalName, originalNamespace string) {
	if obj == nil || hostName == "" || originalName == "" {
		return
	}
	refs := getRefMap(obj)
	if refs == nil {
		refs = refMap{}
	}
	refs[hostName] = refMapping{Name: originalName, Namespace: originalNamespace}
	setRefMap(obj, refs)
}

func (p *Patcher) lookupOriginalRef(obj *unstructured.Unstructured, hostName string) (refMapping, bool) {
	if obj == nil || hostName == "" {
		return refMapping{}, false
	}
	refs := getRefMap(obj)
	if refs == nil {
		return refMapping{}, false
	}
	ref, ok := refs[hostName]
	return ref, ok
}

// ApplyToHost applies patches to translate virtual references to host references
func (p *Patcher) ApplyToHost(ctx *synccontext.SyncContext, vObj, pObj client.Object) error {
	vUnstructured, ok := vObj.(*unstructured.Unstructured)
	if !ok {
		return nil
	}
	pUnstructured, ok := pObj.(*unstructured.Unstructured)
	if !ok {
		return nil
	}

	// Rebuild the original-refs annotation from scratch on every invocation: clear it first,
	// then let recordOriginalRef re-populate it with only the refs translated during this
	// pass. This prunes stale mappings for refs that no longer exist.
	clearRefMap(pUnstructured)

	for _, patch := range p.patches {
		if err := p.applyPatchToHost(ctx, vUnstructured, pUnstructured, patch); err != nil {
			return fmt.Errorf("failed to apply patch at %s: %w", patch.patch.Path, err)
		}
	}
	return nil
}

// ApplyToVirtual applies patches to translate host references to virtual references
func (p *Patcher) ApplyToVirtual(ctx *synccontext.SyncContext, pObj, vObj client.Object) error {
	pUnstructured, ok := pObj.(*unstructured.Unstructured)
	if !ok {
		return nil
	}
	vUnstructured, ok := vObj.(*unstructured.Unstructured)
	if !ok {
		return nil
	}

	for _, patch := range p.patches {
		if err := p.applyPatchToVirtual(ctx, pUnstructured, vUnstructured, patch); err != nil {
			return fmt.Errorf("failed to apply patch at %s: %w", patch.patch.Path, err)
		}
	}
	return nil
}

// applyPatchToHost applies a single patch for virtual to host translation
func (p *Patcher) applyPatchToHost(ctx *synccontext.SyncContext, vObj, pObj *unstructured.Unstructured, patch compiledPatch) error {
	var scalarFn scalarPatchFunc

	switch patch.patch.Type {
	case config.PatchRewriteName:
		scalarFn = p.rewriteNameScalarToHost
	case config.PatchRewriteNamespace:
		scalarFn = p.rewriteNamespaceScalarToHost
	case config.PatchNone:
		scalarFn = p.copyScalar
	}

	return p.traverseAndApply(ctx, vObj.Object, pObj.Object, patch.segments, func(ctx *synccontext.SyncContext, src, dst map[string]interface{}, field string, srcObj, dstObj *unstructured.Unstructured) error {
		switch patch.patch.Type {
		case config.PatchRewriteName:
			return p.rewriteNameToHostAtPath(ctx, src, dst, field, srcObj, dstObj)
		case config.PatchRewriteNamespace:
			return p.rewriteNamespaceToHostAtPath(ctx, src, dst, field)
		case config.PatchRewriteRef:
			return p.rewriteRefToHostAtPath(ctx, src, dst, field, srcObj, dstObj)
		case config.PatchRewriteHostRef:
			return p.rewriteHostRefToHostAtPath(ctx, src, dst, field)
		case config.PatchRewriteLabelSelector:
			return p.rewriteLabelSelectorToHostAtPath(ctx, src, dst, field, srcObj)
		case config.PatchNone:
			return p.copyValueAtPath(src, dst, field)
		default:
			return fmt.Errorf("unknown patch type: %s", patch.patch.Type)
		}
	}, scalarFn, vObj, pObj)
}

// applyPatchToVirtual applies a single patch for host to virtual translation
func (p *Patcher) applyPatchToVirtual(ctx *synccontext.SyncContext, pObj, vObj *unstructured.Unstructured, patch compiledPatch) error {
	var scalarFn scalarPatchFunc

	switch patch.patch.Type {
	case config.PatchRewriteName:
		scalarFn = p.rewriteNameScalarToVirtual
	case config.PatchRewriteNamespace:
		scalarFn = p.rewriteNamespaceScalarToVirtual
	case config.PatchNone:
		scalarFn = p.copyScalar
	}

	return p.traverseAndApply(ctx, pObj.Object, vObj.Object, patch.segments, func(ctx *synccontext.SyncContext, src, dst map[string]interface{}, field string, srcObj, dstObj *unstructured.Unstructured) error {
		switch patch.patch.Type {
		case config.PatchRewriteName:
			return p.rewriteNameToVirtualAtPath(src, dst, field, srcObj)
		case config.PatchRewriteNamespace:
			return p.rewriteNamespaceToVirtualAtPath(src, dst, field, dstObj)
		case config.PatchRewriteRef:
			return p.rewriteRefToVirtualAtPath(src, dst, field, srcObj)
		case config.PatchRewriteHostRef:
			return p.rewriteHostRefToVirtualAtPath(src, dst, field, dstObj)
		case config.PatchRewriteLabelSelector:
			return p.rewriteLabelSelectorToVirtualAtPath(src, dst, field)
		case config.PatchNone:
			return p.copyValueAtPath(src, dst, field)
		default:
			return fmt.Errorf("unknown patch type: %s", patch.patch.Type)
		}
	}, scalarFn, pObj, vObj)
}

// patchFunc is the function signature for applying a patch at a specific path
type patchFunc func(ctx *synccontext.SyncContext, src, dst map[string]interface{}, field string, srcObj, dstObj *unstructured.Unstructured) error

// scalarPatchFunc applies a patch to scalar array elements (e.g., []string)
type scalarPatchFunc func(ctx *synccontext.SyncContext, srcVal interface{}, srcObj, dstObj *unstructured.Unstructured) (interface{}, error)

// traverseAndApply traverses the object following the path segments and applies the patch function
// at each matching location (handling array wildcards by iterating all elements)
func (p *Patcher) traverseAndApply(ctx *synccontext.SyncContext, src, dst map[string]interface{}, segments []PathSegment, fn patchFunc, scalarFn scalarPatchFunc, srcObj, dstObj *unstructured.Unstructured) error {
	if len(segments) == 0 {
		return nil
	}

	// If this is the last segment, apply the patch function
	if len(segments) == 1 {
		seg := segments[0]
		if seg.IsArray {
			// The final field is an array - apply to each element
			return p.applyToArrayElements(src, dst, seg, func(srcElem, dstElem map[string]interface{}) error {
				// For array elements that are the target, we apply to the whole element
				// This is used for rewriteRef where the element itself is the reference
				return fn(ctx, srcElem, dstElem, "", srcObj, dstObj)
			}, scalarFn, ctx, srcObj, dstObj)
		}
		return fn(ctx, src, dst, seg.Field, srcObj, dstObj)
	}

	// Navigate deeper
	seg := segments[0]
	remaining := segments[1:]

	if seg.IsArray {
		// Handle array traversal
		srcArr, srcOk := src[seg.Field].([]interface{})
		dstArr, dstOk := dst[seg.Field].([]interface{})
		if !srcOk || !dstOk {
			// Array doesn't exist or isn't an array - gracefully skip
			return nil
		}

		if seg.ArrayIndex == -1 {
			// Wildcard [*] - iterate all elements
			for i := range srcArr {
				if i >= len(dstArr) {
					break
				}
				srcElem, srcElemOk := srcArr[i].(map[string]interface{})
				dstElem, dstElemOk := dstArr[i].(map[string]interface{})
				if !srcElemOk || !dstElemOk {
					continue
				}
				if err := p.traverseAndApply(ctx, srcElem, dstElem, remaining, fn, scalarFn, srcObj, dstObj); err != nil {
					return err
				}
			}
		} else {
			// Specific index [n]
			idx := seg.ArrayIndex
			if idx >= len(srcArr) || idx >= len(dstArr) {
				return nil
			}
			srcElem, srcElemOk := srcArr[idx].(map[string]interface{})
			dstElem, dstElemOk := dstArr[idx].(map[string]interface{})
			if !srcElemOk || !dstElemOk {
				return nil
			}
			return p.traverseAndApply(ctx, srcElem, dstElem, remaining, fn, scalarFn, srcObj, dstObj)
		}
		return nil
	}

	// Regular field navigation
	srcNext, srcOk := src[seg.Field].(map[string]interface{})
	dstNext, dstOk := dst[seg.Field].(map[string]interface{})
	if !srcOk || !dstOk {
		// Field doesn't exist or isn't an object - gracefully skip
		return nil
	}

	return p.traverseAndApply(ctx, srcNext, dstNext, remaining, fn, scalarFn, srcObj, dstObj)
}

// applyToArrayElements applies a function to each element in an array field
func (p *Patcher) applyToArrayElements(src, dst map[string]interface{}, seg PathSegment, fn func(srcElem, dstElem map[string]interface{}) error, scalarFn scalarPatchFunc, ctx *synccontext.SyncContext, srcObj, dstObj *unstructured.Unstructured) error {
	srcArr, srcOk := src[seg.Field].([]interface{})
	dstArr, dstOk := dst[seg.Field].([]interface{})
	if !srcOk || !dstOk {
		return nil
	}

	if seg.ArrayIndex == -1 {
		// Wildcard - iterate all
		for i := range srcArr {
			if i >= len(dstArr) {
				break
			}
			if srcElem, srcElemOk := srcArr[i].(map[string]interface{}); srcElemOk {
				dstElem, dstElemOk := dstArr[i].(map[string]interface{})
				if !dstElemOk {
					continue
				}
				if err := fn(srcElem, dstElem); err != nil {
					return err
				}
			} else if scalarFn != nil {
				newVal, err := scalarFn(ctx, srcArr[i], srcObj, dstObj)
				if err != nil {
					return err
				}
				dstArr[i] = newVal
			}
		}
	} else {
		// Specific index
		idx := seg.ArrayIndex
		if idx >= len(srcArr) || idx >= len(dstArr) {
			return nil
		}
		if srcElem, srcElemOk := srcArr[idx].(map[string]interface{}); srcElemOk {
			dstElem, dstElemOk := dstArr[idx].(map[string]interface{})
			if !dstElemOk {
				return nil
			}
			return fn(srcElem, dstElem)
		} else if scalarFn != nil {
			newVal, err := scalarFn(ctx, srcArr[idx], srcObj, dstObj)
			if err != nil {
				return err
			}
			dstArr[idx] = newVal
			return nil
		}
	}
	dst[seg.Field] = dstArr
	return nil
}

// rewriteNameToHostAtPath rewrites a name field from virtual to host format at the given path
func (p *Patcher) rewriteNameToHostAtPath(ctx *synccontext.SyncContext, src, dst map[string]interface{}, field string, srcObj, dstObj *unstructured.Unstructured) error {
	value, ok := src[field].(string)
	if !ok || value == "" {
		return nil
	}

	// Get namespace from sibling field or object metadata
	namespace := ""
	if ns, ok := src["namespace"].(string); ok {
		namespace = ns
	}
	if namespace == "" {
		namespace = srcObj.GetNamespace()
	}

	hostName := translate.Default.HostName(ctx, value, namespace)
	dst[field] = hostName.Name
	p.recordOriginalRef(dstObj, hostName.Name, value, namespace)
	return nil
}

// rewriteNameToVirtualAtPath extracts the original virtual name from a host name
func (p *Patcher) rewriteNameToVirtualAtPath(src, dst map[string]interface{}, field string, srcObj *unstructured.Unstructured) error {
	value, ok := src[field].(string)
	if !ok || value == "" {
		return nil
	}

	if ref, ok := p.lookupOriginalRef(srcObj, value); ok && ref.Name != "" {
		dst[field] = ref.Name
		return nil
	}

	originalName := p.reverseTranslateName(value)
	dst[field] = originalName
	return nil
}

func (p *Patcher) rewriteNameScalarToHost(ctx *synccontext.SyncContext, srcVal interface{}, srcObj, dstObj *unstructured.Unstructured) (interface{}, error) {
	value, ok := srcVal.(string)
	if !ok || value == "" {
		return srcVal, nil
	}
	namespace := ""
	if srcObj != nil {
		namespace = srcObj.GetNamespace()
	}
	hostName := translate.Default.HostName(ctx, value, namespace)
	p.recordOriginalRef(dstObj, hostName.Name, value, namespace)
	return hostName.Name, nil
}

func (p *Patcher) rewriteNameScalarToVirtual(_ *synccontext.SyncContext, srcVal interface{}, srcObj, dstObj *unstructured.Unstructured) (interface{}, error) {
	value, ok := srcVal.(string)
	if !ok || value == "" {
		return srcVal, nil
	}
	if ref, ok := p.lookupOriginalRef(srcObj, value); ok && ref.Name != "" {
		return ref.Name, nil
	}
	return p.reverseTranslateName(value), nil
}

// reverseTranslateName extracts the original virtual name from a host-translated name.
// The host name format is: {name}-x-{namespace}-x-{vcluster}
//
// IMPORTANT LIMITATION: This parsing is ambiguous when BOTH name AND namespace contain "-x-".
// For example, "foo-x-bar-x-ns-x-test-x-vcluster" could be parsed multiple ways.
// We use a "last separator" heuristic which works correctly when:
// - Only the name contains "-x-", OR
// - Only the namespace contains "-x-", OR
// - Neither contains "-x-"
//
// The edge case where BOTH contain "-x-" is documented as unsupported.
// If you need to support such names, use the original reference annotation tracking
// which stores the original values explicitly. See docs/configuration/patches.md for details.
func (p *Patcher) reverseTranslateName(hostName string) string {
	suffix := "-x-" + p.vclusterName
	if !strings.HasSuffix(hostName, suffix) {
		// Not a translated name, return as-is
		return hostName
	}

	// Remove the vcluster suffix: {name}-x-{namespace}
	withoutVcluster := strings.TrimSuffix(hostName, suffix)

	// Strategy 1: If we have the original namespace from a sibling field annotation or context
	// For ToVirtual, the sibling namespace field would still be the HOST namespace,
	// but we need to find the ORIGINAL namespace which is encoded in the name.
	// So we can't use the sibling field directly - it doesn't help.

	// Strategy 2: Best-effort parsing
	// Find the last "-x-" to split name and namespace
	// This works correctly unless BOTH name AND namespace contain "-x-"
	// which is an edge case we document as unsupported
	if idx := strings.LastIndex(withoutVcluster, "-x-"); idx > 0 {
		originalName := withoutVcluster[:idx]
		if originalName != "" {
			return originalName
		}
	}

	// If we can't parse it, return as-is
	return hostName
}

// rewriteNamespaceToHostAtPath rewrites a namespace field to host namespace
func (p *Patcher) rewriteNamespaceToHostAtPath(ctx *synccontext.SyncContext, src, dst map[string]interface{}, field string) error {
	value, ok := src[field].(string)
	if !ok {
		return nil
	}
	dst[field] = translate.Default.HostNamespace(ctx, value)
	return nil
}

// rewriteNamespaceToVirtualAtPath rewrites a host namespace back to virtual namespace
func (p *Patcher) rewriteNamespaceToVirtualAtPath(src, dst map[string]interface{}, field string, vObj *unstructured.Unstructured) error {
	_, ok := src[field].(string)
	if !ok {
		return nil
	}
	virtualNs := vObj.GetNamespace()
	if virtualNs == "" {
		virtualNs = "default"
	}
	dst[field] = virtualNs
	return nil
}

func (p *Patcher) rewriteNamespaceScalarToHost(ctx *synccontext.SyncContext, srcVal interface{}, _, _ *unstructured.Unstructured) (interface{}, error) {
	value, ok := srcVal.(string)
	if !ok {
		return srcVal, nil
	}
	return translate.Default.HostNamespace(ctx, value), nil
}

func (p *Patcher) rewriteNamespaceScalarToVirtual(_ *synccontext.SyncContext, srcVal interface{}, _, dstObj *unstructured.Unstructured) (interface{}, error) {
	if _, ok := srcVal.(string); !ok {
		return srcVal, nil
	}
	virtualNs := ""
	if dstObj != nil {
		virtualNs = dstObj.GetNamespace()
	}
	if virtualNs == "" {
		virtualNs = "default"
	}
	return virtualNs, nil
}

func (p *Patcher) copyScalar(_ *synccontext.SyncContext, srcVal interface{}, _, _ *unstructured.Unstructured) (interface{}, error) {
	return srcVal, nil
}

// rewriteRefToHostAtPath rewrites a reference object to host format
// When field is empty, src/dst are the reference objects themselves
func (p *Patcher) rewriteRefToHostAtPath(ctx *synccontext.SyncContext, src, dst map[string]interface{}, field string, srcObj, dstObj *unstructured.Unstructured) error {
	var ref map[string]interface{}
	var dstRef map[string]interface{}

	if field == "" {
		// src/dst ARE the reference objects (array element case)
		ref = src
		dstRef = dst
	} else {
		var ok bool
		ref, ok = src[field].(map[string]interface{})
		if !ok {
			return nil
		}
		dstRef, ok = dst[field].(map[string]interface{})
		if !ok {
			// Create the destination ref if it doesn't exist
			dstRef = make(map[string]interface{})
			for k, v := range ref {
				dstRef[k] = v
			}
			dst[field] = dstRef
		}
	}

	name, _ := ref["name"].(string)
	if name == "" {
		return nil
	}

	namespace, _ := ref["namespace"].(string)
	if namespace == "" {
		namespace = srcObj.GetNamespace()
	}

	// Translate name and namespace
	hostName := translate.Default.HostName(ctx, name, namespace)
	dstRef["name"] = hostName.Name
	dstRef["namespace"] = hostName.Namespace
	p.recordOriginalRef(dstObj, hostName.Name, name, namespace)
	return nil
}

// rewriteRefToVirtualAtPath reverses a reference object back to virtual format
func (p *Patcher) rewriteRefToVirtualAtPath(src, dst map[string]interface{}, field string, srcObj *unstructured.Unstructured) error {
	var ref map[string]interface{}
	var dstRef map[string]interface{}

	if field == "" {
		ref = src
		dstRef = dst
	} else {
		var ok bool
		ref, ok = src[field].(map[string]interface{})
		if !ok {
			return nil
		}
		dstRef, ok = dst[field].(map[string]interface{})
		if !ok {
			dstRef = make(map[string]interface{})
			for k, v := range ref {
				dstRef[k] = v
			}
			dst[field] = dstRef
		}
	}

	hostName, _ := ref["name"].(string)
	if hostName == "" {
		return nil
	}

	if refMapping, ok := p.lookupOriginalRef(srcObj, hostName); ok && refMapping.Name != "" {
		dstRef["name"] = refMapping.Name
		if refMapping.Namespace != "" {
			dstRef["namespace"] = refMapping.Namespace
		}
		return nil
	}

	// Extract original name and namespace from translated name
	originalName, originalNamespace := p.reverseTranslateNameAndNamespace(hostName)
	if originalName != "" && originalNamespace != "" {
		dstRef["name"] = originalName
		dstRef["namespace"] = originalNamespace
	}
	// If we can't parse it, leave as-is (don't modify)

	return nil
}

// reverseTranslateNameAndNamespace extracts both the original name and namespace
// from a host-translated name. Format: {name}-x-{namespace}-x-{vcluster}
func (p *Patcher) reverseTranslateNameAndNamespace(hostName string) (name, namespace string) {
	suffix := "-x-" + p.vclusterName
	if !strings.HasSuffix(hostName, suffix) {
		// Not a translated name
		return "", ""
	}

	// Remove the vcluster suffix: {name}-x-{namespace}
	withoutVcluster := strings.TrimSuffix(hostName, suffix)

	// Find the last "-x-" to split name and namespace
	// This works correctly unless BOTH name AND namespace contain "-x-"
	if idx := strings.LastIndex(withoutVcluster, "-x-"); idx > 0 {
		name = withoutVcluster[:idx]
		namespace = withoutVcluster[idx+3:]
		if name != "" && namespace != "" {
			return name, namespace
		}
	}

	return "", ""
}

// rewriteHostRefToHostAtPath rewrites a host-native reference (only namespace changes)
func (p *Patcher) rewriteHostRefToHostAtPath(ctx *synccontext.SyncContext, src, dst map[string]interface{}, field string) error {
	var ref map[string]interface{}
	var dstRef map[string]interface{}

	if field == "" {
		ref = src
		dstRef = dst
	} else {
		var ok bool
		ref, ok = src[field].(map[string]interface{})
		if !ok {
			return nil
		}
		dstRef, ok = dst[field].(map[string]interface{})
		if !ok {
			dstRef = make(map[string]interface{})
			for k, v := range ref {
				dstRef[k] = v
			}
			dst[field] = dstRef
		}
	}

	// For host refs, only translate namespace if present
	if namespace, hasNs := ref["namespace"].(string); hasNs {
		dstRef["namespace"] = translate.Default.HostNamespace(ctx, namespace)
	}
	return nil
}

// rewriteHostRefToVirtualAtPath reverses a host-native reference
func (p *Patcher) rewriteHostRefToVirtualAtPath(src, dst map[string]interface{}, field string, vObj *unstructured.Unstructured) error {
	var dstRef map[string]interface{}

	if field == "" {
		dstRef = dst
	} else {
		ref, ok := src[field].(map[string]interface{})
		if !ok {
			return nil
		}
		dstRef, ok = dst[field].(map[string]interface{})
		if !ok {
			dstRef = make(map[string]interface{})
			for k, v := range ref {
				dstRef[k] = v
			}
			dst[field] = dstRef
		}
	}

	// Set namespace to virtual namespace
	virtualNs := vObj.GetNamespace()
	if virtualNs != "" {
		dstRef["namespace"] = virtualNs
	}
	return nil
}

// rewriteLabelSelectorToHostAtPath translates labels in a selector to host format
func (p *Patcher) rewriteLabelSelectorToHostAtPath(_ *synccontext.SyncContext, src, dst map[string]interface{}, field string, srcObj *unstructured.Unstructured) error {
	var selector map[string]interface{}
	var dstSelector map[string]interface{}

	if field == "" {
		selector = src
		dstSelector = dst
	} else {
		var ok bool
		selector, ok = src[field].(map[string]interface{})
		if !ok {
			return nil
		}
		dstSelector, ok = dst[field].(map[string]interface{})
		if !ok {
			dstSelector = make(map[string]interface{})
			for k, v := range selector {
				dstSelector[k] = v
			}
			dst[field] = dstSelector
		}
	}

	// Translate matchLabels if present (keys only)
	if matchLabels, ok := selector["matchLabels"].(map[string]interface{}); ok {
		translatedLabels := make(map[string]interface{}, len(matchLabels))
		for k, v := range matchLabels {
			s, ok := v.(string)
			if !ok {
				continue
			}
			translatedLabels[translate.HostLabel(k)] = s
		}
		dstSelector["matchLabels"] = translatedLabels
	}

	if p.includeSelectorOwnerLabels {
		matchLabels, ok := dstSelector["matchLabels"].(map[string]interface{})
		if !ok || matchLabels == nil {
			matchLabels = map[string]interface{}{}
		}
		virtualNamespace := ""
		if srcObj != nil {
			virtualNamespace = srcObj.GetNamespace()
		}
		if virtualNamespace == "" {
			matchLabels[translate.MarkerLabel] = translate.Default.MarkerLabelCluster()
		} else {
			matchLabels[translate.MarkerLabel] = p.vclusterName
			matchLabels[translate.NamespaceLabel] = virtualNamespace
		}
		dstSelector["matchLabels"] = matchLabels
	}

	if matchExpressions, ok := selector["matchExpressions"].([]interface{}); ok {
		translatedExpressions := make([]interface{}, 0, len(matchExpressions))
		for _, expr := range matchExpressions {
			exprMap, ok := expr.(map[string]interface{})
			if !ok {
				continue
			}
			newExpr := make(map[string]interface{}, len(exprMap))
			for k, v := range exprMap {
				newExpr[k] = v
			}
			if key, ok := exprMap["key"].(string); ok && key != "" {
				newExpr["key"] = translate.HostLabel(key)
			}
			translatedExpressions = append(translatedExpressions, newExpr)
		}
		dstSelector["matchExpressions"] = translatedExpressions
	}
	return nil
}

// rewriteLabelSelectorToVirtualAtPath translates host labels back to virtual format
func (p *Patcher) rewriteLabelSelectorToVirtualAtPath(src, dst map[string]interface{}, field string) error {
	var selector map[string]interface{}
	var dstSelector map[string]interface{}

	if field == "" {
		selector = src
		dstSelector = dst
	} else {
		var ok bool
		selector, ok = src[field].(map[string]interface{})
		if !ok {
			return nil
		}
		dstSelector, ok = dst[field].(map[string]interface{})
		if !ok {
			dstSelector = make(map[string]interface{})
			for k, v := range selector {
				dstSelector[k] = v
			}
			dst[field] = dstSelector
		}
	}

	if matchLabels, ok := selector["matchLabels"].(map[string]interface{}); ok {
		translated := make(map[string]interface{}, len(matchLabels))
		for k, v := range matchLabels {
			value, ok := v.(string)
			if !ok {
				continue
			}
			vKey, ok := translate.VirtualLabel(k)
			if !ok {
				continue
			}
			translated[vKey] = value
		}
		dstSelector["matchLabels"] = translated
	}

	if matchExpressions, ok := selector["matchExpressions"].([]interface{}); ok {
		translatedExpressions := make([]interface{}, 0, len(matchExpressions))
		for _, expr := range matchExpressions {
			exprMap, ok := expr.(map[string]interface{})
			if !ok {
				continue
			}
			key, ok := exprMap["key"].(string)
			if !ok || key == "" {
				continue
			}
			vKey, ok := translate.VirtualLabel(key)
			if !ok {
				continue
			}
			newExpr := make(map[string]interface{}, len(exprMap))
			for k, v := range exprMap {
				newExpr[k] = v
			}
			newExpr["key"] = vKey
			translatedExpressions = append(translatedExpressions, newExpr)
		}
		dstSelector["matchExpressions"] = translatedExpressions
	}

	return nil
}

// copyValueAtPath copies a value from source to target without translation
// Performs a deep copy to avoid shared references between objects
func (p *Patcher) copyValueAtPath(src, dst map[string]interface{}, field string) error {
	if field == "" {
		// Copy all fields from src to dst with deep copy
		for k, v := range src {
			dst[k] = deepCopyValue(v)
		}
		return nil
	}

	if value, ok := src[field]; ok {
		dst[field] = deepCopyValue(value)
	}
	return nil
}

// deepCopyValue performs a deep copy of interface{} values
// Handles maps, slices, and primitive types
func deepCopyValue(v interface{}) interface{} {
	if v == nil {
		return nil
	}

	switch val := v.(type) {
	case map[string]interface{}:
		result := make(map[string]interface{}, len(val))
		for k, v := range val {
			result[k] = deepCopyValue(v)
		}
		return result
	case []interface{}:
		result := make([]interface{}, len(val))
		for i, v := range val {
			result[i] = deepCopyValue(v)
		}
		return result
	default:
		// Primitive types (string, int, float, bool) are immutable, return as-is
		return val
	}
}

// parsePath converts a dot-notation path with optional array notation to PathSegments
// Examples:
//   - "spec.name" -> [{Field: "spec"}, {Field: "name"}]
//   - "spec.rules[*].name" -> [{Field: "spec"}, {Field: "rules", IsArray: true, ArrayIndex: -1}, {Field: "name"}]
//   - "spec.rules[0].name" -> [{Field: "spec"}, {Field: "rules", IsArray: true, ArrayIndex: 0}, {Field: "name"}]
func parsePath(path string) []PathSegment {
	parts := strings.Split(path, ".")
	segments := make([]PathSegment, 0, len(parts))

	for _, part := range parts {
		if part == "" {
			continue
		}

		if matches := arrayIndexRegex.FindStringSubmatch(part); matches != nil {
			field := matches[1]
			indexStr := matches[2]

			seg := PathSegment{
				Field:   field,
				IsArray: true,
			}

			if indexStr == "*" {
				seg.ArrayIndex = -1 // Wildcard
			} else {
				idx, _ := strconv.Atoi(indexStr)
				seg.ArrayIndex = idx
			}

			segments = append(segments, seg)
		} else {
			segments = append(segments, PathSegment{
				Field:   part,
				IsArray: false,
			})
		}
	}

	return segments
}
