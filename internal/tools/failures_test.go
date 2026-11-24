package tools

import (
	"net/http"
	"strings"
	"testing"
)

// Case names of the Trakt failure matrix not already named elsewhere.
const (
	testCaseAccountLimit = "account limit"
	testCaseVIPRequired  = "vip required"
)

// failureTool is one tool under the Trakt failure matrix: the call it makes
// and the route that answers it.
type failureTool struct {
	tool  string
	args  map[string]any
	route string
	// reads is true when the tool decodes a Trakt body.
	reads bool
}

// runTraktFailureMatrix checks that every Trakt failure of each tool maps to
// its fixed message, never echoes Trakt's text to the caller or the logs,
// and that a malformed body of a reading tool is an internal error.
func runTraktFailureMatrix(t *testing.T, tools []failureTool) {
	t.Helper()

	hostile := `{"error_description":"` + injectionMarker + `"}`
	failures := []struct {
		name   string
		status int
		body   string
		want   string
	}{
		{msgNotFound, http.StatusNotFound, hostile, msgNotFound},
		{testCaseUnauthorized, http.StatusUnauthorized, hostile, msgUpstreamAuth},
		{testCaseAccountLimit, statusTraktAccountLimit, hostile, msgAccountLimit},
		{testCaseVIPRequired, http.StatusUpgradeRequired, hostile, msgVIPRequired},
		{testCaseServerError, http.StatusInternalServerError, hostile, testMsgServerError},
		{testCaseBadGateway, http.StatusBadGateway, ``, testMsgBadGateway},
	}

	for _, tl := range tools {
		for _, f := range failures {
			t.Run(tl.tool+"/"+f.name, func(t *testing.T) {
				t.Parallel()

				h := newHarness(t)
				h.trakt.respond(tl.route, f.status, f.body, nil)
				h.fail(tl.tool, tl.args, f.want)

				if strings.Contains(h.logs.String(), injectionMarker) {
					t.Error("Trakt's message reached the logs")
				}
			})
		}

		t.Run(tl.tool+"/rate limited", func(t *testing.T) {
			t.Parallel()

			h := newHarness(t)
			h.trakt.respond(tl.route, testStatusRateLimited, hostile, nil)

			res := h.call(tl.tool, tl.args)
			if !res.IsError || !strings.Contains(resultText(res), "rate limited") || strings.Contains(resultText(res), injectionMarker) {
				t.Errorf("result = %v %q, want a rate limit error without Trakt text", res.IsError, resultText(res))
			}
		})

		if tl.reads {
			t.Run(tl.tool+"/malformed body", func(t *testing.T) {
				t.Parallel()

				h := newHarness(t)
				h.trakt.respond(tl.route, http.StatusOK, `{"not": "closed"`, nil)
				h.fail(tl.tool, tl.args, msgInternal)
			})
		}
	}
}
