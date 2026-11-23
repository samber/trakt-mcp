package main

import (
	"encoding/json"
	"net/http"
	"path/filepath"
	"sort"
	"strings"
	"testing"
)

// itemsPath is the fixture operation with optional trailing segments.
const itemsPath = "/items/{type}/{id}"

// itemsAllPath is its clone without the optional segments.
const itemsAllPath = "/items"

// overlayFixture has one operation whose last two path segments are
// optional on the real API, plus a second operation to rename.
const overlayFixture = `{
  "/items/{type}/{id}": {
    "summary": "Items",
    "get": {
      "operationId": "getItems",
      "parameters": [
        {"name": "type", "in": "path", "required": true, "schema": {"type": "string"}},
        {"name": "id", "in": "path", "required": true, "schema": {"type": "string"}},
        {"name": "page", "in": "query", "schema": {"type": "integer"}}
      ],
      "responses": {"200": {"description": "ok"}}
    }
  },
  "/things/{id}": {
    "delete": {"operationId": "deleteOddName", "responses": {"204": {"description": "gone"}}}
  }
}`

func loadOverlayPaths(t *testing.T) map[string]any {
	t.Helper()

	var paths map[string]any
	if err := json.Unmarshal([]byte(overlayFixture), &paths); err != nil {
		t.Fatalf("fixture: %v", err)
	}

	return paths
}

func paramNames(t *testing.T, op map[string]any) []string {
	t.Helper()

	params, _ := op["parameters"].([]any)
	names := make([]string, 0, len(params))

	for _, p := range params {
		name, _ := asMap(t, p, "parameter")["name"].(string)
		names = append(names, name)
	}

	return names
}

func fixtureOverlay() overlay {
	return overlay{
		clones: []pathClone{
			{method: http.MethodGet, from: itemsPath, to: itemsAllPath, operationID: "getItemsAll"},
			{method: http.MethodGet, from: itemsPath, to: "/items/{type}", operationID: "getItemsByType"},
		},
		renames: map[endpoint]string{{method: http.MethodDelete, path: "/things/{id}"}: "deleteThing"},
	}
}

func TestOverlayClonesOperationWithoutDroppedPathParams(t *testing.T) {
	t.Parallel()

	paths := loadOverlayPaths(t)
	if err := applyOverlay(paths, fixtureOverlay()); err != nil {
		t.Fatalf("applyOverlay: %v", err)
	}

	tests := []struct {
		path   string
		opID   string
		params []string
	}{
		{path: itemsAllPath, opID: "getItemsAll", params: []string{"page"}},
		{path: "/items/{type}", opID: "getItemsByType", params: []string{"type", "page"}},
		{path: "/items/{type}/{id}", opID: "getItems", params: []string{"type", "id", "page"}},
	}
	for _, tt := range tests {
		item := asMap(t, paths[tt.path], tt.path)

		op := asMap(t, item["get"], tt.path+" get")
		if op["operationId"] != tt.opID {
			t.Errorf("%s: operationId %v, want %s", tt.path, op["operationId"], tt.opID)
		}

		if got := strings.Join(paramNames(t, op), ","); got != strings.Join(tt.params, ",") {
			t.Errorf("%s: parameters %s, want %s", tt.path, got, strings.Join(tt.params, ","))
		}

		if item["summary"] != "Items" {
			t.Errorf("%s: path-level summary not carried over", tt.path)
		}
	}

	// The clones must not share maps with the original operation.
	clone := asMap(t, asMap(t, paths[itemsAllPath], itemsAllPath)["get"], "clone")
	clone["description"] = "edited"

	orig := asMap(t, asMap(t, paths["/items/{type}/{id}"], "orig")["get"], "orig get")
	if _, ok := orig["description"]; ok {
		t.Error("editing a clone changed the original operation")
	}
}

func TestOverlayClonesIntoExistingPathItem(t *testing.T) {
	t.Parallel()

	paths := loadOverlayPaths(t)
	// /things/{id} already exists with DELETE: the clone adds GET beside it.
	ov := overlay{clones: []pathClone{{method: http.MethodGet, from: itemsPath, to: "/things/{id}", operationID: "getThing"}}}

	if err := applyOverlay(paths, ov); err != nil {
		t.Fatalf("applyOverlay: %v", err)
	}

	item := asMap(t, paths["/things/{id}"], "/things/{id}")
	if _, ok := item["delete"]; !ok {
		t.Error("existing DELETE was lost")
	}

	get := asMap(t, item["get"], "GET /things/{id}")
	if get["operationId"] != "getThing" {
		t.Errorf("operationId %v, want getThing", get["operationId"])
	}

	if got := strings.Join(paramNames(t, get), ","); got != "id,page" {
		t.Errorf("parameters %s, want id,page", got)
	}

	if _, ok := item["summary"]; ok {
		t.Error("source path-level summary overwrote the existing path item")
	}
}

func TestOverlayRenamesOperationID(t *testing.T) {
	t.Parallel()

	paths := loadOverlayPaths(t)
	if err := applyOverlay(paths, fixtureOverlay()); err != nil {
		t.Fatalf("applyOverlay: %v", err)
	}

	op := asMap(t, asMap(t, paths["/things/{id}"], "/things/{id}")["delete"], "delete")
	if op["operationId"] != "deleteThing" {
		t.Fatalf("operationId %v, want deleteThing", op["operationId"])
	}
}

func TestOverlayRejectsBadEntries(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		ov   overlay
	}{
		{name: "unknown clone source", ov: overlay{clones: []pathClone{{method: http.MethodGet, from: "/nope", to: "/n", operationID: "x"}}}},
		{name: "unknown clone method", ov: overlay{clones: []pathClone{{method: http.MethodPost, from: itemsPath, to: "/i", operationID: "x"}}}},
		{name: "clone target method exists", ov: overlay{clones: []pathClone{{method: http.MethodGet, from: itemsPath, to: itemsPath, operationID: "x"}}}},
		{name: "placeholder without parameter", ov: overlay{clones: []pathClone{{method: http.MethodGet, from: itemsPath, to: "/items/{slug}", operationID: "x"}}}},
		{name: "empty operationId", ov: overlay{clones: []pathClone{{method: http.MethodGet, from: itemsPath, to: itemsAllPath, operationID: ""}}}},
		{name: "duplicate operationId", ov: overlay{clones: []pathClone{{method: http.MethodGet, from: itemsPath, to: itemsAllPath, operationID: "getItems"}}}},
		{name: "unknown rename", ov: overlay{renames: map[endpoint]string{{method: http.MethodGet, path: "/nope"}: "x"}}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			if err := applyOverlay(loadOverlayPaths(t), tt.ov); err == nil {
				t.Fatal("applyOverlay accepted a bad overlay")
			}
		})
	}
}

// TestFilteredSpecMatchesEndpoints checks, offline, that the committed
// filtered spec holds exactly the endpoints.txt operations plus the overlay
// clones, so a hand edit or a stale regeneration cannot slip through.
func TestFilteredSpecMatchesEndpoints(t *testing.T) {
	t.Parallel()

	spec, err := readSpec(filepath.Join(repoTraktDir, filteredSpecFileName))
	if err != nil {
		t.Fatal(err)
	}

	endpoints, err := readEndpoints(filepath.Join(repoTraktDir, endpointsFileName))
	if err != nil {
		t.Fatal(err)
	}

	var want []string
	for _, e := range endpoints {
		want = append(want, e.method+" "+e.path)
	}

	for _, c := range traktOverlay.clones {
		want = append(want, c.method+" "+c.to)
	}

	var got []string

	paths := asMap(t, spec["paths"], "paths")
	for path, item := range paths {
		for key := range asMap(t, item, path) {
			if httpMethods[strings.ToUpper(key)] {
				got = append(got, strings.ToUpper(key)+" "+path)
			}
		}
	}

	sort.Strings(want)
	sort.Strings(got)

	if strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Fatalf("filtered spec operations differ from endpoints.txt plus overlay clones:\ngot:\n%s\nwant:\n%s",
			strings.Join(got, "\n"), strings.Join(want, "\n"))
	}
}
