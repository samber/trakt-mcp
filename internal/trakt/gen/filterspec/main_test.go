package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// fixtureSpec is a three-path OpenAPI 3.0 document. Only "GET /keep" is
// listed in the tests' endpoints: Kept references KeptChild transitively,
// Dropped is referenced only by the unlisted paths, Orphan by nothing.
const fixtureSpec = `{
  "openapi": "3.0.2",
  "info": {"title": "Fixture", "version": "1.0.0"},
  "servers": [
    {"url": "https://api.trakt.tv", "description": "Production"},
    {"url": "https://apiz.trakt.tv", "description": "Private production"}
  ],
  "tags": [{"name": "keep"}, {"name": "drop"}],
  "paths": {
    "/keep": {
      "parameters": [{"$ref": "#/components/parameters/Page"}],
      "get": {
        "operationId": "getKeep",
        "tags": ["keep"],
        "responses": {"200": {"description": "ok", "content": {"application/json": {"schema": {"$ref": "#/components/schemas/Kept"}}}}}
      },
      "delete": {
        "operationId": "deleteKeep",
        "tags": ["drop"],
        "responses": {"204": {"description": "gone", "content": {"application/json": {"schema": {"$ref": "#/components/schemas/Dropped"}}}}}
      }
    },
    "/drop": {
      "get": {
        "operationId": "getDrop",
        "tags": ["drop"],
        "responses": {"200": {"description": "ok", "content": {"application/json": {"schema": {"$ref": "#/components/schemas/Dropped"}}}}}
      }
    },
    "/other": {
      "post": {
        "operationId": "postOther",
        "responses": {"201": {"description": "ok"}}
      }
    }
  },
  "components": {
    "parameters": {
      "Page": {"name": "page", "in": "query", "schema": {"type": "integer"}},
      "Unused": {"name": "unused", "in": "query", "schema": {"type": "string"}}
    },
    "schemas": {
      "Kept": {"type": "object", "properties": {"child": {"$ref": "#/components/schemas/KeptChild"}}},
      "KeptChild": {"type": "object", "properties": {"name": {"type": "string"}}},
      "Dropped": {"type": "object", "properties": {"x": {"type": "string"}}},
      "Orphan": {"type": "string"}
    },
    "securitySchemes": {
      "traktAPI": {"type": "apiKey", "in": "header", "name": "trakt-api-key"},
      "traktOAuth": {"type": "oauth2", "flows": {"authorizationCode": {"authorizationUrl": "https://auth.trakt.tv/oauth/authorize", "tokenUrl": "https://auth.trakt.tv/oauth/token", "scopes": {}}}}
    }
  }
}`

func loadFixture(t *testing.T) map[string]any {
	t.Helper()

	var spec map[string]any
	if err := json.Unmarshal([]byte(fixtureSpec), &spec); err != nil {
		t.Fatalf("fixture: %v", err)
	}

	return spec
}

func filterFixture(t *testing.T) map[string]any {
	t.Helper()

	endpoints, err := parseEndpoints(strings.NewReader("GET /keep\n"))
	if err != nil {
		t.Fatalf("parseEndpoints: %v", err)
	}

	out, err := filterSpec(loadFixture(t), endpoints)
	if err != nil {
		t.Fatalf("filterSpec: %v", err)
	}

	return out
}

func asMap(t *testing.T, v any, what string) map[string]any {
	t.Helper()

	m, ok := v.(map[string]any)
	if !ok {
		t.Fatalf("%s: got %T, want object", what, v)
	}

	return m
}

func TestFilterKeepsOnlyListedEndpoints(t *testing.T) {
	t.Parallel()
	out := filterFixture(t)

	paths := asMap(t, out["paths"], "paths")
	if len(paths) != 1 {
		t.Fatalf("paths: got %d entries, want 1: %v", len(paths), paths)
	}

	keep := asMap(t, paths["/keep"], "/keep")
	if _, ok := keep["get"]; !ok {
		t.Error("GET /keep was dropped")
	}

	if _, ok := keep["delete"]; ok {
		t.Error("DELETE /keep was kept but is not listed")
	}

	if _, ok := keep["parameters"]; !ok {
		t.Error("path-level parameters of /keep were dropped")
	}

	servers, ok := out["servers"].([]any)
	if !ok || len(servers) != 1 || asMap(t, servers[0], "server")["url"] != productionServer {
		t.Errorf("servers: got %v, want only %s", out["servers"], productionServer)
	}

	tags, ok := out["tags"].([]any)
	if !ok || len(tags) != 1 || asMap(t, tags[0], "tag")["name"] != "keep" {
		t.Errorf("tags: got %v, want only keep", out["tags"])
	}
}

func TestFilterDropsUnreferencedSchemas(t *testing.T) {
	t.Parallel()
	out := filterFixture(t)

	components := asMap(t, out["components"], "components")

	schemas := asMap(t, components["schemas"], "schemas")
	for _, name := range []string{"Kept", "KeptChild"} {
		if _, ok := schemas[name]; !ok {
			t.Errorf("schema %s was dropped but is referenced", name)
		}
	}

	for _, name := range []string{"Dropped", "Orphan"} {
		if _, ok := schemas[name]; ok {
			t.Errorf("schema %s was kept but is unreferenced", name)
		}
	}

	params := asMap(t, components["parameters"], "parameters")
	if _, ok := params["Page"]; !ok {
		t.Error("parameter Page was dropped but is referenced")
	}

	if _, ok := params["Unused"]; ok {
		t.Error("parameter Unused was kept but is unreferenced")
	}

	security := asMap(t, components["securitySchemes"], "securitySchemes")
	for _, name := range []string{"traktAPI", "traktOAuth"} {
		if _, ok := security[name]; !ok {
			t.Errorf("security scheme %s was dropped", name)
		}
	}
}

func TestFilterRejectsUnknownEndpoint(t *testing.T) {
	t.Parallel()

	endpoints, err := parseEndpoints(strings.NewReader("GET /missing\n"))
	if err != nil {
		t.Fatalf("parseEndpoints: %v", err)
	}

	if _, err := filterSpec(loadFixture(t), endpoints); err == nil {
		t.Fatal("filterSpec accepted an endpoint absent from the spec")
	}
}

func TestParseEndpoints(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		input   string
		want    int
		wantErr bool
	}{
		{name: "two lines and blanks", input: "GET /a\n\n  POST /b  \n", want: 2},
		{name: "lowercase method", input: "get /a\n", wantErr: true},
		{name: "missing path", input: "GET\n", wantErr: true},
		{name: "path without slash", input: "GET a\n", wantErr: true},
		{name: "unknown method", input: "FETCH /a\n", wantErr: true},
		{name: "duplicate", input: "GET /a\nGET /a\n", wantErr: true},
		{name: "empty", input: "", wantErr: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			got, err := parseEndpoints(strings.NewReader(tt.input))
			if tt.wantErr {
				if err == nil {
					t.Fatalf("got %v, want error", got)
				}

				return
			}

			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}

			if len(got) != tt.want {
				t.Fatalf("got %d endpoints, want %d", len(got), tt.want)
			}
		})
	}
}

func TestDowngradeOpenAPI31(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		input string
		want  string
	}{
		{
			name:  "nullable type array",
			input: `{"type": ["string", "null"]}`,
			want:  `{"nullable": true, "type": "string"}`,
		},
		{
			name:  "null only type array",
			input: `{"type": ["null"]}`,
			want:  `{"nullable": true}`,
		},
		{
			name:  "single type array",
			input: `{"type": ["integer"]}`,
			want:  `{"type": "integer"}`,
		},
		{
			name:  "numeric exclusive bounds",
			input: `{"type": "integer", "exclusiveMinimum": 0, "exclusiveMaximum": 11}`,
			want:  `{"type": "integer", "minimum": 0, "exclusiveMinimum": true, "maximum": 11, "exclusiveMaximum": true}`,
		},
		{
			name:  "const becomes enum",
			input: `{"type": "string", "const": "movie"}`,
			want:  `{"type": "string", "enum": ["movie"]}`,
		},
		{
			name:  "schema examples become example",
			input: `{"type": "string", "examples": ["a", "b"]}`,
			want:  `{"type": "string", "example": "a"}`,
		},
		{
			name:  "nested in properties",
			input: `{"properties": {"x": {"type": ["number", "null"]}}}`,
			want:  `{"properties": {"x": {"nullable": true, "type": "number"}}}`,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			var in, want any
			if err := json.Unmarshal([]byte(tt.input), &in); err != nil {
				t.Fatal(err)
			}

			if err := json.Unmarshal([]byte(tt.want), &want); err != nil {
				t.Fatal(err)
			}

			got := downgradeSchemas(in)
			gotJSON, _ := json.Marshal(got)

			wantJSON, _ := json.Marshal(want)
			if string(gotJSON) != string(wantJSON) {
				t.Fatalf("got %s, want %s", gotJSON, wantJSON)
			}
		})
	}
}

func TestDowngradeKeepsMediaTypeExamples(t *testing.T) {
	t.Parallel()
	// Media-type "examples" maps are valid OpenAPI 3.0 and must survive.
	input := `{"content": {"application/json": {"schema": {"type": "string"}, "examples": {"one": {"value": "a"}}}}}`

	var in any
	if err := json.Unmarshal([]byte(input), &in); err != nil {
		t.Fatal(err)
	}

	gotJSON, _ := json.Marshal(downgradeSchemas(in))
	if !strings.Contains(string(gotJSON), `"examples":{"one"`) {
		t.Fatalf("media-type examples were rewritten: %s", gotJSON)
	}
}

func TestDowngradeKeepsPropertiesNamedLikeKeywords(t *testing.T) {
	t.Parallel()
	// Trakt objects have fields called "type"; property names must never be
	// read as schema keywords, while their own schemas are still rewritten.
	input := `{"type": "object", "properties": {
		"type": {"type": ["string", "null"]},
		"const": {"type": "integer"},
		"examples": {"type": "array", "items": {"type": "string"}}
	}}`
	want := `{"properties": {
		"const": {"type": "integer"},
		"examples": {"items": {"type": "string"}, "type": "array"},
		"type": {"nullable": true, "type": "string"}
	}, "type": "object"}`

	var in, wantValue any
	if err := json.Unmarshal([]byte(input), &in); err != nil {
		t.Fatal(err)
	}

	if err := json.Unmarshal([]byte(want), &wantValue); err != nil {
		t.Fatal(err)
	}

	gotJSON, _ := json.Marshal(downgradeSchemas(in))

	wantJSON, _ := json.Marshal(wantValue)
	if string(gotJSON) != string(wantJSON) {
		t.Fatalf("got %s, want %s", gotJSON, wantJSON)
	}
}

func TestFilterDowngradesOnlyOpenAPI31(t *testing.T) {
	t.Parallel()
	spec := loadFixture(t)
	spec["openapi"] = "3.1.0"

	endpoints, err := parseEndpoints(strings.NewReader("GET /keep\n"))
	if err != nil {
		t.Fatal(err)
	}

	out, err := filterSpec(spec, endpoints)
	if err != nil {
		t.Fatal(err)
	}

	if out["openapi"] != downgradedVersion {
		t.Fatalf("openapi: got %v, want %s", out["openapi"], downgradedVersion)
	}

	out = filterFixture(t)
	if out["openapi"] != "3.0.2" {
		t.Fatalf("openapi 3.0 spec version changed to %v", out["openapi"])
	}
}

func TestFilterTypesUntypedParameters(t *testing.T) {
	t.Parallel()
	spec := loadFixture(t)
	paths := asMap(t, spec["paths"], "paths")
	keep := asMap(t, paths["/keep"], "/keep")
	get := asMap(t, keep["get"], "GET /keep")
	queryParam := func(name string, schema map[string]any) map[string]any {
		return map[string]any{"name": name, "in": "query", "schema": schema}
	}
	get["parameters"] = []any{
		queryParam("extended", map[string]any{"nullable": true}),
		queryParam("kind", map[string]any{"enum": []any{"movie", "show"}}),
		queryParam("limit", map[string]any{"type": "integer"}),
		queryParam("mixed", map[string]any{"oneOf": []any{}}),
	}

	endpoints, err := parseEndpoints(strings.NewReader("GET /keep\n"))
	if err != nil {
		t.Fatal(err)
	}

	out, err := filterSpec(spec, endpoints)
	if err != nil {
		t.Fatal(err)
	}

	op := asMap(t, asMap(t, asMap(t, out["paths"], "paths")["/keep"], "/keep")["get"], "get")

	params, ok := op["parameters"].([]any)
	if !ok {
		t.Fatal("parameters missing")
	}

	want := map[string]any{"extended": "string", "kind": "string", "limit": "integer", "mixed": nil}

	for _, p := range params {
		pm := asMap(t, p, "parameter")
		name, _ := pm["name"].(string)

		got := asMap(t, pm["schema"], name)["type"]
		if got != want[name] {
			t.Errorf("%s: type %v, want %v", name, got, want[name])
		}
	}
	// The component parameter Page was already typed and must not change.
	page := asMap(t, asMap(t, asMap(t, out["components"], "components")["parameters"], "parameters")["Page"], "Page")
	if asMap(t, page["schema"], "Page schema")["type"] != "integer" {
		t.Error("typed component parameter was rewritten")
	}
}

func TestEncodeSpecRoundTripsOneLinePerPath(t *testing.T) {
	t.Parallel()
	spec := loadFixture(t)

	data, err := encodeSpec(spec)
	if err != nil {
		t.Fatal(err)
	}

	var back any
	if err := json.Unmarshal(data, &back); err != nil {
		t.Fatalf("output is not valid JSON: %v\n%s", err, data)
	}

	wantJSON, _ := json.Marshal(spec)

	gotJSON, _ := json.Marshal(back)
	if string(gotJSON) != string(wantJSON) {
		t.Fatalf("round trip changed the document:\ngot  %s\nwant %s", gotJSON, wantJSON)
	}

	lines := strings.Split(string(data), "\n")

	for _, prefix := range []string{`    "/keep":{`, `    "/drop":{`, `      "Kept":{`, `      "traktAPI":{`} {
		found := false

		for _, line := range lines {
			if strings.HasPrefix(line, prefix) {
				found = true
				break
			}
		}

		if !found {
			t.Errorf("no line starts with %q:\n%s", prefix, data)
		}
	}
}

// repoTraktDir is the internal/trakt directory, relative to this package.
const repoTraktDir = "../.."

// maxFilteredSpecBytes bounds the committed filtered spec: the full Trakt
// spec is about 1.9 MB, and the filtered one must stay a small subset.
const maxFilteredSpecBytes = 1 << 20

// specInEnv names the full downloaded spec, as in the Makefile.
const specInEnv = "SPEC_IN"

// TestLocalAidLiveSpecHasEndpoints is a local aid, not a CI gate: it skips
// unless SPEC_IN names a downloaded full spec, and reports every endpoints.txt
// line the upstream spec no longer has. The offline gate is
// TestFilteredSpecMatchesEndpoints; upstream drift in CI is spec-drift.yml.
func TestLocalAidLiveSpecHasEndpoints(t *testing.T) {
	t.Parallel()

	specPath := os.Getenv(specInEnv)
	if specPath == "" {
		t.Skipf("local aid: %s not set; run `make spec-fetch` and pass the downloaded spec", specInEnv)
	}

	spec, err := readSpec(specPath)
	if err != nil {
		t.Fatal(err)
	}

	f, err := os.Open(filepath.Join(repoTraktDir, endpointsFileName))
	if err != nil {
		t.Fatal(err)
	}

	defer func() {
		if cerr := f.Close(); cerr != nil {
			t.Errorf("close endpoints file: %v", cerr)
		}
	}()

	endpoints, err := parseEndpoints(f)
	if err != nil {
		t.Fatal(err)
	}

	paths, ok := spec["paths"].(map[string]any)
	if !ok {
		t.Fatal("spec has no paths object")
	}

	for _, e := range endpoints {
		item, ok := paths[e.path].(map[string]any)
		if !ok {
			t.Errorf("%s %s: path not in spec", e.method, e.path)
			continue
		}

		if _, ok := item[strings.ToLower(e.method)]; !ok {
			t.Errorf("%s %s: method not in spec", e.method, e.path)
		}
	}
}

func TestCommittedFilteredSpecIsSmall(t *testing.T) {
	t.Parallel()

	info, err := os.Stat(filepath.Join(repoTraktDir, filteredSpecFileName))
	if err != nil {
		t.Fatal(err)
	}

	if info.Size() >= maxFilteredSpecBytes {
		t.Fatalf("filtered spec is %d bytes, want under %d", info.Size(), maxFilteredSpecBytes)
	}
}
