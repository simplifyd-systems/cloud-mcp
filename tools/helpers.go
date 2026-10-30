// Package tools implements MCP tool handlers for the Simplifyd Cloud API.
package tools

import (
	"bytes"
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

	"github.com/simplifyd-systems/cloud-mcp/client"
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

// Version is the server version reported to the API in the User-Agent. It is
// overwritten by SetVersion at startup; the default matters only for a build
// that never calls it.
var Version = "dev"

// SetVersion records the running server's version and rebuilds the
// process-wide client so it identifies itself correctly.
//
// The rebuild is the point: package initialisation runs before main, so the
// client built in init() already exists by the time main knows its own version,
// and setting the string alone would leave the stdio client reporting "dev"
// forever.
func SetVersion(version string) {
	sdkMu.Lock()
	defer sdkMu.Unlock()
	Version = version
	sdkClient = newSDKClient(sdkToken)
}

func newSDKClient(token string) *cloud.Client {
	return cloud.NewClient(
		cloud.WithToken(token),
		cloud.WithBaseURL(client.BaseURL()),
		// Without this the SDK's default names the CLI, so MCP traffic would be
		// listed as a command line in the user's session list.
		cloud.WithUserAgent("edge-mcp/"+Version),
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

// isRemote reports whether a call arrived over HTTP, where the server is the
// hosted one rather than a process on the caller's own machine.
func isRemote(req *mcp.CallToolRequest) bool {
	return req != nil && req.Extra != nil
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

// jsonText marshals v to indented JSON and returns it as a text result, with
// sensitive fields redacted and empty ones dropped.
func jsonText(v any) *mcp.CallToolResult {
	return encodeResult(v, true)
}

// jsonTextRaw is jsonText without the redaction. It is reserved for tools whose
// sole purpose is to return something redaction would hide: a newly created
// credential, a file's contents, or DNS records the user must publish.
func jsonTextRaw(v any) *mcp.CallToolResult {
	return encodeResult(v, false)
}

// nothingToReport is returned for a call that succeeded but whose response
// carried nothing: several API writes answer with no more than a success flag,
// and the SDK decodes that into a zero struct. Shown as is, that reads as a
// tool returning an empty object, which is indistinguishable from a broken one.
// addTool replaces it with a line naming the tool.
var nothingToReport = text("Done.")

func encodeResult(v any, redact bool) *mcp.CallToolResult {
	data, err := json.Marshal(v)
	if err != nil {
		return toolError("failed to encode response")
	}
	var public any
	if err := json.Unmarshal(data, &public); err != nil {
		return toolError("failed to encode response")
	}
	if redact {
		redactSensitiveFields(public)
	}
	public = tidy(public)
	if public == nil {
		return text("No results.")
	}
	if object, ok := public.(map[string]any); ok && allZero(object) {
		return nothingToReport
	}

	var buf bytes.Buffer
	encoder := json.NewEncoder(&buf)
	// Config files and static sites are HTML and templates; escaping < and >
	// as \u003c makes them unreadable for no benefit outside a browser.
	encoder.SetEscapeHTML(false)
	encoder.SetIndent("", "  ")
	if err := encoder.Encode(public); err != nil {
		return toolError("failed to encode response")
	}
	return text(strings.TrimSuffix(buf.String(), "\n"))
}

// zeroTime is how the API spells a timestamp it did not fill in.
const zeroTime = "0001-01-01T00:00:00Z"

// tidy drops what carries no information from a decoded response: nulls, empty
// strings, unset timestamps, and the per-type blocks of a service ("kafka_svc",
// "email_svc", ...) that are all zero because the service is of another type.
// Anthropic's directory review rejects responses padded beyond the task, and a
// service listed with seven empty blocks is mostly padding.
//
// Zero numbers and false are kept elsewhere: "replicas": 0 and
// "has_verified_domain": false are answers, not padding.
func tidy(v any) any {
	switch value := v.(type) {
	case map[string]any:
		for key, child := range value {
			child = tidy(child)
			switch {
			case child == nil, child == "", child == zeroTime:
				delete(value, key)
			case strings.HasSuffix(key, "_svc") && allZero(child):
				delete(value, key)
			default:
				value[key] = child
			}
		}
		return value
	case []any:
		for i, child := range value {
			value[i] = tidy(child)
		}
		return value
	}
	return v
}

// allZero reports whether v holds no information: every leaf is null, an empty
// string, false or zero.
func allZero(v any) bool {
	switch value := v.(type) {
	case nil:
		return true
	case string:
		return value == "" || value == zeroTime
	case bool:
		return !value
	case float64:
		return value == 0
	case map[string]any:
		for _, child := range value {
			if !allZero(child) {
				return false
			}
		}
		return true
	case []any:
		return len(value) == 0
	}
	return false
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

// authChallenge is the WWW-Authenticate value attached to a tool result when
// the API rejects the caller's token. Empty over stdio, where there is no
// OAuth flow to restart.
var authChallenge string

// SetAuthChallenge sets the challenge apiErr attaches to authentication
// failures. The HTTP transport calls it at startup.
func SetAuthChallenge(challenge string) {
	authChallenge = challenge
}

// reauthenticate marks a tool result as an authentication failure the client
// can fix by signing in again. ChatGPT shows its reconnect prompt only for a
// result carrying this _meta key; other clients ignore it.
func reauthenticate(result *mcp.CallToolResult) *mcp.CallToolResult {
	if authChallenge != "" {
		result.Meta = mcp.Meta{"mcp/www_authenticate": []string{authChallenge}}
	}
	return result
}

// apiErr formats an API error for display.
func apiErr(op string, err error) *mcp.CallToolResult {
	requestID := newRequestID()
	var apiError *cloud.APIError
	if errors.As(err, &apiError) {
		log.Printf("operation failed request_id=%s operation=%q status=%d", requestID, op, apiError.StatusCode)
		// A 4xx is the caller's to fix, and the API says how in words written
		// for the customer: "port 80 is already exposed as TCP", "configure a
		// backup destination first". Without them the model can neither correct
		// the call nor tell the person why it failed, and Anthropic's review
		// fails tools that answer with a bare "request rejected". A 5xx or a
		// transport error stays generic: those messages can carry internals.
		if message := customerMessage(apiError); message != "" {
			return errResult("%s: %s (reference %s)", op, message, requestID)
		}
		switch apiError.StatusCode {
		case http.StatusBadRequest, http.StatusUnprocessableEntity:
			return errResult("%s: request rejected (reference %s)", op, requestID)
		case http.StatusUnauthorized:
			return reauthenticate(errResult("%s: authentication required (reference %s)", op, requestID))
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

// customerMessage returns the API's explanation of a refusal, or "" when there
// is none fit to show. The SDK fills Message with "HTTP <status>" when the body
// had no message of its own, which says nothing the status does not.
func customerMessage(apiError *cloud.APIError) string {
	switch apiError.StatusCode {
	case http.StatusBadRequest, http.StatusForbidden, http.StatusNotFound,
		http.StatusConflict, http.StatusUnprocessableEntity:
	default:
		return ""
	}
	message := strings.TrimSpace(apiError.Message)
	if message == "" || strings.HasPrefix(message, "HTTP ") {
		return ""
	}
	return message
}

func newRequestID() string {
	var b [6]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "unavailable"
	}
	return fmt.Sprintf("%x", b[:])
}
