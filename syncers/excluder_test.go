package syncers

import (
	"testing"

	"github.com/loft-sh/vcluster/pkg/util/translate"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
)

func TestShouldExcludeObjectNil(t *testing.T) {
	if shouldExcludeObject(nil) {
		t.Fatal("expected nil object to not be excluded")
	}
}

func TestShouldExcludeObjectLabels(t *testing.T) {
	obj := &unstructured.Unstructured{}

	obj.SetLabels(map[string]string{
		translate.ControllerLabel: "other-controller",
	})
	if !shouldExcludeObject(obj) {
		t.Fatal("expected object with non-matching controller label to be excluded")
	}

	obj.SetLabels(map[string]string{
		translate.ControllerLabel: controlledByLabelValue,
	})
	if shouldExcludeObject(obj) {
		t.Fatal("expected object with matching controller label to not be excluded")
	}
}

func TestShouldExcludeObjectAnnotations(t *testing.T) {
	obj := &unstructured.Unstructured{}

	obj.SetAnnotations(map[string]string{
		translate.ControllerLabel: "other-controller",
	})
	if !shouldExcludeObject(obj) {
		t.Fatal("expected object with non-matching controller annotation to be excluded")
	}

	obj.SetAnnotations(map[string]string{
		translate.ControllerLabel: controlledByLabelValue,
	})
	if shouldExcludeObject(obj) {
		t.Fatal("expected object with matching controller annotation to not be excluded")
	}
}

func TestShouldExcludeObjectLabelAnnotationConflict(t *testing.T) {
	obj := &unstructured.Unstructured{}
	obj.SetLabels(map[string]string{
		translate.ControllerLabel: controlledByLabelValue,
	})
	obj.SetAnnotations(map[string]string{
		translate.ControllerLabel: "other-controller",
	})

	if !shouldExcludeObject(obj) {
		t.Fatal("expected conflicting controller annotation to exclude object")
	}
}
