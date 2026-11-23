// Command filterspec reduces Trakt's official OpenAPI document to the
// operations listed in internal/trakt/endpoints.txt, so that oapi-codegen
// generates a client for the endpoints the MCP tools use and nothing else.
//
// Usage:
//
//	go run ./internal/trakt/gen/filterspec -spec openapi.json \
//	    -endpoints internal/trakt/endpoints.txt -out internal/trakt/openapi.filtered.json
//
// Transforms applied, in order (each covered by a test in main_test.go or
// overlay_test.go):
//
//  1. Keep only the listed METHOD+path operations; a listed endpoint missing
//     from the spec is an error. Path-level fields (parameters, summary) stay.
//  2. Reduce "servers" to the production API host.
//  3. Keep only "tags" used by a kept operation.
//  4. Prune "components" entries not reachable through a "$ref" from the kept
//     operations; every security scheme is kept.
//  5. Only for an OpenAPI 3.1 input: rewrite 3.1 schema keywords that
//     oapi-codegen (OpenAPI 3.0) cannot read, and set "openapi" to 3.0.3:
//     type arrays with "null" become "nullable", numeric exclusive bounds
//     become minimum/maximum plus boolean flags, "const" becomes a
//     one-value "enum", and schema "examples" arrays become "example".
//  6. Give every untyped parameter schema (no "type", "$ref" or composition
//     keyword) the type "string". Trakt declares "extended" as only
//     {"nullable": true} and the search "type" as an untyped enum, which
//     oapi-codegen turns into interface{} arguments; on the wire every path
//     and query parameter is a string anyway.
//  7. Apply traktOverlay (overlay.go): clone operations under shorter paths
//     where the spec marks optional path segments as required, rename
//     misleading operationIds, then check operationIds are unique. The
//     filtered spec therefore holds the endpoints.txt operations plus the
//     clone paths, which TestFilteredSpecMatchesEndpoints checks offline.
package main

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"maps"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

const (
	// productionServer is the only Trakt API host the client calls; the
	// spec's second host (apiz) is private.
	productionServer = "https://api.trakt.tv"
	// productionServerDescription keeps the server entry self-describing.
	productionServerDescription = "Production"
	// downgradedVersion is the OpenAPI version written after a 3.1 input is
	// rewritten into 3.0 keywords, the latest 3.0 patch oapi-codegen reads.
	downgradedVersion = "3.0.3"
	// openAPI31Prefix identifies inputs that need the 3.1 to 3.0 rewrite.
	openAPI31Prefix = "3.1."
	// endpointsFileName and filteredSpecFileName live in internal/trakt.
	endpointsFileName    = "endpoints.txt"
	filteredSpecFileName = "openapi.filtered.json"
	// componentRefPrefix starts every local component reference.
	componentRefPrefix = "#/components/"
	// securitySchemesSection is never pruned: schemes are referenced by name
	// from "security" requirements, not by "$ref".
	securitySchemesSection = "securitySchemes"
	// jsonIndent matches the formatting of the committed filtered spec.
	jsonIndent = "  "
	// nullType is the JSON Schema type name OpenAPI 3.0 expresses as nullable.
	nullType = "null"
	// outputFileMode makes the generated spec readable by everyone, like
	// any other committed source file.
	outputFileMode = 0o644
)

// httpMethods lists the operation keys of an OpenAPI path item.
var httpMethods = map[string]bool{
	http.MethodGet: true, http.MethodPut: true, http.MethodPost: true, http.MethodDelete: true,
	http.MethodOptions: true, http.MethodHead: true, http.MethodPatch: true, http.MethodTrace: true,
}

// nameMapKeys are object keys whose value maps user-chosen names (property
// names, status codes, media types) to objects. Their own keys are never
// schema keywords, so the 3.1 rewrite skips them and descends into values.
var nameMapKeys = map[string]bool{
	"properties": true, "patternProperties": true, "schemas": true,
	"parameters": true, "responses": true, "headers": true, "content": true,
	"examples": true, "requestBodies": true, "securitySchemes": true,
	"links": true, "callbacks": true, "paths": true, "variables": true,
	"encoding": true, "definitions": true, "$defs": true,
}

type endpoint struct {
	method string
	path   string
}

func main() {
	specPath := flag.String("spec", "", "full Trakt OpenAPI document (JSON)")
	endpointsPath := flag.String("endpoints", "", "file listing one `METHOD /path` per line")
	outPath := flag.String("out", "", "filtered OpenAPI document to write")

	flag.Parse()

	if err := run(*specPath, *endpointsPath, *outPath); err != nil {
		slog.Error("filterspec failed", "error", err)
		os.Exit(1)
	}
}

func run(specPath, endpointsPath, outPath string) error {
	if specPath == "" || endpointsPath == "" || outPath == "" {
		return errors.New("-spec, -endpoints and -out are required")
	}

	spec, err := readSpec(specPath)
	if err != nil {
		return err
	}

	endpoints, err := readEndpoints(endpointsPath)
	if err != nil {
		return err
	}

	out, err := filterSpec(spec, endpoints)
	if err != nil {
		return err
	}

	paths, _ := out["paths"].(map[string]any)
	if err := applyOverlay(paths, traktOverlay); err != nil {
		return err
	}

	data, err := encodeSpec(out)
	if err != nil {
		return err
	}

	if err := os.WriteFile(outPath, data, outputFileMode); err != nil {
		return fmt.Errorf("write %s: %w", outPath, err)
	}

	slog.Info("filtered spec written", "path", outPath, "operations", len(endpoints), "bytes", len(data))

	return nil
}

func readSpec(path string) (map[string]any, error) {
	data, err := os.ReadFile(filepath.Clean(path))
	if err != nil {
		return nil, fmt.Errorf("read spec: %w", err)
	}

	dec := json.NewDecoder(bytes.NewReader(data))
	// UseNumber keeps numeric literals byte-for-byte instead of float64.
	dec.UseNumber()

	var spec map[string]any
	if err := dec.Decode(&spec); err != nil {
		return nil, fmt.Errorf("decode spec %s: %w", path, err)
	}

	return spec, nil
}

func readEndpoints(path string) (endpoints []endpoint, err error) {
	f, err := os.Open(filepath.Clean(path))
	if err != nil {
		return nil, fmt.Errorf("open endpoints: %w", err)
	}
	defer func() {
		if cerr := f.Close(); cerr != nil && err == nil {
			err = fmt.Errorf("close endpoints: %w", cerr)
		}
	}()

	return parseEndpoints(f)
}

// parseEndpoints reads one `METHOD /path` per line; blank lines are skipped.
func parseEndpoints(r io.Reader) ([]endpoint, error) {
	var endpoints []endpoint

	seen := map[endpoint]bool{}
	scanner := bufio.NewScanner(r)

	lineNo := 0
	for scanner.Scan() {
		lineNo++

		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}

		fields := strings.Fields(line)
		if len(fields) != 2 {
			return nil, fmt.Errorf("endpoints line %d: want `METHOD /path`, got %q", lineNo, line)
		}

		e := endpoint{method: fields[0], path: fields[1]}
		if !httpMethods[e.method] {
			return nil, fmt.Errorf("endpoints line %d: unknown upper-case HTTP method %q", lineNo, e.method)
		}

		if !strings.HasPrefix(e.path, "/") {
			return nil, fmt.Errorf("endpoints line %d: path %q must start with /", lineNo, e.path)
		}

		if seen[e] {
			return nil, fmt.Errorf("endpoints line %d: duplicate %s %s", lineNo, e.method, e.path)
		}

		seen[e] = true
		endpoints = append(endpoints, e)
	}

	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("read endpoints: %w", err)
	}

	if len(endpoints) == 0 {
		return nil, errors.New("endpoints list is empty")
	}

	return endpoints, nil
}

// filterSpec applies the transforms listed in the package comment. The
// result shares nested maps with spec, and transform 6 edits parameter
// schemas in place: callers must not reuse spec afterwards.
func filterSpec(spec map[string]any, endpoints []endpoint) (map[string]any, error) {
	keptPaths, usedTags, err := keepOperations(spec, endpoints)
	if err != nil {
		return nil, err
	}

	out := map[string]any{}
	maps.Copy(out, spec)

	out["paths"] = keptPaths
	out["servers"] = []any{map[string]any{"url": productionServer, "description": productionServerDescription}}

	filterTags(out, usedTags)

	if components, ok := spec["components"].(map[string]any); ok {
		pruned, err := pruneComponents(components, keptPaths)
		if err != nil {
			return nil, err
		}

		out["components"] = pruned
	}

	if version, ok := out["openapi"].(string); ok && strings.HasPrefix(version, openAPI31Prefix) {
		out["paths"] = downgradeSchemas(out["paths"])
		if components, ok := out["components"]; ok {
			out["components"] = downgradeSchemas(components)
		}

		out["openapi"] = downgradedVersion
	}

	typeUntypedParameters(out["paths"])

	if components, ok := out["components"].(map[string]any); ok {
		if params, ok := components["parameters"].(map[string]any); ok {
			for _, p := range params {
				typeParameter(p)
			}
		}
	}

	return out, nil
}

// typedSchemaKeys mark a parameter schema whose type is already decided.
var typedSchemaKeys = []string{"type", "$ref", "oneOf", "anyOf", "allOf", "not"}

// parameterStringType is the wire type of every path and query parameter.
const parameterStringType = "string"

// typeUntypedParameters applies transform 6 to the path-level and
// operation-level parameters of every path item under paths.
func typeUntypedParameters(paths any) {
	items, ok := paths.(map[string]any)
	if !ok {
		return
	}

	for _, item := range items {
		pathItem, ok := item.(map[string]any)
		if !ok {
			continue
		}

		typeParameterList(pathItem["parameters"])

		for method, op := range pathItem {
			if !httpMethods[strings.ToUpper(method)] {
				continue
			}

			if opMap, ok := op.(map[string]any); ok {
				typeParameterList(opMap["parameters"])
			}
		}
	}
}

func typeParameterList(v any) {
	params, ok := v.([]any)
	if !ok {
		return
	}

	for _, p := range params {
		typeParameter(p)
	}
}

func typeParameter(p any) {
	param, ok := p.(map[string]any)
	if !ok {
		return
	}

	schema, ok := param["schema"].(map[string]any)
	if !ok {
		return
	}

	for _, k := range typedSchemaKeys {
		if _, ok := schema[k]; ok {
			return
		}
	}

	schema["type"] = parameterStringType
}

// keepOperations applies transform 1: it returns the kept path items and the
// tag names their operations use.
func keepOperations(spec map[string]any, endpoints []endpoint) (map[string]any, map[string]bool, error) {
	paths, ok := spec["paths"].(map[string]any)
	if !ok {
		return nil, nil, errors.New("spec has no paths object")
	}

	keptPaths := map[string]any{}
	usedTags := map[string]bool{}

	for _, e := range endpoints {
		item, ok := paths[e.path].(map[string]any)
		if !ok {
			return nil, nil, fmt.Errorf("%s %s: path not in spec", e.method, e.path)
		}

		op, ok := item[strings.ToLower(e.method)].(map[string]any)
		if !ok {
			return nil, nil, fmt.Errorf("%s %s: method not in spec", e.method, e.path)
		}

		kept, ok := keptPaths[e.path].(map[string]any)
		if !ok {
			kept = map[string]any{}

			for k, v := range item {
				if !httpMethods[strings.ToUpper(k)] {
					kept[k] = v
				}
			}

			keptPaths[e.path] = kept
		}

		kept[strings.ToLower(e.method)] = op

		tags, _ := op["tags"].([]any)
		for _, t := range tags {
			if name, ok := t.(string); ok {
				usedTags[name] = true
			}
		}
	}

	return keptPaths, usedTags, nil
}

// filterTags applies transform 3 to out.
func filterTags(out map[string]any, usedTags map[string]bool) {
	tags, ok := out["tags"].([]any)
	if !ok {
		return
	}

	var kept []any

	for _, t := range tags {
		if m, ok := t.(map[string]any); ok && usedTags[fmt.Sprint(m["name"])] {
			kept = append(kept, m)
		}
	}

	if len(kept) > 0 {
		out["tags"] = kept
	} else {
		delete(out, "tags")
	}
}

// pruneComponents keeps the components reachable from roots through "$ref",
// following references inside kept components transitively.
func pruneComponents(components map[string]any, roots any) (map[string]any, error) {
	reached := map[string]bool{}

	queue := collectRefs(roots, nil)
	for len(queue) > 0 {
		ref := queue[0]
		queue = queue[1:]

		if reached[ref] {
			continue
		}

		section, name, err := splitComponentRef(ref)
		if err != nil {
			return nil, err
		}

		entries, ok := components[section].(map[string]any)
		if !ok {
			return nil, fmt.Errorf("dangling reference %s", ref)
		}

		target, ok := entries[name]
		if !ok {
			return nil, fmt.Errorf("dangling reference %s", ref)
		}

		reached[ref] = true
		queue = collectRefs(target, queue)
	}

	out := map[string]any{}

	for section, v := range components {
		if section == securitySchemesSection {
			out[section] = v
			continue
		}

		entries, ok := v.(map[string]any)
		if !ok {
			// Extensions such as x-* are not addressable by $ref; keep them.
			out[section] = v
			continue
		}

		kept := map[string]any{}

		for name, entry := range entries {
			if reached[componentRefPrefix+section+"/"+name] {
				kept[name] = entry
			}
		}

		if len(kept) > 0 {
			out[section] = kept
		}
	}

	return out, nil
}

func splitComponentRef(ref string) (section, name string, err error) {
	rest, ok := strings.CutPrefix(ref, componentRefPrefix)
	if !ok {
		return "", "", fmt.Errorf("unsupported non-local reference %s", ref)
	}

	section, name, ok = strings.Cut(rest, "/")
	if !ok || name == "" || strings.Contains(name, "/") {
		return "", "", fmt.Errorf("unsupported reference %s", ref)
	}

	return section, name, nil
}

// collectRefs appends every "$ref" string found under v to acc, in a
// deterministic order.
func collectRefs(v any, acc []string) []string {
	switch t := v.(type) {
	case map[string]any:
		if ref, ok := t["$ref"].(string); ok {
			acc = append(acc, ref)
		}

		for _, k := range sortedKeys(t) {
			acc = collectRefs(t[k], acc)
		}
	case []any:
		for _, item := range t {
			acc = collectRefs(item, acc)
		}
	}

	return acc
}

func sortedKeys(m map[string]any) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}

	sort.Strings(keys)

	return keys
}

// downgradeSchemas rewrites OpenAPI 3.1 schema keywords into their 3.0
// equivalents (transform 5) everywhere under v and returns the result.
func downgradeSchemas(v any) any {
	return downgradeValue(v, false)
}

func downgradeValue(v any, nameMap bool) any {
	switch t := v.(type) {
	case map[string]any:
		out := make(map[string]any, len(t))
		for k, child := range t {
			out[k] = downgradeValue(child, !nameMap && nameMapKeys[k])
		}

		if !nameMap {
			downgradeKeywords(out)
		}

		return out
	case []any:
		out := make([]any, len(t))
		for i, item := range t {
			out[i] = downgradeValue(item, false)
		}

		return out
	default:
		return v
	}
}

func downgradeKeywords(schema map[string]any) {
	if types, ok := schema["type"].([]any); ok {
		var concrete []any

		nullable := false

		for _, ty := range types {
			if ty == nullType {
				nullable = true
			} else {
				concrete = append(concrete, ty)
			}
		}

		delete(schema, "type")

		if len(concrete) == 1 {
			schema["type"] = concrete[0]
		}
		// Several non-null types have no 3.0 form; the schema is left untyped.
		if nullable {
			schema["nullable"] = true
		}
	}

	if bound, ok := schema["exclusiveMinimum"]; ok && isNumber(bound) {
		schema["minimum"] = bound
		schema["exclusiveMinimum"] = true
	}

	if bound, ok := schema["exclusiveMaximum"]; ok && isNumber(bound) {
		schema["maximum"] = bound
		schema["exclusiveMaximum"] = true
	}

	if c, ok := schema["const"]; ok {
		schema["enum"] = []any{c}
		delete(schema, "const")
	}

	if examples, ok := schema["examples"].([]any); ok {
		if len(examples) > 0 {
			schema["example"] = examples[0]
		}

		delete(schema, "examples")
	}
}

func isNumber(v any) bool {
	switch v.(type) {
	case float64, json.Number:
		return true
	default:
		return false
	}
}

// encodeSpec writes stable, diff-friendly output with sorted keys and no
// HTML escaping. Fully indented JSON is about 3.4 times larger than compact
// JSON for this deeply nested spec, so each top-level entry, each path and
// each component goes on its own compact line instead: a change to one
// endpoint shows up as a one-line diff.
func encodeSpec(spec map[string]any) ([]byte, error) {
	var buf bytes.Buffer
	if err := encodeLines(&buf, spec, 0, splitTopLevel); err != nil {
		return nil, err
	}

	buf.WriteByte('\n')

	return buf.Bytes(), nil
}

// splitRule tells encodeLines which entries of an object to expand into one
// line per child, and how to expand that child's own entries.
type splitRule func(key string) (expand bool, next splitRule)

func splitNothing(string) (bool, splitRule) { return false, splitNothing }

// splitTopLevel expands "paths" (one line per path) and "components" (one
// line per component, grouped by section).
func splitTopLevel(key string) (bool, splitRule) {
	switch key {
	case "paths":
		return true, splitNothing
	case "components":
		return true, func(string) (bool, splitRule) { return true, splitNothing }
	default:
		return false, splitNothing
	}
}

func encodeLines(buf *bytes.Buffer, m map[string]any, depth int, rule splitRule) error {
	indent := strings.Repeat(jsonIndent, depth+1)

	buf.WriteString("{\n")

	keys := sortedKeys(m)
	for i, k := range keys {
		buf.WriteString(indent)

		key, err := compactJSON(k)
		if err != nil {
			return err
		}

		buf.Write(key)
		buf.WriteByte(':')

		child, isObject := m[k].(map[string]any)

		expand, next := rule(k)
		if isObject && expand && len(child) > 0 {
			if err := encodeLines(buf, child, depth+1, next); err != nil {
				return err
			}
		} else {
			value, err := compactJSON(m[k])
			if err != nil {
				return err
			}

			buf.Write(value)
		}

		if i < len(keys)-1 {
			buf.WriteByte(',')
		}

		buf.WriteByte('\n')
	}

	buf.WriteString(strings.Repeat(jsonIndent, depth))
	buf.WriteByte('}')

	return nil
}

func compactJSON(v any) ([]byte, error) {
	var buf bytes.Buffer

	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)

	if err := enc.Encode(v); err != nil {
		return nil, fmt.Errorf("encode filtered spec: %w", err)
	}

	return bytes.TrimSuffix(buf.Bytes(), []byte("\n")), nil
}
