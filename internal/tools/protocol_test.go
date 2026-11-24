package tools

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"slices"
	"strings"
	"sync"
	"testing"
)

// methodRecorder wraps the handler and records the JSON-RPC method of every
// POST body it sees.
type methodRecorder struct {
	next    http.Handler
	mu      sync.Mutex
	methods []string
}

func (m *methodRecorder) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Body != nil {
		body, err := io.ReadAll(r.Body)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}

		r.Body = io.NopCloser(bytes.NewReader(body))

		var msg struct {
			Method string `json:"method"`
		}
		if json.Unmarshal(body, &msg) == nil && msg.Method != "" {
			m.mu.Lock()
			m.methods = append(m.methods, msg.Method)
			m.mu.Unlock()
		}
	}

	m.next.ServeHTTP(w, r)
}

func (m *methodRecorder) seen() []string {
	m.mu.Lock()
	defer m.mu.Unlock()

	return slices.Clone(m.methods)
}

func TestLegacyInitializeBothVersions(t *testing.T) {
	t.Parallel()

	cases := []struct {
		version string
		// initialize is whether the client opens with initialize: legacy
		// versions do, the native stateless version does not.
		initialize bool
	}{
		{"2025-06-18", true},
		{"2025-11-25", true},
		{"2026-07-28", false},
	}
	for _, c := range cases {
		t.Run(c.version, func(t *testing.T) {
			t.Parallel()

			rec := &methodRecorder{}
			h := newHarness(t, withWrap(func(next http.Handler) http.Handler {
				rec.next = next
				return rec
			}))
			h.trakt.respond(trendingRoute, http.StatusOK, trendingJSON(), nil)

			h.session = h.connect(c.version)
			if got := h.session.InitializeResult().ProtocolVersion; got != c.version {
				t.Fatalf("negotiated %q, want %q", got, c.version)
			}

			var out Listing[TrendingMovie]
			h.ok(toolTrending, map[string]any{fieldLimit: 2}, &out)

			if len(out.Items) != 2 {
				t.Errorf("items = %+v, want 2", out.Items)
			}

			seen := rec.seen()
			if slices.Contains(seen, "initialize") != c.initialize || !slices.Contains(seen, "tools/call") {
				t.Errorf("methods = %v, want initialize %v and tools/call", seen, c.initialize)
			}
		})
	}
}

func TestNoBearer_401WithResourceMetadata(t *testing.T) {
	t.Parallel()

	h := newHarness(t)
	for _, authz := range []string{"", "Bearer wrong"} {
		req, err := http.NewRequestWithContext(t.Context(), http.MethodPost, h.srv.URL, strings.NewReader(`{"jsonrpc":"2.0","id":1,"method":"tools/list"}`))
		if err != nil {
			t.Fatal(err)
		}

		req.Header.Set("Content-Type", "application/json")

		if authz != "" {
			req.Header.Set("Authorization", authz)
		}

		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}

		if err := resp.Body.Close(); err != nil {
			t.Fatal(err)
		}

		if resp.StatusCode != http.StatusUnauthorized {
			t.Errorf("%q: status = %d, want 401", authz, resp.StatusCode)
		}

		want := `Bearer resource_metadata="` + testMetadataURL + `"`
		if got := resp.Header.Get("WWW-Authenticate"); got != want {
			t.Errorf("%q: WWW-Authenticate = %q, want %q", authz, got, want)
		}
	}
}
