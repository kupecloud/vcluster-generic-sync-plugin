package metrics

import (
	"context"
	"errors"
	"fmt"
	"net"
	"slices"
	"strings"
	"testing"

	"github.com/prometheus/client_golang/prometheus"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/runtime/schema"
)

// gatherCounters registers the given vecs in a fresh registry and returns
// metric name -> "label=value," key -> counter value.
func gatherCounters(t *testing.T, vecs ...prometheus.Collector) map[string]map[string]float64 {
	t.Helper()
	reg := prometheus.NewRegistry()
	reg.MustRegister(vecs...)
	families, err := reg.Gather()
	if err != nil {
		t.Fatalf("gather: %v", err)
	}
	series := map[string]map[string]float64{}
	for _, fam := range families {
		for _, m := range fam.GetMetric() {
			if m.GetCounter() == nil {
				continue
			}
			pairs := make([]string, 0, 2*len(m.GetLabel()))
			for _, lp := range m.GetLabel() {
				pairs = append(pairs, lp.GetName(), lp.GetValue())
			}
			key := labelKey(pairs...)
			if series[fam.GetName()] == nil {
				series[fam.GetName()] = map[string]float64{}
			}
			series[fam.GetName()][key] = m.GetCounter().GetValue()
		}
	}
	return series
}

// labelKey builds the "name=value," key for a series from name/value pairs,
// sorted by label name the way Gather emits them (error_type sorts before kind).
func labelKey(pairs ...string) string {
	type kv struct{ k, v string }
	kvs := make([]kv, 0, len(pairs)/2)
	for i := 0; i+1 < len(pairs); i += 2 {
		kvs = append(kvs, kv{pairs[i], pairs[i+1]})
	}
	slices.SortFunc(kvs, func(a, b kv) int { return strings.Compare(a.k, b.k) })
	key := ""
	for _, p := range kvs {
		key += p.k + "=" + p.v + ","
	}
	return key
}

func assertSeriesAtZero(t *testing.T, series map[string]map[string]float64, want map[string][]string) {
	t.Helper()
	for name, labelSets := range want {
		got, ok := series[name]
		if !ok {
			t.Errorf("%s: no counter series exported at all", name)
			continue
		}
		for _, ls := range labelSets {
			v, ok := got[ls]
			if !ok {
				t.Errorf("%s{%s}: series not pre-created", name, ls)
				continue
			}
			if v != 0 {
				t.Errorf("%s{%s}: pre-created at %v, want 0", name, ls, v)
			}
		}
	}
}

// TestRegisterSyncerPreCreatesSeries pins the contract the alert rules on
// generic_sync_errors_total / generic_sync_operations_total rely on: every
// per-syncer counter series with known label values is exported at 0 from the
// moment the syncer is registered, so its first increment is visible to
// rate()/increase().
func TestRegisterSyncerPreCreatesSeries(t *testing.T) {
	const direction, kind = DirectionFromHost, "SeriesTestKind" // untouched by any other test

	RegisterSyncer(direction, kind, "v1", "mirror", false)

	series := gatherCounters(t, SyncOperationsTotal, SyncErrorsTotal, ReconcileTotal, NamespaceFilteredTotal, SelectorFilteredTotal, SyncConflictsTotal)

	dk := labelKey(LabelDirection, direction, LabelKind, kind)
	want := map[string][]string{
		"generic_sync_operations_total":         {},
		"generic_sync_errors_total":             {},
		"generic_sync_reconcile_total":          {dk},
		"generic_sync_namespace_filtered_total": {dk},
		"generic_sync_selector_filtered_total":  {dk},
		"generic_sync_sync_conflicts_total":     {dk},
	}
	for _, op := range []string{OperationCreate, OperationUpdate, OperationDelete} {
		for _, st := range []string{StatusSuccess, StatusError, StatusSkipped} {
			want["generic_sync_operations_total"] = append(want["generic_sync_operations_total"],
				labelKey(LabelDirection, direction, LabelKind, kind, LabelOperation, op, LabelStatus, st))
		}
	}
	for _, et := range []string{ErrorTypeConflict, ErrorTypeNotFound, ErrorTypeValidation, ErrorTypeForbidden, ErrorTypeTransient, ErrorTypeUnknown} {
		want["generic_sync_errors_total"] = append(want["generic_sync_errors_total"],
			labelKey(LabelDirection, direction, LabelKind, kind, LabelErrorType, et))
	}
	if n := len(want["generic_sync_operations_total"]); n != 9 {
		t.Fatalf("test expects 9 operations series, built %d", n)
	}
	if n := len(want["generic_sync_errors_total"]); n != 6 {
		t.Fatalf("test expects 6 errors series, built %d", n)
	}

	assertSeriesAtZero(t, series, want)

	// Exactly the expected series for this syncer, none extra.
	mine := LabelDirection + "=" + direction + "," // direction sorts first in every vec here
	for name, labelSets := range want {
		var got int
		for key := range series[name] {
			if strings.HasPrefix(key, mine) && strings.Contains(key, LabelKind+"="+kind+",") {
				got++
			}
		}
		if got != len(labelSets) {
			t.Errorf("%s: %d series for %s, want %d", name, got, dk, len(labelSets))
		}
	}
}

// TestInitPluginSeries pins that config_reloads_total{status} is exported at
// 0 for both statuses from process start (init calls initPluginSeries).
func TestInitPluginSeries(t *testing.T) {
	ConfigReloadsTotal.Reset() // other tests in this binary increment it
	initPluginSeries()

	series := gatherCounters(t, ConfigReloadsTotal)
	assertSeriesAtZero(t, series, map[string][]string{
		"generic_sync_config_reloads_total": {"status=success,", "status=error,"},
	})
	if n := len(series["generic_sync_config_reloads_total"]); n != 2 {
		t.Errorf("config_reloads_total: %d series, want 2", n)
	}
}

// TestLabelValueSlicesMatchConstants guards the slices the pre-init iterates:
// every constant must be listed exactly once, or a series is silently never
// pre-created.
func TestLabelValueSlicesMatchConstants(t *testing.T) {
	check := func(name string, got, want []string) {
		t.Helper()
		if !slices.Equal(got, want) {
			t.Errorf("%s = %v, want %v", name, got, want)
		}
	}
	check("operations", operations, []string{OperationCreate, OperationUpdate, OperationDelete})
	check("statuses", statuses, []string{StatusSuccess, StatusError, StatusSkipped})
	check("errorTypes", errorTypes, []string{ErrorTypeConflict, ErrorTypeNotFound, ErrorTypeValidation, ErrorTypeForbidden, ErrorTypeTransient, ErrorTypeUnknown})
}

// TestClassifyErrorReturnsKnownErrorTypes asserts every non-nil error is
// classified to a value in errorTypes, so RecordError(ClassifyError(err)) can
// only ever increment a pre-created series.
func TestClassifyErrorReturnsKnownErrorTypes(t *testing.T) {
	inputs := []error{
		apierrors.NewConflict(schema.GroupResource{}, "x", errors.New("c")),
		apierrors.NewNotFound(schema.GroupResource{}, "x"),
		apierrors.NewInvalid(schema.GroupKind{}, "x", nil),
		apierrors.NewBadRequest("bad"),
		apierrors.NewForbidden(schema.GroupResource{}, "x", errors.New("f")),
		apierrors.NewUnauthorized("no"),
		apierrors.NewTooManyRequestsError("slow down"),
		apierrors.NewServiceUnavailable("down"),
		apierrors.NewTimeoutError("slow", 1),
		apierrors.NewServerTimeout(schema.GroupResource{}, "get", 1),
		apierrors.NewInternalError(errors.New("boom")),
		apierrors.NewAlreadyExists(schema.GroupResource{}, "x"),
		apierrors.NewResourceExpired("expired"),
		context.Canceled,
		context.DeadlineExceeded,
		&net.OpError{Op: "dial", Err: errors.New("refused")},
		errors.New("something went wrong"),
		fmt.Errorf("wrapped: %w", apierrors.NewConflict(schema.GroupResource{}, "x", errors.New("c"))),
	}
	for _, err := range inputs {
		got := ClassifyError(err)
		if !slices.Contains(errorTypes, got) {
			t.Errorf("ClassifyError(%v) = %q, not in errorTypes %v", err, got, errorTypes)
		}
	}
	if got := ClassifyError(nil); got != "" {
		t.Errorf("ClassifyError(nil) = %q, want empty (callers never record it)", got)
	}
}
