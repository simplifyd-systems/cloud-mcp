// Package tools implements MCP tool handlers for the Simplifyd Cloud API.
package tools

import (
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	cloud "github.com/simplifyd-systems/cloud-go-sdk"

	"github.com/simplifyd-com/cloud-mcp/client"
)

// Process-wide auth state. This is the stdio identity only: a single local
// user running the binary as a subprocess. It is never read or written on the
// HTTP path, where each caller supplies their own token per request.
var (
	sdkMu     sync.RWMutex
	sdkClient *cloud.Client
	sdkToken  string
)

// sharedTransport is reused by every SDK client so that per-request clients on
// the HTTP path still pool connections to the API instead of renegotiating TLS
// on every tool call. cloud.NewClient would otherwise build a fresh
// http.Client, and therefore a fresh connection pool, for each caller.
var sharedTransport http.RoundTripper = func() http.RoundTripper {
	t := http.DefaultTransport.(*http.Transport).Clone()
	t.MaxIdleConnsPerHost = 32
	return t
}()

func init() {
	sdkToken = client.ResolveToken()
	sdkClient = newSDKClient(sdkToken)
}

func newSDKClient(token string) *cloud.Client {
	return cloud.NewClient(
		cloud.WithToken(token),
		cloud.WithBaseURL(client.BaseURL()),
		cloud.WithHTTPClient(&http.Client{
			Timeout:   30 * time.Second,
			Transport: sharedTransport,
		}),
	)
}

// setToken swaps the process-wide SDK client for one authenticated with the
// given token (SDK clients are immutable once constructed). Only the stdio
// transport calls this — see RegisterAuthTools.
func setToken(token string) {
	sdkMu.Lock()
	defer sdkMu.Unlock()
	sdkToken = token
	sdkClient = newSDKClient(token)
}

// localClient returns the process-wide client and whether a token is set.
func localClient() (*cloud.Client, bool) {
	sdkMu.RLock()
	defer sdkMu.RUnlock()
	return sdkClient, sdkToken != ""
}

// bearerToken extracts the token from an Authorization header value,
// tolerating any capitalisation of the scheme.
func bearerToken(header string) string {
	const prefix = "bearer "
	if len(header) > len(prefix) && strings.EqualFold(header[:len(prefix)], prefix) {
		return strings.TrimSpace(header[len(prefix):])
	}
	return ""
}

// sdkFor resolves the API client for a single tool call, and replaces the old
// requireAuth+sdk pair.
//
// Over HTTP, req.Extra is non-nil and carries the caller's headers: the token
// is taken from Authorization on every call, so concurrent users of a hosted
// server never observe each other's credentials. Over stdio, Extra is nil and
// we fall back to the process-wide token resolved from SIMPLIFYD_API_TOKEN,
// ~/.simplifyd/config.json, or the login tool.
//
// The returned result is non-nil exactly when ok is false, and should be
// returned to the caller as the tool's error result.
func sdkFor(req *mcp.CallToolRequest) (*cloud.Client, *mcp.CallToolResult, bool) {
	if req != nil && req.Extra != nil {
		token := bearerToken(req.Extra.Header.Get("Authorization"))
		if token == "" {
			return nil, toolError(
				"not authenticated — send a Simplifyd API token as an " +
					"Authorization: Bearer <token> header",
			), false
		}
		return newSDKClient(token), nil, true
	}
	api, ok := localClient()
	if !ok {
		return nil, toolError(
			"not authenticated — set SIMPLIFYD_API_TOKEN or run the login tool first",
		), false
	}
	return api, nil, true
}

// text returns a successful CallToolResult containing a plain-text message.
func text(s string) *mcp.CallToolResult {
	return &mcp.CallToolResult{
		Content: []mcp.Content{&mcp.TextContent{Text: s}},
	}
}

// toolError returns an error CallToolResult with the given message.
func toolError(msg string) *mcp.CallToolResult {
	return &mcp.CallToolResult{
		IsError: true,
		Content: []mcp.Content{&mcp.TextContent{Text: msg}},
	}
}

// errResult formats an error and returns it as a tool error result.
func errResult(format string, args ...any) *mcp.CallToolResult {
	return toolError(fmt.Sprintf(format, args...))
}

// jsonText marshals v to indented JSON and returns it as a text result.
func jsonText(v any) *mcp.CallToolResult {
	data, err := json.Marshal(v)
	if err != nil {
		return toolError("failed to encode response")
	}
	var public any
	if err := json.Unmarshal(data, &public); err != nil {
		return toolError("failed to encode response")
	}
	redactSensitiveFields(public)
	return jsonTextRaw(public)
}

// jsonTextRaw is reserved for tools whose sole purpose is explicit, documented
// disclosure of a newly-created or requested credential.
func jsonTextRaw(v any) *mcp.CallToolResult {
	data, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return toolError("failed to encode response")
	}
	return text(string(data))
}

var sensitiveResponseFields = map[string]string{
	"value":             "has_value",
	"content":           "has_content",
	"key":               "has_key",
	"password":          "has_password",
	"cred":              "has_credential",
	"connection_url":    "has_connection_url",
	"registry_password": "has_registry_password",
}

func redactSensitiveFields(v any) {
	switch value := v.(type) {
	case map[string]any:
		for key, child := range value {
			if indicator, sensitive := sensitiveResponseFields[key]; sensitive {
				if child != nil && child != "" {
					value[indicator] = true
				}
				delete(value, key)
				continue
			}
			redactSensitiveFields(child)
		}
	case []any:
		for _, child := range value {
			redactSensitiveFields(child)
		}
	}
}

// apiErr formats an API error for display.
func apiErr(op string, err error) *mcp.CallToolResult {
	requestID := newRequestID()
	var apiError *cloud.APIError
	if errors.As(err, &apiError) {
		log.Printf("operation failed request_id=%s operation=%q status=%d", requestID, op, apiError.StatusCode)
		switch apiError.StatusCode {
		case http.StatusBadRequest, http.StatusUnprocessableEntity:
			return errResult("%s: request rejected (reference %s)", op, requestID)
		case http.StatusUnauthorized:
			return errResult("%s: authentication required (reference %s)", op, requestID)
		case http.StatusForbidden:
			return errResult("%s: permission denied (reference %s)", op, requestID)
		case http.StatusNotFound:
			return errResult("%s: resource not found (reference %s)", op, requestID)
		case http.StatusConflict:
			return errResult("%s: request conflicts with current state (reference %s)", op, requestID)
		case http.StatusTooManyRequests:
			return errResult("%s: rate limit exceeded; try again later (reference %s)", op, requestID)
		}
	}
	log.Printf("operation failed request_id=%s operation=%q error_type=%T", requestID, op, err)
	return errResult("%s: request failed (reference %s)", op, requestID)
}

func newRequestID() string {
	var b [6]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "unavailable"
	}
	return fmt.Sprintf("%x", b[:])
}
