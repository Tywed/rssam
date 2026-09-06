package httpserver

import (
	"encoding/json"
	"go/ast"
	"go/parser"
	"go/token"
	"net/http"
	"net/http/httptest"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"testing"
)

// openAPIOps returns "METHOD /path" for every operation in openapi.json.
func openAPIOps(t *testing.T) (map[string]map[string]any, []string) {
	t.Helper()
	var doc struct {
		Paths map[string]map[string]json.RawMessage `json:"paths"`
	}
	if err := json.Unmarshal(openAPISpec, &doc); err != nil {
		t.Fatalf("openapi.json is not valid JSON: %v", err)
	}
	ops := map[string]map[string]any{}
	var keys []string
	for p, item := range doc.Paths {
		for method, raw := range item {
			switch method {
			case "get", "post", "put", "delete", "patch", "head", "options":
			default:
				continue // parameters, summary, ...
			}
			var op map[string]any
			if err := json.Unmarshal(raw, &op); err != nil {
				t.Fatalf("%s %s: %v", method, p, err)
			}
			k := strings.ToUpper(method) + " " + p
			ops[k] = op
			keys = append(keys, k)
		}
	}
	sort.Strings(keys)
	return ops, keys
}

// routerOps extracts every "METHOD /path" registered in Server.Handler() by
// reading server.go, so the comparison cannot drift when routes are added.
func routerOps(t *testing.T) []string {
	t.Helper()
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, "server.go", nil, 0)
	if err != nil {
		t.Fatal(err)
	}
	var out []string
	ast.Inspect(f, func(n ast.Node) bool {
		call, ok := n.(*ast.CallExpr)
		if !ok || len(call.Args) < 1 {
			return true
		}
		sel, ok := call.Fun.(*ast.SelectorExpr)
		if !ok || (sel.Sel.Name != "HandleFunc" && sel.Sel.Name != "Handle") {
			return true
		}
		lit, ok := call.Args[0].(*ast.BasicLit)
		if !ok || lit.Kind != token.STRING {
			return true
		}
		pattern, _ := strconv.Unquote(lit.Value)
		if strings.HasSuffix(pattern, "/") && !strings.Contains(pattern, " ") {
			return true // sub-mux mounts like "/v1/"
		}
		if !strings.Contains(pattern, " ") {
			pattern = "GET " + pattern // method-less registrations answer GET
		}
		out = append(out, pattern)
		return true
	})
	sort.Strings(out)
	return out
}

// Every route in the router is documented and every documented operation is
// routed. The UI (/ui/*) is deliberately out of scope: it is HTML, not API.
func TestOpenAPI_MatchesRouter(t *testing.T) {
	_, specKeys := openAPIOps(t)
	routes := routerOps(t)

	spec := map[string]bool{}
	for _, k := range specKeys {
		spec[k] = true
	}
	routed := map[string]bool{}
	for _, k := range routes {
		routed[k] = true
	}
	for _, k := range routes {
		if !spec[k] {
			t.Errorf("route %q is registered in Handler() but missing from openapi.json", k)
		}
	}
	for _, k := range specKeys {
		if !routed[k] {
			t.Errorf("openapi.json documents %q but Handler() does not route it", k)
		}
	}
}

// Every documented /v1 operation actually reaches wrapAPI (401 for anonymous
// callers) instead of falling through the mux as 404/405 - this catches path
// parameter names or methods that exist in the spec but not in the router,
// which the source-level comparison above would miss if a pattern was
// registered on a different mux.
func TestOpenAPI_OperationsAreReachable(t *testing.T) {
	ops, keys := openAPIOps(t)
	env := newRouterEnv(t, nil)
	param := regexp.MustCompile(`\{[^}]+}`)
	for _, k := range keys {
		method, path, _ := strings.Cut(k, " ")
		concrete := param.ReplaceAllString(path, "1")
		rec := env.do(method, concrete, "", "")
		switch {
		case strings.HasPrefix(path, "/v1/"):
			if rec.Code != http.StatusUnauthorized {
				t.Errorf("%s: anonymous request got %d, want 401 (route not reachable?)", k, rec.Code)
			}
		default:
			if rec.Code == http.StatusNotFound && path != "/ws/v1" {
				t.Errorf("%s: 404 from router", k)
			}
		}
		// Every documented error code an anonymous /v1 call can hit is declared.
		if strings.HasPrefix(path, "/v1/") {
			resp, _ := ops[k]["responses"].(map[string]any)
			if _, ok := resp["401"]; !ok {
				t.Errorf("%s: missing 401 response in spec", k)
			}
		}
	}
}

// The spec is served verbatim and is valid JSON with the paths block.
func TestOpenAPI_Served(t *testing.T) {
	s := New(Dependencies{})
	rec := httptest.NewRecorder()
	s.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/openapi.json", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("status %d", rec.Code)
	}
	if ct := rec.Header().Get("Content-Type"); !strings.HasPrefix(ct, "application/json") {
		t.Fatalf("content-type %q", ct)
	}
	var doc map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &doc); err != nil {
		t.Fatal(err)
	}
	if doc["openapi"] != "3.0.3" {
		t.Fatalf("openapi version %v", doc["openapi"])
	}
}

var rbacProbeBodies = map[string]string{
	"/v1/users":                            `{"username":"x","password":"Str0ng-Passw0rd!"}`,
	"/v1/me":                               `{"current_password":"a","password":"b"}`,
	"/v1/me/api-keys":                      `{"name":"n"}`,
	"/v1/categories":                       `{"title":"t"}`,
	"/v1/categories/{id}":                  `{"title":"t"}`,
	"/v1/feeds":                            `{"feed_url":"http://e.test/f"}`,
	"/v1/feeds/{id}":                       `{"feed_url":"http://e.test/f"}`,
	"/v1/entries":                          `{"entry_ids":[1],"status":"read"}`,
	"/v1/feeds/{feedID}/entries/{entryID}": `{"status":"read"}`,
	"/v1/filters":                          `{"name":"f"}`,
	"/v1/filters/{id}":                     `{"name":"f"}`,
	"/v1/filters/{id}/test":                `{"text":"x"}`,
	"/v1/webhooks":                         `{"name":"w","url":"http://e.test/h"}`,
	"/v1/webhooks/{id}":                    `{"name":"w"}`,
}

// Admin-only handlers are documented as such: a non-admin principal gets 403
// exactly on the operations whose description says "Requires an admin
// principal", and 2xx/4xx-other elsewhere.
func TestOpenAPI_AdminMarkersMatchRBAC(t *testing.T) {
	ops, keys := openAPIOps(t)
	env := newRouterEnv(t, nil)
	param := regexp.MustCompile(`\{[^}]+}`)
	for _, k := range keys {
		method, path, _ := strings.Cut(k, " ")
		if !strings.HasPrefix(path, "/v1/") {
			continue
		}
		desc, _ := ops[k]["description"].(string)
		adminDocumented := strings.Contains(desc, "Requires an admin principal")
		if k == "PUT /v1/me" {
			// 403 here means "current_password is incorrect", not RBAC.
			continue
		}
		// Some handlers decode the body (DisallowUnknownFields) before the
		// admin check; send a valid one so 400 cannot mask the RBAC verdict.
		body := ""
		if method == http.MethodPost || method == http.MethodPut {
			body = rbacProbeBodies[path]
		}
		rec := env.do(method, param.ReplaceAllString(path, "1"), env.bobKey, body)
		gotForbidden := rec.Code == http.StatusForbidden
		if adminDocumented != gotForbidden {
			t.Errorf("%s: spec admin=%v but non-admin got %d (%s)", k, adminDocumented, rec.Code, strings.TrimSpace(rec.Body.String()))
		}
	}
}
