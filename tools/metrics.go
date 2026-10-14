package tools

import (
	"context"
	"fmt"
	"math"
	"sort"
	"sync"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	cloud "github.com/simplifyd-systems/cloud-go-sdk"
)

// Resource metrics for services.
//
// The API keeps one sample a minute, so even a day is 1,440 points.
// Handing those to a model would spend its context on numbers it then has to
// summarise itself, and it would do so inconsistently. These tools do the
// arithmetic once — percentages of allocation, percentiles, disk growth — and
// add plain-language signals for the decisions an agent is actually making:
// does this service need more CPU, more memory, more instances or a bigger
// volume, or could it run on less.

// Thresholds behind the signals. They are deliberately round: an agent quoting
// "p95 CPU above 80%" back to a person should be quoting a rule they can
// recognise.
const (
	cpuHotP95Pct      = 80.0
	cpuBurstPeakPct   = 95.0
	memHighP95Pct     = 80.0
	memNearLimitPct   = 90.0
	diskHighPct       = 80.0
	diskFullSoonDays  = 14.0
	idleCPUPeakPct    = 25.0
	idleMemPeakPct    = 40.0
	maxSeriesBuckets  = 48
	envMetricsWorkers = 4
)

// The API also accepts 7d and 30d, but it keeps samples for only two days, so
// those ranges return the same two days under a longer label. Offering them
// here would invite an agent to read a weekend as a month.
var metricsRanges = map[cloud.MetricsRange]time.Duration{
	cloud.MetricsRange1h: time.Hour,
	cloud.MetricsRange6h: 6 * time.Hour,
	cloud.MetricsRange1d: 24 * time.Hour,
}

// parseMetricsRange defaults to a day rather than the API's hour: an hour says
// what a service is doing now, but a scaling decision wants to have seen the
// daily peak.
func parseMetricsRange(s string) (cloud.MetricsRange, error) {
	if s == "" {
		return cloud.MetricsRange1d, nil
	}
	r := cloud.MetricsRange(s)
	if _, ok := metricsRanges[r]; !ok {
		return "", fmt.Errorf("range must be one of 1h, 6h or 1d, got %q", s)
	}
	return r, nil
}

type allocation struct {
	VCPUs     int `json:"vcpus"`
	MemoryMiB int `json:"memory_mib"`
}

type instanceSummary struct {
	Current int `json:"current"`
	Min     int `json:"min"`
	Max     int `json:"max"`
}

// usageSummary describes CPU or memory. The raw figures are totals across all
// instances; the percentages are of what those instances were allocated, and
// are absent when the service has no per-instance allocation to compare with.
type usageSummary struct {
	Unit       string   `json:"unit"`
	Current    int      `json:"current"`
	Peak       int      `json:"peak"`
	CurrentPct *float64 `json:"current_pct,omitempty"`
	AvgPct     *float64 `json:"avg_pct,omitempty"`
	P95Pct     *float64 `json:"p95_pct,omitempty"`
	PeakPct    *float64 `json:"peak_pct,omitempty"`
}

type diskSummary struct {
	UsedMiB     int     `json:"used_mib"`
	CapacityMiB int     `json:"capacity_mib"`
	UsedPct     float64 `json:"used_pct"`
	// Growth is a least-squares fit over the window, so a single large write
	// does not read as a trend. Absent when the window is too short to fit.
	GrowthMiBPerDay *float64 `json:"growth_mib_per_day,omitempty"`
	DaysUntilFull   *float64 `json:"days_until_full,omitempty"`
}

type metricsBucket struct {
	Start         time.Time `json:"start"`
	Instances     int       `json:"instances"`
	CPUAvgPct     *float64  `json:"cpu_avg_pct,omitempty"`
	CPUPeakPct    *float64  `json:"cpu_peak_pct,omitempty"`
	MemoryAvgPct  *float64  `json:"memory_avg_pct,omitempty"`
	MemoryPeakPct *float64  `json:"memory_peak_pct,omitempty"`
	CPUPeak       int       `json:"cpu_peak_millicores"`
	MemoryPeak    int       `json:"memory_peak_mib"`
	DiskUsedMiB   int       `json:"disk_used_mib,omitempty"`
}

type metricsSummary struct {
	Service     string           `json:"service"`
	Range       string           `json:"range"`
	Samples     int              `json:"samples"`
	From        *time.Time       `json:"from,omitempty"`
	To          *time.Time       `json:"to,omitempty"`
	PerInstance allocation       `json:"allocation_per_instance"`
	Instances   *instanceSummary `json:"instances,omitempty"`
	CPU         *usageSummary    `json:"cpu,omitempty"`
	Memory      *usageSummary    `json:"memory,omitempty"`
	Disk        *diskSummary     `json:"disk,omitempty"`
	Signals     []string         `json:"signals"`
	Series      []metricsBucket  `json:"series,omitempty"`

	// pressure orders services in an environment sweep, worst first.
	pressure float64
}

func pct(used, allocated float64) (float64, bool) {
	if allocated <= 0 {
		return 0, false
	}
	return used / allocated * 100, true
}

func round1(f float64) *float64 {
	r := math.Round(f*10) / 10
	return &r
}

// cpuPct and memPct read a sample against the allocation of the instances it
// was taken across. Each instance gets the full per-instance allocation, so a
// two-instance service at 1500m on 1 vCPU each is at 75%, not 150%.
func cpuPct(p cloud.MetricPoint, alloc allocation) (float64, bool) {
	return pct(float64(p.CPUMillicores), float64(alloc.VCPUs*1000*p.PodCount))
}

func memPct(p cloud.MetricPoint, alloc allocation) (float64, bool) {
	return pct(float64(p.MemoryMiB), float64(alloc.MemoryMiB*p.PodCount))
}

func summariseUsage(points []cloud.MetricPoint, unit string, raw func(cloud.MetricPoint) int, ofAlloc func(cloud.MetricPoint) (float64, bool)) *usageSummary {
	u := &usageSummary{Unit: unit, Current: raw(points[len(points)-1])}
	var pcts []float64
	var sum float64
	for _, p := range points {
		if v := raw(p); v > u.Peak {
			u.Peak = v
		}
		if v, ok := ofAlloc(p); ok {
			pcts = append(pcts, v)
			sum += v
		}
	}
	if len(pcts) == 0 {
		return u
	}
	if v, ok := ofAlloc(points[len(points)-1]); ok {
		u.CurrentPct = round1(v)
	}
	u.AvgPct = round1(sum / float64(len(pcts)))
	sort.Float64s(pcts)
	u.P95Pct = round1(pcts[int(math.Ceil(0.95*float64(len(pcts))))-1])
	u.PeakPct = round1(pcts[len(pcts)-1])
	return u
}

// summariseDisk reads the latest volume sample and fits growth over the
// samples that share its capacity. A volume that was expanded mid-window
// would otherwise show the expansion as a sudden drop in utilisation.
func summariseDisk(points []cloud.MetricPoint) *diskSummary {
	var latest *cloud.MetricPoint
	for i := len(points) - 1; i >= 0; i-- {
		if points[i].DiskCapacityMiB > 0 {
			latest = &points[i]
			break
		}
	}
	if latest == nil {
		return nil
	}
	d := &diskSummary{
		UsedMiB:     latest.DiskUsedMiB,
		CapacityMiB: latest.DiskCapacityMiB,
		UsedPct:     *round1(float64(latest.DiskUsedMiB) / float64(latest.DiskCapacityMiB) * 100),
	}

	var xs, ys []float64
	for _, p := range points {
		if p.DiskCapacityMiB == latest.DiskCapacityMiB {
			xs = append(xs, p.Timestamp.Sub(latest.Timestamp).Hours()/24)
			ys = append(ys, float64(p.DiskUsedMiB))
		}
	}
	// Under an hour of history, a fit is mostly noise from whatever the
	// service happened to be writing.
	if len(xs) < 2 || -xs[0] < 1.0/24 {
		return d
	}
	slope := leastSquaresSlope(xs, ys)
	d.GrowthMiBPerDay = round1(slope)
	if slope > 0 {
		d.DaysUntilFull = round1(float64(latest.DiskCapacityMiB-latest.DiskUsedMiB) / slope)
	}
	return d
}

func leastSquaresSlope(xs, ys []float64) float64 {
	n := float64(len(xs))
	var sx, sy, sxx, sxy float64
	for i := range xs {
		sx += xs[i]
		sy += ys[i]
		sxx += xs[i] * xs[i]
		sxy += xs[i] * ys[i]
	}
	den := n*sxx - sx*sx
	if den == 0 {
		return 0
	}
	return (n*sxy - sx*sy) / den
}

func summariseMetrics(svc string, m *cloud.ServiceMetrics, withSeries bool) *metricsSummary {
	s := &metricsSummary{
		Service:     svc,
		Range:       string(m.Range),
		Samples:     len(m.Points),
		PerInstance: allocation{VCPUs: m.VCPUs, MemoryMiB: m.MemoryMiB},
	}
	if len(m.Points) == 0 {
		s.Signals = []string{fmt.Sprintf(
			"No samples in the last %s: the service was not running, or it is a type the platform does not measure (static sites, buckets).",
			m.Range,
		)}
		return s
	}

	points := m.Points
	first, last := points[0].Timestamp, points[len(points)-1].Timestamp
	s.From, s.To = &first, &last

	inst := &instanceSummary{Current: points[len(points)-1].PodCount, Min: points[0].PodCount, Max: points[0].PodCount}
	for _, p := range points {
		inst.Min = min(inst.Min, p.PodCount)
		inst.Max = max(inst.Max, p.PodCount)
	}
	s.Instances = inst

	alloc := s.PerInstance
	s.CPU = summariseUsage(points, "millicores",
		func(p cloud.MetricPoint) int { return p.CPUMillicores },
		func(p cloud.MetricPoint) (float64, bool) { return cpuPct(p, alloc) })
	s.Memory = summariseUsage(points, "MiB",
		func(p cloud.MetricPoint) int { return p.MemoryMiB },
		func(p cloud.MetricPoint) (float64, bool) { return memPct(p, alloc) })
	s.Disk = summariseDisk(points)

	s.Signals, s.pressure = metricsSignals(s, m)
	if withSeries {
		s.Series = metricsSeries(points, alloc, metricsRanges[m.Range])
	}
	return s
}

func metricsSignals(s *metricsSummary, m *cloud.ServiceMetrics) ([]string, float64) {
	var out []string
	var pressure float64
	rng := string(m.Range)

	// A service with a volume can only run as one instance, so more replicas
	// is not an answer for it and the advice must say so.
	scaleCPU := "Raise vcpus, or add replicas if requests spread across instances."
	if m.HasVolume {
		scaleCPU = "Raise vcpus: this service mounts a volume, so it runs as a single instance and cannot add replicas."
	}

	if c := s.CPU; c.P95Pct != nil {
		pressure = max(pressure, *c.P95Pct)
		switch {
		case *c.P95Pct >= cpuHotP95Pct:
			out = append(out, fmt.Sprintf(
				"CPU is under sustained pressure: 95th percentile %.1f%% of allocation over the last %s. %s",
				*c.P95Pct, rng, scaleCPU))
		case *c.PeakPct >= cpuBurstPeakPct:
			out = append(out, fmt.Sprintf(
				"CPU reaches its limit in bursts (peak %.1f%%, 95th percentile %.1f%%): it is slowed during spikes but has headroom the rest of the time. Scale only if the spikes coincide with slow responses.",
				*c.PeakPct, *c.P95Pct))
		}
	}

	if mem := s.Memory; mem.PeakPct != nil {
		pressure = max(pressure, *mem.PeakPct)
		switch {
		case *mem.PeakPct >= memNearLimitPct:
			out = append(out, fmt.Sprintf(
				"Memory peaked at %.1f%% of allocation over the last %s. An instance that reaches its memory limit is restarted. Raise memory; more replicas help only if memory use grows with traffic.",
				*mem.PeakPct, rng))
		case *mem.P95Pct >= memHighP95Pct:
			out = append(out, fmt.Sprintf(
				"Memory is running high: 95th percentile %.1f%% of allocation. Consider raising memory before load grows.",
				*mem.P95Pct))
		}
	}

	if d := s.Disk; d != nil {
		pressure = max(pressure, d.UsedPct)
		soon := d.DaysUntilFull != nil && *d.DaysUntilFull <= diskFullSoonDays
		if d.UsedPct >= diskHighPct || soon {
			msg := fmt.Sprintf("Volume is %.1f%% full (%d of %d MiB).", d.UsedPct, d.UsedMiB, d.CapacityMiB)
			if d.DaysUntilFull != nil {
				msg += fmt.Sprintf(" At the current rate of %.1f MiB/day it fills in about %.0f days.", *d.GrowthMiBPerDay, *d.DaysUntilFull)
			}
			msg += " Expand it with update-service-volume; a full volume makes writes fail."
			out = append(out, msg)
		}
	}

	// Over-provisioning is only worth naming with a full day of history behind
	// it: an hour of a quiet service says nothing about its busy hour.
	span := s.To.Sub(*s.From)
	if len(out) == 0 && span >= 20*time.Hour &&
		s.CPU.PeakPct != nil && *s.CPU.PeakPct < idleCPUPeakPct &&
		s.Memory.PeakPct != nil && *s.Memory.PeakPct < idleMemPeakPct {
		out = append(out, fmt.Sprintf(
			"Over-provisioned: CPU never exceeded %.1f%% and memory %.1f%% of allocation over the last %s. It could run on less.",
			*s.CPU.PeakPct, *s.Memory.PeakPct, rng))
	}

	if s.Instances.Min != s.Instances.Max {
		out = append(out, fmt.Sprintf(
			"Instance count changed during the window (between %d and %d); percentages are read against the instances running at each sample.",
			s.Instances.Min, s.Instances.Max))
	}

	// Samples are a minute apart while the service runs. Far fewer than the
	// window holds means it was stopped or new for part of it, and the
	// summary describes less time than the range suggests.
	if want := metricsRanges[m.Range].Minutes(); float64(s.Samples) < want/2 {
		out = append(out, fmt.Sprintf(
			"Only %d samples (about %s) cover the last %s: the service was stopped or recently started for part of it, so read the figures with that in mind.",
			s.Samples, span.Round(time.Minute), rng))
	}

	if len(out) == 0 {
		out = append(out, fmt.Sprintf("No resource pressure over the last %s.", rng))
	}
	return out, pressure
}

// metricsSeries folds the window into at most maxSeriesBuckets time buckets,
// enough to see when in the day the peaks fall without the raw samples.
func metricsSeries(points []cloud.MetricPoint, alloc allocation, window time.Duration) []metricsBucket {
	width := window / maxSeriesBuckets
	if width < time.Minute {
		width = time.Minute
	}
	end := points[len(points)-1].Timestamp
	start := end.Add(-window)

	type acc struct {
		b                metricsBucket
		cpuSum, memSum   float64
		cpuN, memN       int
		cpuPeak, memPeak float64
	}
	var buckets []*acc
	idx := map[int]*acc{}
	for _, p := range points {
		i := max(0, int(p.Timestamp.Sub(start)/width))
		a, ok := idx[i]
		if !ok {
			a = &acc{b: metricsBucket{Start: start.Add(time.Duration(i) * width)}}
			idx[i] = a
			buckets = append(buckets, a)
		}
		a.b.Instances = max(a.b.Instances, p.PodCount)
		a.b.CPUPeak = max(a.b.CPUPeak, p.CPUMillicores)
		a.b.MemoryPeak = max(a.b.MemoryPeak, p.MemoryMiB)
		if p.DiskCapacityMiB > 0 {
			a.b.DiskUsedMiB = p.DiskUsedMiB
		}
		if v, ok := cpuPct(p, alloc); ok {
			a.cpuSum += v
			a.cpuN++
			a.cpuPeak = max(a.cpuPeak, v)
		}
		if v, ok := memPct(p, alloc); ok {
			a.memSum += v
			a.memN++
			a.memPeak = max(a.memPeak, v)
		}
	}

	out := make([]metricsBucket, 0, len(buckets))
	for _, a := range buckets {
		if a.cpuN > 0 {
			a.b.CPUAvgPct = round1(a.cpuSum / float64(a.cpuN))
			a.b.CPUPeakPct = round1(a.cpuPeak)
		}
		if a.memN > 0 {
			a.b.MemoryAvgPct = round1(a.memSum / float64(a.memN))
			a.b.MemoryPeakPct = round1(a.memPeak)
		}
		out = append(out, a.b)
	}
	return out
}

// ---- get-service-metrics ----

type serviceMetricsArgs struct {
	Workspace     string `json:"workspace"                jsonschema:"Workspace slug or name"`
	Project       string `json:"project"                  jsonschema:"Project slug or name"`
	Env           string `json:"env"                      jsonschema:"Environment slug or name"`
	Service       string `json:"service"                  jsonschema:"Service slug"`
	Range         string `json:"range,omitempty"          jsonschema:"How far back to look: 1h, 6h or 1d. Defaults to 1d."`
	IncludeSeries bool   `json:"include_series,omitempty" jsonschema:"Also return the window folded into up to 48 time buckets, to see when peaks happen. Off by default."`
}

func handleGetServiceMetrics(
	ctx context.Context,
	req *mcp.CallToolRequest,
	args serviceMetricsArgs,
) (*mcp.CallToolResult, any, error) {
	api, r, ok := sdkFor(req)
	if !ok {
		return r, nil, nil
	}
	rng, err := parseMetricsRange(args.Range)
	if err != nil {
		return toolError(err.Error()), nil, nil
	}
	m, err := services(api, args.Workspace, args.Project, args.Env).Metrics(ctx, args.Service, rng)
	if err != nil {
		return apiErr("get service metrics", err), nil, nil
	}
	return jsonText(summariseMetrics(args.Service, m, args.IncludeSeries)), nil, nil
}

// ---- get-environment-metrics ----

type envMetricsArgs struct {
	Workspace string `json:"workspace"       jsonschema:"Workspace slug or name"`
	Project   string `json:"project"         jsonschema:"Project slug or name"`
	Env       string `json:"env"             jsonschema:"Environment slug or name"`
	Range     string `json:"range,omitempty" jsonschema:"How far back to look: 1h, 6h or 1d. Defaults to 1d."`
}

type envMetricsResult struct {
	Range    string            `json:"range"`
	Services []*metricsSummary `json:"services"`
	// NotMeasured lists services with no samples in the window, so an agent
	// can tell "fine" from "not looked at".
	NotMeasured []string          `json:"not_measured,omitempty"`
	Errors      map[string]string `json:"errors,omitempty"`
}

func handleGetEnvironmentMetrics(
	ctx context.Context,
	req *mcp.CallToolRequest,
	args envMetricsArgs,
) (*mcp.CallToolResult, any, error) {
	api, r, ok := sdkFor(req)
	if !ok {
		return r, nil, nil
	}
	rng, err := parseMetricsRange(args.Range)
	if err != nil {
		return toolError(err.Error()), nil, nil
	}
	svcClient := services(api, args.Workspace, args.Project, args.Env)
	svcs, err := svcClient.List(ctx)
	if err != nil {
		return apiErr("list services", err), nil, nil
	}

	type result struct {
		slug    string
		summary *metricsSummary
		err     error
	}
	results := make([]result, len(svcs))
	sem := make(chan struct{}, envMetricsWorkers)
	var wg sync.WaitGroup
	for i, svc := range svcs {
		wg.Add(1)
		go func(i int, slug string) {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()
			m, err := svcClient.Metrics(ctx, slug, rng)
			if err != nil {
				results[i] = result{slug: slug, err: err}
				return
			}
			results[i] = result{slug: slug, summary: summariseMetrics(slug, m, false)}
		}(i, svc.Slug)
	}
	wg.Wait()

	out := envMetricsResult{Range: string(rng), Services: []*metricsSummary{}}
	for _, r := range results {
		switch {
		case r.err != nil:
			if out.Errors == nil {
				out.Errors = map[string]string{}
			}
			out.Errors[r.slug] = r.err.Error()
		case r.summary.Samples == 0:
			out.NotMeasured = append(out.NotMeasured, r.slug)
		default:
			out.Services = append(out.Services, r.summary)
		}
	}
	sort.SliceStable(out.Services, func(i, j int) bool {
		return out.Services[i].pressure > out.Services[j].pressure
	})
	return jsonText(out), nil, nil
}

func RegisterMetricsTools(s *mcp.Server) {
	addTool(s, &mcp.Tool{
		Name: "get-service-metrics",
		Description: "Get CPU, memory and disk usage for a service over a window (1h, 6h or 1d; default 1d), summarised for scaling decisions. " +
			"Returns the per-instance allocation, instance count, current/average/95th-percentile/peak usage as a percentage of allocation, volume usage with growth rate and days until full, and plain-language signals " +
			"(sustained CPU pressure, memory near its limit, a volume filling up, over-provisioning). " +
			"Use it before changing vcpus, memory or replicas with update-service, or a volume's size with update-service-volume. Set include_series to see when in the window the peaks fall.",
	}, handleGetServiceMetrics)

	addTool(s, &mcp.Tool{
		Name: "get-environment-metrics",
		Description: "Summarise CPU, memory and disk usage for every service in an environment over a window (1h, 6h or 1d; default 1d), most pressured first. " +
			"Each entry has the same summary and signals as get-service-metrics. Use it to find which services need scaling up, or could be scaled down, without checking them one by one.",
	}, handleGetEnvironmentMetrics)
}
