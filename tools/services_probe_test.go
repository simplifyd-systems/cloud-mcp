package tools

import "testing"

func TestReadinessProbeFromArgsDefaults(t *testing.T) {
	probe, err := readinessProbeFromArgs(&serviceProbeArgs{Path: "/ready", Port: 8080})
	if err != nil {
		t.Fatal(err)
	}
	if probe.PeriodSeconds != 10 || probe.TimeoutSeconds != 1 ||
		probe.FailureThreshold != 3 || probe.SuccessThreshold != 1 {
		t.Fatalf("unexpected defaults: %#v", probe)
	}
}

func TestReadinessProbeFromArgsRejectsInvalidEndpoint(t *testing.T) {
	if _, err := readinessProbeFromArgs(&serviceProbeArgs{Path: "ready", Port: 8080}); err == nil {
		t.Fatal("expected invalid path error")
	}
	if _, err := readinessProbeFromArgs(&serviceProbeArgs{Path: "/ready", Port: 0}); err == nil {
		t.Fatal("expected invalid port error")
	}
}
