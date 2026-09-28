package tools

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	cloud "github.com/simplifyd-systems/cloud-go-sdk"
)

// withFakeAPI points the process-wide client at a test server for the length
// of one test, the way the stdio transport would with a real token.
func withFakeAPI(t *testing.T, handler http.HandlerFunc) {
	t.Helper()
	server := httptest.NewServer(handler)
	sdkMu.Lock()
	prevClient, prevToken := sdkClient, sdkToken
	sdkClient = cloud.NewClient(cloud.WithBaseURL(server.URL), cloud.WithToken("test"))
	sdkToken = "test"
	sdkMu.Unlock()
	t.Cleanup(func() {
		server.Close()
		sdkMu.Lock()
		sdkClient, sdkToken = prevClient, prevToken
		sdkMu.Unlock()
	})
}

const emailBase = "/v1/workspaces/ws/projects/p/envs/prod/svcs/mail/email"

func TestAddEmailDomainListsTheRecordsToPublish(t *testing.T) {
	withFakeAPI(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != emailBase+"/domains" {
			http.NotFound(w, r)
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"domain": cloud.EmailDomain{
			ID: "d1", Domain: "mail.acme.com", Status: "pending",
			Records: []cloud.EmailDNSRecord{
				{Purpose: "dkim", Type: "TXT", Name: "smail._domainkey.mail.acme.com", Value: "v=DKIM1; p=abc"},
				{Purpose: "spf", Type: "TXT", Name: "mail.acme.com", Value: "v=spf1 include:why.email ~all"},
			},
		}})
	})

	result, _, _ := handleAddEmailDomain(context.Background(), nil, emailDomainArgs{
		Workspace: "ws", Project: "p", Env: "prod", Service: "mail", Domain: "mail.acme.com",
	})
	out := resultText(t, result)
	// The records are the one thing the person has to act on, so every one of
	// them has to reach the conversation whole.
	for _, want := range []string{"d1", "smail._domainkey.mail.acme.com", "v=DKIM1; p=abc", "v=spf1 include:why.email ~all", "DNS provider"} {
		if !strings.Contains(out, want) {
			t.Errorf("result does not mention %q:\n%s", want, out)
		}
	}
}

func TestVerifyEmailDomainSaysWhetherItCanSend(t *testing.T) {
	status := "pending"
	withFakeAPI(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != emailBase+"/domains/d1/verify" {
			http.NotFound(w, r)
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"domain": cloud.EmailDomain{ID: "d1", Domain: "mail.acme.com", Status: status}})
	})
	args := emailDomainArgs{Workspace: "ws", Project: "p", Env: "prod", Service: "mail", Domain: "d1"}

	result, _, _ := handleVerifyEmailDomain(context.Background(), nil, args)
	if out := resultText(t, result); !strings.Contains(out, "still pending") {
		t.Errorf("a pending domain should be reported as pending: %s", out)
	}

	status = "verified"
	result, _, _ = handleVerifyEmailDomain(context.Background(), nil, args)
	if out := resultText(t, result); !strings.Contains(out, "is verified") || !strings.Contains(out, "SMTP_PASSWORD") {
		t.Errorf("a verified domain should say how to send: %s", out)
	}
}

func TestCreateServiceMakesAnEmailService(t *testing.T) {
	var body map[string]any
	withFakeAPI(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/v1/workspaces/ws/projects/p/envs/prod/svcs" {
			http.NotFound(w, r)
			return
		}
		_ = json.NewDecoder(r.Body).Decode(&body)
		_ = json.NewEncoder(w).Encode(cloud.Service{Slug: "mail", Name: "mail", Type: cloud.ServiceTypeEmail})
	})

	result, _, _ := handleCreateService(context.Background(), nil, createServiceArgs{
		Workspace: "ws", Project: "p", Env: "prod", Name: "mail", Type: "email",
	})
	if result.IsError {
		t.Fatalf("create failed: %s", resultText(t, result))
	}
	if body["type"] != "email" {
		t.Errorf("type = %v, want email", body["type"])
	}
	if svc, ok := body["email_svc"].(map[string]any); !ok || svc["name"] != "mail" {
		t.Errorf("email_svc = %v, want the service's name", body["email_svc"])
	}
}
