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

	"github.com/samber/google-contacts-mcp/internal/contacts"
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

// recordingHarness is a harness whose server records request methods.
func recordingHarness(t *testing.T) (*harness, *methodRecorder) {
	t.Helper()

	rec := &methodRecorder{}
	h := newHarnessWrapped(t, func(next http.Handler) http.Handler {
		rec.next = next
		return rec
	})

	return h, rec
}

func legacyRoundTrip(t *testing.T, version string) {
	t.Helper()

	h, rec := recordingHarness(t)
	h.people.put(contacts.Contact{GivenName: testGivenName, Emails: []contacts.Typed{{Value: testEmail}}})

	h.session = h.connect(version)
	if got := h.session.InitializeResult().ProtocolVersion; got != version {
		t.Fatalf("negotiated %q, want %q", got, version)
	}

	var out SearchOutput
	h.ok(toolSearchContacts, map[string]any{argQuery: "ada"}, &out)

	if len(out.Contacts) != 1 {
		t.Errorf("contacts = %+v", out.Contacts)
	}

	if seen := rec.seen(); !slices.Contains(seen, "initialize") || !slices.Contains(seen, "tools/call") {
		t.Errorf("methods = %v", seen)
	}
}

func TestLegacyInitialize_20250618(t *testing.T) {
	t.Parallel()
	legacyRoundTrip(t, "2025-06-18")
}

func TestLegacyInitialize_20251125(t *testing.T) {
	t.Parallel()
	legacyRoundTrip(t, "2025-11-25")
}

func TestStateless_20260728(t *testing.T) {
	t.Parallel()

	h, rec := recordingHarness(t)
	h.people.put(contacts.Contact{GivenName: testGivenName})
	h.session = h.connect("2026-07-28")

	var out SearchOutput
	h.ok(toolSearchContacts, map[string]any{argQuery: "ada"}, &out)

	if len(out.Contacts) != 1 {
		t.Errorf("contacts = %+v", out.Contacts)
	}

	seen := rec.seen()
	if slices.Contains(seen, "initialize") || !slices.Contains(seen, "tools/call") {
		t.Errorf("methods = %v, want tools/call without initialize", seen)
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
