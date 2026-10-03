package tools

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	cloud "github.com/simplifyd-systems/cloud-go-sdk"
)

const zonesBase = "/v1/workspaces/ws/dns/zones"

func fakeDNSAPI(t *testing.T, handle func(w http.ResponseWriter, r *http.Request) bool) {
	t.Helper()
	mx := uint16(10)
	withFakeAPI(t, func(w http.ResponseWriter, r *http.Request) {
		if handle != nil && handle(w, r) {
			return
		}
		switch {
		case r.Method == http.MethodGet && r.URL.Path == zonesBase:
			_ = json.NewEncoder(w).Encode(map[string]any{"zones": []cloud.DNSZone{{Slug: "z1", Name: "acme.com", Status: cloud.DNSZoneActive}}})
		case r.Method == http.MethodGet && r.URL.Path == zonesBase+"/z1/records":
			_ = json.NewEncoder(w).Encode(map[string]any{"records": []cloud.DNSRecord{
				{Slug: "r1", Name: "@", Type: "MX", Value: "mx.zoho.com", TTL: 3600, Priority: &mx, Owner: "user"},
				{Slug: "r2", Name: "smail._domainkey", Type: "TXT", Value: "v=DKIM1; p=abc", TTL: 300, Owner: "platform"},
			}})
		default:
			http.NotFound(w, r)
		}
	})
}

// A record's value is what a person reads; the generic JSON redaction of
// "value" fields must not hide it.
func TestListDNSRecordsShowsValues(t *testing.T) {
	fakeDNSAPI(t, nil)
	result, _, _ := handleListDNSRecords(context.Background(), nil, dnsZoneArgs{Workspace: "ws", Zone: "acme.com"})
	out := resultText(t, result)
	for _, want := range []string{"@ MX 10 mx.zoho.com", "id r1", "v=DKIM1; p=abc", "platform-managed"} {
		if !strings.Contains(out, want) {
			t.Errorf("records do not mention %q:\n%s", want, out)
		}
	}
}

func TestUpdateDNSRecordKeepsUnchangedFields(t *testing.T) {
	var sent cloud.DNSRecordInput
	fakeDNSAPI(t, func(w http.ResponseWriter, r *http.Request) bool {
		if r.Method != http.MethodPut || r.URL.Path != zonesBase+"/z1/records/r1" {
			return false
		}
		_ = json.NewDecoder(r.Body).Decode(&sent)
		_ = json.NewEncoder(w).Encode(map[string]any{"record": cloud.DNSRecord{Slug: "r1", Name: "@", Type: "MX", Value: sent.Value, Priority: sent.Priority}})
		return true
	})
	value := "mx2.zoho.com"
	result, _, _ := handleUpdateDNSRecord(context.Background(), nil, updateDNSRecordArgs{
		Workspace: "ws", Zone: "acme.com", Record: "r1", Value: &value,
	})
	if result.IsError {
		t.Fatal(resultText(t, result))
	}
	if sent.Name != "@" || sent.Type != "MX" || sent.Value != value || sent.TTL != 3600 || sent.Priority == nil || *sent.Priority != 10 {
		t.Errorf("sent %+v", sent)
	}
}

func TestAddDNSRecordReportsWhyItWasRefused(t *testing.T) {
	fakeDNSAPI(t, func(w http.ResponseWriter, r *http.Request) bool {
		if r.Method != http.MethodPost || r.URL.Path != zonesBase+"/z1/records" {
			return false
		}
		w.WriteHeader(http.StatusBadRequest)
		_ = json.NewEncoder(w).Encode(map[string]any{"message": "a CNAME cannot be at the root of the domain; use an ALIAS record"})
		return true
	})
	result, _, _ := handleAddDNSRecord(context.Background(), nil, addDNSRecordArgs{
		Workspace: "ws", Zone: "acme.com", Name: "@", Type: "cname", Value: "acme.netlify.app",
	})
	if out := resultText(t, result); !result.IsError || !strings.Contains(out, "use an ALIAS record") {
		t.Fatalf("got %s", out)
	}
}

func TestDNSZoneByUnknownNameListsTheZones(t *testing.T) {
	fakeDNSAPI(t, nil)
	result, _, _ := handleListDNSRecords(context.Background(), nil, dnsZoneArgs{Workspace: "ws", Zone: "acme.net"})
	if out := resultText(t, result); !result.IsError || !strings.Contains(out, "acme.com") {
		t.Fatalf("got %s", out)
	}
}

func TestAddDNSZoneExplainsDelegation(t *testing.T) {
	fakeDNSAPI(t, func(w http.ResponseWriter, r *http.Request) bool {
		if r.Method != http.MethodPost || r.URL.Path != zonesBase {
			return false
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"zone": cloud.DNSZone{
			Slug: "z2", Name: "acme.org", Status: cloud.DNSZonePending,
			Nameservers: []string{"taiwo.simplifyd.net", "kehinde.simplifyd.net"},
		}})
		return true
	})
	result, _, _ := handleAddDNSZone(context.Background(), nil, addDNSZoneArgs{Workspace: "ws", Domain: "acme.org"})
	out := resultText(t, result)
	for _, want := range []string{"pending", "taiwo.simplifyd.net and kehinde.simplifyd.net", "registrar"} {
		if !strings.Contains(out, want) {
			t.Errorf("does not mention %q:\n%s", want, out)
		}
	}
}
