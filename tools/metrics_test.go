package tools

import (
	"context"
	"encoding/json"
	"math"
	"net/http"
	"strings"
	"testing"
	"time"

	cloud "github.com/simplifyd-systems/cloud-go-sdk"
)

// series builds one sample a minute ending at a fixed time, from a function of
// the sample's index.
func series(n int, f func(i int) cloud.MetricPoint) []cloud.MetricPoint {
	end := time.Date(2026, 10, 14, 12, 0, 0, 0, time.UTC)
	out := make([]cloud.MetricPoint, n)
	for i := range out {
		p := f(i)
		p.Timestamp = end.Add(-time.Duration(n-1-i) * time.Minute)
		out[i] = p
	}
	return out
}

func hasSignal(s *metricsSummary, substr string) bool {
	for _, sig := range s.Signals {
		if strings.Contains(sig, substr) {
			return true
		}
	}
	return false
}

// Usage is summed across instances while the allocation is per instance; two
// instances at 1500m on 1 vCPU each are at 75%, not 150%.
func TestSummariseMetricsReadsUsageAgainstEveryInstance(t *testing.T) {
	m := &cloud.ServiceMetrics{
		Range: cloud.MetricsRange1h, VCPUs: 1, MemoryMiB: 512,
		Points: series(60, func(int) cloud.MetricPoint {
			return cloud.MetricPoint{CPUMillicores: 1500, MemoryMiB: 512, PodCount: 2}
		}),
	}
	s := summariseMetrics("api", m, false)
	if got := *s.CPU.CurrentPct; got != 75 {
		t.Errorf("cpu current = %v%%, want 75%%", got)
	}
	if got := *s.Memory.PeakPct; got != 50 {
		t.Errorf("memory peak = %v%%, want 50%%", got)
	}
	if s.Instances.Current != 2 {
		t.Errorf("instances = %+v", s.Instances)
	}
}

func TestSummariseMetricsFlagsSustainedCPU(t *testing.T) {
	m := &cloud.ServiceMetrics{
		Range: cloud.MetricsRange1h, VCPUs: 1, MemoryMiB: 1024,
		Points: series(60, func(int) cloud.MetricPoint {
			return cloud.MetricPoint{CPUMillicores: 900, MemoryMiB: 200, PodCount: 1}
		}),
	}
	s := summariseMetrics("api", m, false)
	if !hasSignal(s, "sustained pressure") || !hasSignal(s, "add replicas") {
		t.Errorf("signals = %q", s.Signals)
	}
}

// A service with a volume runs as a single instance, so the advice must not
// suggest replicas.
func TestSummariseMetricsDoesNotSuggestReplicasWithVolume(t *testing.T) {
	m := &cloud.ServiceMetrics{
		Range: cloud.MetricsRange1h, VCPUs: 1, MemoryMiB: 1024, HasVolume: true,
		Points: series(60, func(int) cloud.MetricPoint {
			return cloud.MetricPoint{CPUMillicores: 950, MemoryMiB: 200, PodCount: 1, DiskUsedMiB: 100, DiskCapacityMiB: 10240}
		}),
	}
	s := summariseMetrics("db", m, false)
	if hasSignal(s, "or add replicas") || !hasSignal(s, "cannot add replicas") {
		t.Errorf("signals = %q", s.Signals)
	}
}

func TestSummariseMetricsFlagsMemoryNearLimit(t *testing.T) {
	m := &cloud.ServiceMetrics{
		Range: cloud.MetricsRange1h, VCPUs: 1, MemoryMiB: 1000,
		Points: series(60, func(i int) cloud.MetricPoint {
			mem := 300
			if i == 30 {
				mem = 950
			}
			return cloud.MetricPoint{CPUMillicores: 100, MemoryMiB: mem, PodCount: 1}
		}),
	}
	s := summariseMetrics("api", m, false)
	if !hasSignal(s, "Memory peaked at 95.0%") {
		t.Errorf("signals = %q", s.Signals)
	}
}

func TestSummariseDiskProjectsDaysUntilFull(t *testing.T) {
	// 10 GiB volume growing 1 MiB a minute (1440 MiB/day) from 5000 MiB.
	m := &cloud.ServiceMetrics{
		Range: cloud.MetricsRange6h, VCPUs: 1, MemoryMiB: 1024, HasVolume: true,
		Points: series(360, func(i int) cloud.MetricPoint {
			return cloud.MetricPoint{CPUMillicores: 100, MemoryMiB: 200, PodCount: 1, DiskUsedMiB: 5000 + i, DiskCapacityMiB: 10240}
		}),
	}
	s := summariseMetrics("db", m, false)
	d := s.Disk
	if d == nil || d.GrowthMiBPerDay == nil || d.DaysUntilFull == nil {
		t.Fatalf("disk = %+v", d)
	}
	if *d.GrowthMiBPerDay != 1440 {
		t.Errorf("growth = %v MiB/day, want 1440", *d.GrowthMiBPerDay)
	}
	// (10240 - 5359) / 1440 ≈ 3.4 days.
	if *d.DaysUntilFull != 3.4 {
		t.Errorf("days until full = %v, want 3.4", *d.DaysUntilFull)
	}
	if !hasSignal(s, "fills in about 3 days") {
		t.Errorf("signals = %q", s.Signals)
	}
}

// Expanding a volume mid-window must not read as the volume emptying.
func TestSummariseDiskFitsOnlyCurrentCapacity(t *testing.T) {
	m := &cloud.ServiceMetrics{
		Range: cloud.MetricsRange6h, VCPUs: 1, MemoryMiB: 1024, HasVolume: true,
		Points: series(360, func(i int) cloud.MetricPoint {
			capacity := 5120
			if i >= 120 {
				capacity = 20480
			}
			return cloud.MetricPoint{CPUMillicores: 100, MemoryMiB: 200, PodCount: 1, DiskUsedMiB: 4000 + i, DiskCapacityMiB: capacity}
		}),
	}
	d := summariseMetrics("db", m, false).Disk
	if d.CapacityMiB != 20480 || *d.GrowthMiBPerDay != 1440 {
		t.Errorf("disk = %+v growth = %v", d, *d.GrowthMiBPerDay)
	}
}

func TestSummariseMetricsFlagsOverProvisioning(t *testing.T) {
	m := &cloud.ServiceMetrics{
		Range: cloud.MetricsRange1d, VCPUs: 2, MemoryMiB: 4096,
		Points: series(1440, func(int) cloud.MetricPoint {
			return cloud.MetricPoint{CPUMillicores: 50, MemoryMiB: 300, PodCount: 1}
		}),
	}
	s := summariseMetrics("api", m, false)
	if !hasSignal(s, "Over-provisioned") {
		t.Errorf("signals = %q", s.Signals)
	}
}

func TestSummariseMetricsNotesThinCoverage(t *testing.T) {
	m := &cloud.ServiceMetrics{
		Range: cloud.MetricsRange1d, VCPUs: 1, MemoryMiB: 1024,
		Points: series(90, func(int) cloud.MetricPoint {
			return cloud.MetricPoint{CPUMillicores: 100, MemoryMiB: 200, PodCount: 1}
		}),
	}
	s := summariseMetrics("api", m, false)
	if !hasSignal(s, "Only 90 samples") {
		t.Errorf("signals = %q", s.Signals)
	}
	if hasSignal(s, "Over-provisioned") {
		t.Errorf("an hour and a half should not be enough to call it over-provisioned: %q", s.Signals)
	}
}

// Fixed-footprint services have no allocation; the raw figures must still come
// through without percentages or signals built on a zero divisor.
func TestSummariseMetricsWithoutAllocation(t *testing.T) {
	m := &cloud.ServiceMetrics{
		Range: cloud.MetricsRange1h,
		Points: series(60, func(int) cloud.MetricPoint {
			return cloud.MetricPoint{CPUMillicores: 40, MemoryMiB: 64, PodCount: 1}
		}),
	}
	s := summariseMetrics("vpn", m, true)
	if s.CPU.Current != 40 || s.CPU.CurrentPct != nil || s.Memory.PeakPct != nil {
		t.Errorf("cpu = %+v memory = %+v", s.CPU, s.Memory)
	}
	if len(s.Signals) != 1 || !hasSignal(s, "No resource pressure") {
		t.Errorf("signals = %q", s.Signals)
	}
}

func TestSummariseMetricsEmpty(t *testing.T) {
	s := summariseMetrics("site", &cloud.ServiceMetrics{Range: cloud.MetricsRange1d}, false)
	if s.Samples != 0 || s.CPU != nil || !hasSignal(s, "No samples") {
		t.Errorf("summary = %+v", s)
	}
}

func TestMetricsSeriesIsBounded(t *testing.T) {
	m := &cloud.ServiceMetrics{
		Range: cloud.MetricsRange1d, VCPUs: 1, MemoryMiB: 1024,
		Points: series(1440, func(i int) cloud.MetricPoint {
			return cloud.MetricPoint{CPUMillicores: i % 1000, MemoryMiB: 200, PodCount: 1}
		}),
	}
	s := summariseMetrics("api", m, true)
	if n := len(s.Series); n == 0 || n > maxSeriesBuckets+1 {
		t.Fatalf("series has %d buckets", n)
	}
	for i := 1; i < len(s.Series); i++ {
		if !s.Series[i].Start.After(s.Series[i-1].Start) {
			t.Fatalf("buckets out of order at %d", i)
		}
	}
}

func TestParseMetricsRange(t *testing.T) {
	if r, err := parseMetricsRange(""); err != nil || r != cloud.MetricsRange1d {
		t.Errorf("default = %q, %v", r, err)
	}
	if _, err := parseMetricsRange("30d"); err == nil {
		t.Error("30d accepted, but the platform keeps only two days")
	}
}

// 1 vCPU is the least a service can have, so a quiet 1-vCPU service is told
// only about the memory it could give back, and nothing at all when memory is
// in use too.
func TestOverProvisioningNamesOnlyWhatCanComeDown(t *testing.T) {
	quiet := func(memMiB, usedMiB int) *metricsSummary {
		return summariseMetrics("api", &cloud.ServiceMetrics{
			Range: cloud.MetricsRange1d, VCPUs: 1, MemoryMiB: memMiB,
			Points: series(1440, func(int) cloud.MetricPoint {
				return cloud.MetricPoint{CPUMillicores: 10, MemoryMiB: usedMiB, PodCount: 1}
			}),
		}, false)
	}

	s := quiet(1024, 200)
	if !hasSignal(s, "memory (peak 19.5% of 1024 MiB) could come down") || hasSignal(s, "vcpus") {
		t.Errorf("signals = %q", s.Signals)
	}

	s = quiet(256, 120)
	if hasSignal(s, "Over-provisioned") || !hasSignal(s, "No resource pressure") {
		t.Errorf("a 1-vCPU service using its memory has nothing to give back: %q", s.Signals)
	}
}

func TestOverProvisioningNamesBothWhenBothCanComeDown(t *testing.T) {
	s := summariseMetrics("api", &cloud.ServiceMetrics{
		Range: cloud.MetricsRange1d, VCPUs: 2, MemoryMiB: 4096,
		Points: series(1440, func(int) cloud.MetricPoint {
			return cloud.MetricPoint{CPUMillicores: 50, MemoryMiB: 300, PodCount: 1}
		}),
	}, false)
	if !hasSignal(s, "vcpus (peak 2.5% of 2) and memory (peak 7.3% of 4096 MiB) could come down") {
		t.Errorf("signals = %q", s.Signals)
	}
}

// A flat volume with a little jitter fits a tiny negative slope, which must
// read as no growth rather than "-0".
func TestRound1HasNoNegativeZero(t *testing.T) {
	if got := *round1(-0.01); math.Signbit(got) {
		t.Errorf("round1(-0.01) = %v, want 0", got)
	}
	b, _ := json.Marshal(round1(-0.01))
	if string(b) != "0" {
		t.Errorf("marshalled as %s, want 0", b)
	}
}

func TestEnvironmentMetricsNamesServicesAndOrdersByPressure(t *testing.T) {
	base := "/v1/workspaces/ws/projects/shop/envs/production/svcs"
	withFakeAPI(t, func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case base:
			_ = json.NewEncoder(w).Encode([]map[string]any{
				{"slug": "01a0-quiet", "name": "web"},
				{"slug": "01a0-busy", "name": "worker"},
				{"slug": "01a0-site", "name": "docs"},
			})
		case base + "/01a0-quiet/metrics", base + "/01a0-busy/metrics":
			cpu := 100
			if strings.Contains(r.URL.Path, "busy") {
				cpu = 950
			}
			_ = json.NewEncoder(w).Encode(cloud.ServiceMetrics{
				Range: cloud.MetricsRange1h, VCPUs: 1, MemoryMiB: 1024,
				Points: series(60, func(int) cloud.MetricPoint {
					return cloud.MetricPoint{CPUMillicores: cpu, MemoryMiB: 200, PodCount: 1}
				}),
			})
		case base + "/01a0-site/metrics":
			_, _ = w.Write([]byte(`{"range":"1h","points":[]}`))
		default:
			http.NotFound(w, r)
		}
	})

	result, _, _ := handleGetEnvironmentMetrics(context.Background(), nil, envMetricsArgs{
		Workspace: "ws", Project: "shop", Env: "production", Range: "1h",
	})
	if result.IsError {
		t.Fatalf("error: %s", resultText(t, result))
	}
	var out struct {
		Services []struct {
			Service string `json:"service"`
			Name    string `json:"name"`
		} `json:"services"`
		NotMeasured []string `json:"not_measured"`
	}
	if err := json.Unmarshal([]byte(resultText(t, result)), &out); err != nil {
		t.Fatal(err)
	}
	if len(out.Services) != 2 || out.Services[0].Name != "worker" || out.Services[1].Name != "web" {
		t.Errorf("services = %+v, want worker then web", out.Services)
	}
	if len(out.NotMeasured) != 1 || out.NotMeasured[0] != "01a0-site" {
		t.Errorf("not_measured = %v", out.NotMeasured)
	}
}

func TestEnvironmentMetricsSaysWhenThereAreNoServices(t *testing.T) {
	withFakeAPI(t, func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`[]`))
	})
	result, _, _ := handleGetEnvironmentMetrics(context.Background(), nil, envMetricsArgs{
		Workspace: "ws", Project: "shop", Env: "dev",
	})
	if out := resultText(t, result); result.IsError || !strings.Contains(out, "has no services") {
		t.Errorf("result = %s", out)
	}
}
