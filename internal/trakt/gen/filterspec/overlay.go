package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"regexp"
	"strings"
)

// pathClone copies the operation `method from` to the shorter path `to`.
// Path parameters whose placeholder is absent from `to` are dropped from the
// copy; everything else (query parameters, responses, path-level fields) is
// kept.
type pathClone struct {
	method      string
	from        string
	to          string
	operationID string
}

// overlay holds hand-maintained corrections applied after filtering
// (transform 7 in the package comment).
type overlay struct {
	clones  []pathClone
	renames map[endpoint]string
}

// traktOverlay corrects the Trakt spec where it disagrees with the API.
//
// Clones: the spec declares the history and ratings filters as required
// path segments, while the API documents them as optional ("Specify a type
// and Trakt id to limit the history for just that item"). Without the short
// forms the unfiltered history and ratings calls cannot be expressed.
//
// Renames: the spec names "Remove a playback item" deleteSyncProgressDropMovie.
var traktOverlay = overlay{
	clones: []pathClone{
		{method: http.MethodGet, from: "/sync/history/{type}/{id}", to: "/sync/history", operationID: "getSyncHistoryAll"},
		{method: http.MethodGet, from: "/sync/history/{type}/{id}", to: "/sync/history/{type}", operationID: "getSyncHistoryByType"},
		{method: http.MethodGet, from: "/sync/ratings/{type}/{rating}", to: "/sync/ratings", operationID: "getSyncRatingsAll"},
		{method: http.MethodGet, from: "/sync/ratings/{type}/{rating}", to: "/sync/ratings/{type}", operationID: "getSyncRatingsByType"},
	},
	renames: map[endpoint]string{
		{method: http.MethodDelete, path: "/sync/playback/{id}"}: "deleteSyncPlaybackItem",
	},
}

// placeholderPattern matches a `{name}` path template segment.
var placeholderPattern = regexp.MustCompile(`\{([^{}/]+)\}`)

// pathParameterLocation is the "in" value of a path parameter.
const pathParameterLocation = "path"

// applyOverlay applies ov to the filtered paths in place, then checks that
// every operationId is still unique.
func applyOverlay(paths map[string]any, ov overlay) error {
	for _, c := range ov.clones {
		if err := clonePath(paths, c); err != nil {
			return err
		}
	}

	for e, opID := range ov.renames {
		op, err := lookupOperation(paths, e.method, e.path)
		if err != nil {
			return fmt.Errorf("rename: %w", err)
		}

		op["operationId"] = opID
	}

	return checkUniqueOperationIDs(paths)
}

func clonePath(paths map[string]any, c pathClone) error {
	if c.operationID == "" {
		return fmt.Errorf("clone %s %s: empty operationId", c.method, c.to)
	}

	if _, err := lookupOperation(paths, c.method, c.from); err != nil {
		return fmt.Errorf("clone: %w", err)
	}

	target, targetExists := paths[c.to].(map[string]any)
	if _, taken := target[strings.ToLower(c.method)]; taken {
		return fmt.Errorf("clone %s %s: target operation already exists", c.method, c.to)
	}

	copied, err := deepCopy(paths[c.from])
	if err != nil {
		return fmt.Errorf("clone %s %s: %w", c.method, c.from, err)
	}

	source, _ := copied.(map[string]any)
	keep := map[string]bool{}

	for _, m := range placeholderPattern.FindAllStringSubmatch(c.to, -1) {
		keep[m[1]] = true
	}

	op, _ := source[strings.ToLower(c.method)].(map[string]any)
	op["operationId"] = c.operationID

	// Path-level parameters of the source move into the operation, so that
	// the clone carries them whether or not the target path item exists.
	if shared, ok := source["parameters"].([]any); ok {
		own, _ := op["parameters"].([]any)
		op["parameters"] = append(append([]any{}, shared...), own...)
	}

	found := map[string]bool{}
	setPathParameters(op, keep, found)

	for name := range keep {
		if !found[name] {
			return fmt.Errorf("clone %s %s: placeholder {%s} has no path parameter in %s", c.method, c.to, name, c.from)
		}
	}

	if !targetExists {
		// A new path item takes the source's other path-level fields
		// (summary, description), which describe the same resource.
		target = map[string]any{}

		for k, v := range source {
			if !httpMethods[strings.ToUpper(k)] && k != "parameters" {
				target[k] = v
			}
		}

		paths[c.to] = target
	}

	target[strings.ToLower(c.method)] = op

	return nil
}

// setPathParameters drops from obj["parameters"] the path parameters not
// named in keep, records the kept ones in found, and removes the list when
// it ends up empty.
func setPathParameters(obj map[string]any, keep, found map[string]bool) {
	params, ok := obj["parameters"].([]any)
	if !ok {
		return
	}

	filtered := filterPathParameters(params, keep, found)
	if len(filtered) == 0 {
		delete(obj, "parameters")

		return
	}

	obj["parameters"] = filtered
}

// filterPathParameters returns params without the path parameters not named
// in keep, recording the kept ones in found.
func filterPathParameters(params []any, keep, found map[string]bool) []any {
	out := make([]any, 0, len(params))

	for _, p := range params {
		param, ok := p.(map[string]any)
		if !ok || param["in"] != pathParameterLocation {
			out = append(out, p)

			continue
		}

		name, _ := param["name"].(string)
		if keep[name] {
			found[name] = true

			out = append(out, p)
		}
	}

	return out
}

func lookupOperation(paths map[string]any, method, path string) (map[string]any, error) {
	item, ok := paths[path].(map[string]any)
	if !ok {
		return nil, fmt.Errorf("%s %s: path not in filtered spec", method, path)
	}

	op, ok := item[strings.ToLower(method)].(map[string]any)
	if !ok {
		return nil, fmt.Errorf("%s %s: method not in filtered spec", method, path)
	}

	return op, nil
}

func checkUniqueOperationIDs(paths map[string]any) error {
	seen := map[string]string{}

	for _, path := range sortedKeys(paths) {
		item, _ := paths[path].(map[string]any)
		for _, method := range sortedKeys(item) {
			if !httpMethods[strings.ToUpper(method)] {
				continue
			}

			op, _ := item[method].(map[string]any)

			opID, _ := op["operationId"].(string)
			if opID == "" {
				continue
			}

			where := strings.ToUpper(method) + " " + path
			if prev, dup := seen[opID]; dup {
				return fmt.Errorf("operationId %q used by %s and %s", opID, prev, where)
			}

			seen[opID] = where
		}
	}

	return nil
}

// deepCopy returns an independent copy of a decoded JSON value.
func deepCopy(v any) (any, error) {
	data, err := json.Marshal(v)
	if err != nil {
		return nil, err
	}

	dec := json.NewDecoder(bytes.NewReader(data))
	dec.UseNumber()

	var out any
	if err := dec.Decode(&out); err != nil {
		return nil, err
	}

	if out == nil {
		return nil, errors.New("empty value")
	}

	return out, nil
}
