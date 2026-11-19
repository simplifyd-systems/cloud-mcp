package tools

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	cloud "github.com/simplifyd-systems/cloud-go-sdk"
)

func pendingVerification(host string) *cloud.DomainVerification {
	return &cloud.DomainVerification{
		Hostname: host, Status: cloud.DomainVerificationPending, RecordType: "TXT",
		RecordName: "_simplifyd-verify." + host, RecordValue: "simplifyd-verify=3f9c",
	}
}

// An agent passes next_step on to the user as it is, so it must name the TXT
// record whenever the domain is pending, and never ask for a record the
// platform already wrote.
func TestDomainNextStep(t *testing.T) {
	verified := &cloud.DomainVerification{Status: cloud.DomainVerificationVerified}
	cases := []struct {
		name       string
		step       domainStep
		want       []string
		wantNoneOf []string
	}{
		{"external, pending",
			domainStep{Domain: "cdn.example.com", CNAMETarget: "edge.simplifyd.app", Verification: pendingVerification("cdn.example.com"), Meanwhile: "the site serves on https://web.simplifyd.site"},
			[]string{"TXT record named _simplifyd-verify.cdn.example.com with the value simplifyd-verify=3f9c", "CNAME for cdn.example.com at edge.simplifyd.app", "not served until", "verify-domain", "https://web.simplifyd.site"},
			nil},
		{"external, verified",
			domainStep{Domain: "cdn.example.com", CNAMETarget: "edge.simplifyd.app", Verification: verified},
			[]string{"CNAME for cdn.example.com", "verified"},
			[]string{"TXT"}},
		{"hosted zone, verified",
			domainStep{Domain: "cdn.example.com", CNAMETarget: "edge.simplifyd.app", Zone: "example.com", Verification: verified},
			[]string{"nothing to do", "example.com zone"},
			[]string{"CNAME", "TXT"}},
		{"hosted zone not delegated yet",
			domainStep{Domain: "cdn.example.com", CNAMETarget: "edge.simplifyd.app", Zone: "example.com", Verification: pendingVerification("cdn.example.com")},
			[]string{"nameservers", "check-dns-zone", "_simplifyd-verify.cdn.example.com"},
			[]string{"CNAME for"}},
		{"hosted zone, records refused",
			domainStep{Domain: "cdn.example.com", CNAMETarget: "edge.simplifyd.app", Zone: "example.com", DNSError: "cdn already has an A record", Verification: pendingVerification("cdn.example.com")},
			[]string{"TXT record", "CNAME for cdn.example.com"},
			nil},
		// An API from before verification sends none and serves the domain.
		{"no verification",
			domainStep{Domain: "cdn.example.com", CNAMETarget: "edge.simplifyd.app"},
			[]string{"CNAME for cdn.example.com"},
			[]string{"TXT"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := tc.step.nextStep()
			for _, w := range tc.want {
				if !strings.Contains(got, w) {
					t.Errorf("next step %q lacks %q", got, w)
				}
			}
			for _, w := range tc.wantNoneOf {
				if strings.Contains(got, w) {
					t.Errorf("next step %q should not mention %q", got, w)
				}
			}
		})
	}
}

// newFakeDomainAPI serves a public bucket, an HTTP service and the verify
// endpoint, and records the requests that change anything.
func newFakeDomainAPI(t *testing.T) *[]string {
	t.Helper()
	var writes []string
	const base = "/v1/workspaces/ws/projects/p/envs/dev/svcs"
	bucket := cloud.S3Bucket{BucketName: "assets", Public: true, PublicURL: "https://assets.web.simplifyd.site", DomainCNAMETarget: "edge.simplifyd.app"}
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		body, _ := io.ReadAll(r.Body)
		if r.Method != http.MethodGet {
			writes = append(writes, r.Method+" "+r.URL.RequestURI()+" "+strings.TrimSpace(string(body)))
		}
		switch {
		case r.Method == http.MethodPost && r.URL.Path == base+"/assets/s3/domains":
			bucket.CustomDomains = append(bucket.CustomDomains, cloud.StaticSiteDomain{
				Slug: "d1", Domain: "cdn.example.com", Status: "pending", Verification: pendingVerification("cdn.example.com"),
			})
			_ = json.NewEncoder(w).Encode(bucket)
		case r.Method == http.MethodPost && strings.HasSuffix(r.URL.Path, "/deployments"):
			_, _ = w.Write([]byte(`{"slug":"dep-1"}`))
		case r.Method == http.MethodPost && r.URL.Path == base+"/api/ingress":
			_, _ = w.Write([]byte(`{"success":true,"cname":"edge.simplifyd.app"}`))
		case r.Method == http.MethodGet && r.URL.Path == base+"/api":
			_ = json.NewEncoder(w).Encode(cloud.Service{Slug: "api", Ingress: []cloud.IngressPort{{
				Slug: "p1", Protocol: "http", Port: 8080, VanityFQDN: "api.simplifyd.app",
				CustomFQDNs: []cloud.FQDN{{FQDN: "api.example.com", CNAME: "edge.simplifyd.app", Verification: pendingVerification("api.example.com")}},
			}}})
		case r.Method == http.MethodPost && r.URL.Path == base+"/api/domains/verify":
			v := pendingVerification("api.example.com")
			v.LastError = "No TXT record found at _simplifyd-verify.api.example.com yet."
			_ = json.NewEncoder(w).Encode(v)
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(ts.Close)
	t.Setenv("SIMPLIFYD_API_URL", ts.URL)
	return &writes
}

func TestAddBucketDomainGivesTheTXTRecordAndDeploys(t *testing.T) {
	writes := newFakeDomainAPI(t)
	res, _, _ := handleAddBucketDomain(context.Background(), httpRequest("Bearer tok"),
		bucketDomainArgs{Workspace: "ws", Project: "p", Env: "dev", Service: "assets", Domain: "CDN.example.com."})
	out := resultText(t, res)
	if res.IsError {
		t.Fatalf("add failed: %s", out)
	}
	for _, want := range []string{`"record_value": "simplifyd-verify=3f9c"`, "TXT record named _simplifyd-verify.cdn.example.com", "CNAME for cdn.example.com at edge.simplifyd.app"} {
		if !strings.Contains(out, want) {
			t.Errorf("result lacks %q:\n%s", want, out)
		}
	}
	want := []string{
		`POST /v1/workspaces/ws/projects/p/envs/dev/svcs/assets/s3/domains {"domain":"cdn.example.com"}`,
		`POST /v1/workspaces/ws/projects/p/envs/dev/svcs/assets/deployments?auto_approve_change_sets=true `,
	}
	if strings.Join(*writes, "\n") != strings.Join(want, "\n") {
		t.Fatalf("writes = %q, want %q", *writes, want)
	}
}

func TestAddServiceIngressGivesTheTXTRecord(t *testing.T) {
	newFakeDomainAPI(t)
	res, _, _ := handleAddServiceIngress(context.Background(), httpRequest("Bearer tok"),
		addIngressArgs{Workspace: "ws", Project: "p", Env: "dev", Service: "api", Port: 8080, CustomFQDN: "api.example.com"})
	out := resultText(t, res)
	if res.IsError {
		t.Fatalf("add failed: %s", out)
	}
	for _, want := range []string{`"next_step"`, "TXT record named _simplifyd-verify.api.example.com", `"vanity_fqdn": "api.simplifyd.app"`} {
		if !strings.Contains(out, want) {
			t.Errorf("result lacks %q:\n%s", want, out)
		}
	}
}

func TestVerifyDomainPending(t *testing.T) {
	writes := newFakeDomainAPI(t)
	res, _, _ := handleVerifyDomain(context.Background(), httpRequest("Bearer tok"),
		verifyDomainArgs{Workspace: "ws", Project: "p", Env: "dev", Service: "api", Domain: "api.example.com"})
	out := resultText(t, res)
	if res.IsError || !strings.Contains(out, "No TXT record found") || !strings.Contains(out, "call verify-domain again") {
		t.Fatalf("unexpected result: %s", out)
	}
	if len(*writes) != 1 || !strings.HasSuffix((*writes)[0], `{"domain":"api.example.com"}`) {
		t.Fatalf("writes = %v", *writes)
	}
}

// Registration infers each tool's input schema, and panics on one it cannot.
func TestDomainToolsRegister(t *testing.T) {
	s := mcp.NewServer(&mcp.Implementation{Name: "test", Version: "0"}, nil)
	RegisterBucketTools(s)
	RegisterDomainVerificationTools(s)
}
