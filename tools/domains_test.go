package tools

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	cloud "github.com/simplifyd-systems/cloud-go-sdk"
)

const domainsBase = "/v1/workspaces/ws/domains"

// fakeDomainsAPI quotes acme.com at total, serves saved as the workspace's
// registrant, and records registrations as sent.
func fakeDomainsAPI(t *testing.T, total *int64, saved *cloud.DomainContact) *[]map[string]any {
	t.Helper()
	var registered []map[string]any
	withFakeAPI(t, func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodGet && r.URL.Path == domainsBase+"/registrant":
			_ = json.NewEncoder(w).Encode(map[string]any{"registrant": saved})
		case r.Method == http.MethodGet && r.URL.Path == domainsBase+"/quote":
			_ = json.NewEncoder(w).Encode(map[string]any{"quote": cloud.DomainQuote{
				Name: "acme.com", Available: true, Years: 1, Price: *total - 1_000_000, VAT: 1_000_000, Total: *total,
			}})
		case r.Method == http.MethodPost && r.URL.Path == domainsBase:
			var in map[string]any
			_ = json.NewDecoder(r.Body).Decode(&in)
			registered = append(registered, in)
			_ = json.NewEncoder(w).Encode(map[string]any{"domain": cloud.Domain{Slug: "d1", Name: "acme.com", Status: cloud.DomainActive}})
		default:
			http.NotFound(w, r)
		}
	})
	return &registered
}

var owner = cloud.DomainContact{
	FirstName: "Ada", LastName: "Obi", Address1: "1 Marina", City: "Lagos", State: "Lagos",
	Zip: "101001", Country: "NG", Email: "ada@acme.com", Phone: "+2348012345678",
}

// Buying takes two calls, and the second must carry the total the first
// showed: a model cannot spend the wallet without having seen the price.
func TestRegisterDomainChargesOnlyAtTheConfirmedPrice(t *testing.T) {
	total := int64(161_250_000)
	registered := fakeDomainsAPI(t, &total, &owner)
	args := registerDomainArgs{Workspace: "ws", Name: "acme.com"}

	result, _, _ := handleRegisterDomain(context.Background(), nil, args)
	out := resultText(t, result)
	if len(*registered) != 0 {
		t.Fatal("charged on the first call")
	}
	for _, want := range []string{"₦16,125.00", "confirm_total=161250000", "Nothing has been charged",
		"Ada Obi <ada@acme.com>", "1 Marina, Lagos", "settings/domains?return=acme.com"} {
		if !strings.Contains(out, want) {
			t.Errorf("first call does not mention %q:\n%s", want, out)
		}
	}

	// The price moves before the person agrees: the old total is refused.
	total = 170_000_000
	args.ConfirmTotal = 161_250_000
	result, _, _ = handleRegisterDomain(context.Background(), nil, args)
	if out := resultText(t, result); !strings.Contains(out, "price has changed") || len(*registered) != 0 {
		t.Fatalf("a stale total must not charge: %s", out)
	}

	args.ConfirmTotal = 170_000_000
	result, _, _ = handleRegisterDomain(context.Background(), nil, args)
	if result.IsError || len(*registered) != 1 {
		t.Fatalf("confirmed call did not register: %s", resultText(t, result))
	}
	// No registrant is sent: the API registers it to the saved one.
	if _, sent := (*registered)[0]["registrant"]; sent {
		t.Errorf("sent a registrant: %v", (*registered)[0])
	}
}

// Without a saved registrant there is nothing to buy as: the person is sent
// to settings, and nothing is charged even with a confirmed total.
func TestRegisterDomainSendsThePersonToSettings(t *testing.T) {
	total := int64(161_250_000)
	registered := fakeDomainsAPI(t, &total, nil)

	result, _, _ := handleRegisterDomain(context.Background(), nil, registerDomainArgs{
		Workspace: "ws", Name: "acme.com", ConfirmTotal: total,
	})
	out := resultText(t, result)
	if !result.IsError || !strings.Contains(out, "https://console.cloud.simplifyd.com/project/settings/domains?return=acme.com") || len(*registered) != 0 {
		t.Fatalf("want directions to settings and no charge: %s", out)
	}
}

func TestRenewDomainNeedsConfirmation(t *testing.T) {
	renewed := 0
	withFakeAPI(t, func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodGet && r.URL.Path == domainsBase:
			_ = json.NewEncoder(w).Encode(map[string]any{"domains": []cloud.Domain{{Slug: "d1", Name: "acme.com", AutoRenew: true}}})
		case r.Method == http.MethodPost && r.URL.Path == domainsBase+"/d1/renew":
			renewed++
			_ = json.NewEncoder(w).Encode(map[string]any{"domain": cloud.Domain{Slug: "d1", Name: "acme.com"}})
		default:
			http.NotFound(w, r)
		}
	})
	args := renewDomainArgs{Workspace: "ws", Domain: "acme.com"}
	result, _, _ := handleRenewDomain(context.Background(), nil, args)
	if out := resultText(t, result); renewed != 0 || !strings.Contains(out, "confirm=true") {
		t.Fatalf("renewed without confirmation: %s", out)
	}
	args.Confirm = true
	if result, _, _ = handleRenewDomain(context.Background(), nil, args); renewed != 1 {
		t.Fatalf("confirmed renewal did not happen: %s", resultText(t, result))
	}
}

// The API's refusals are written for the customer; they must reach the model
// so it can correct the call or tell the person what to do.
func TestDomainErrorsCarryTheAPIsReason(t *testing.T) {
	withFakeAPI(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		_ = json.NewEncoder(w).Encode(map[string]any{"message": "a domain can be registered for 1 to 10 years"})
	})
	result, _, _ := handleQuoteDomain(context.Background(), nil, quoteDomainArgs{Workspace: "ws", Name: "acme.com", Years: 20})
	if out := resultText(t, result); !result.IsError || !strings.Contains(out, "1 to 10 years") {
		t.Fatalf("got %s", out)
	}
}

// Registration infers each tool's input schema, and panics on one it cannot.
func TestDomainAndDNSToolsRegister(t *testing.T) {
	s := mcp.NewServer(&mcp.Implementation{Name: "test", Version: "0"}, nil)
	RegisterDomainTools(s)
	RegisterDNSTools(s)
}
