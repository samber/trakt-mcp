package oauth

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"strings"
	"testing"
	"time"
)

// fetchNow is a fixed clock so cache expiry assertions are exact.
var fetchNow = time.Unix(1_900_000_000, 0)

// testDocPath is where test servers serve the metadata document.
const testDocPath = "/client.json"

// docFor returns a valid metadata document for the request's own URL.
func docFor(r *http.Request) map[string]any {
	return map[string]any{
		paramClientID:                "https://" + r.Host + r.URL.Path,
		"client_name":                "Test Client",
		"redirect_uris":              []string{"https://client.example.com/callback"},
		"token_endpoint_auth_method": authMethodNone,
	}
}

func writeJSON(t *testing.T, w http.ResponseWriter, v any) {
	t.Helper()
	w.Header().Set("Content-Type", "application/json; charset=utf-8")

	if err := json.NewEncoder(w).Encode(v); err != nil {
		t.Errorf("encode: %v", err)
	}
}

// newTestFetcher builds a fetcher that trusts srv's certificate. allowPrivate
// lets it reach the loopback test server.
func newTestFetcher(t *testing.T, srv *httptest.Server, opts CIMDOptions) *CIMDFetcher {
	t.Helper()

	f := NewCIMDFetcher(opts)

	f.now = func() time.Time { return fetchNow }
	if srv != nil {
		tr, ok := f.client.Transport.(*http.Transport)
		if !ok {
			t.Fatalf("fetcher transport is %T", f.client.Transport)
		}

		srvTr, ok := srv.Client().Transport.(*http.Transport)
		if !ok {
			t.Fatalf("server transport is %T", srv.Client().Transport)
		}

		tr.TLSClientConfig = srvTr.TLSClientConfig.Clone()
	}

	return f
}

func TestFetch_OK(t *testing.T) {
	t.Parallel()

	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "public, max-age=600")
		writeJSON(t, w, docFor(r))
	}))
	defer srv.Close()

	f := newTestFetcher(t, srv, CIMDOptions{AllowPrivate: true})

	id := srv.URL + testDocPath

	c, exp, err := f.Fetch(context.Background(), id)
	if err != nil {
		t.Fatalf("Fetch: %v", err)
	}

	if c.ID != id || c.Kind != "cimd" || c.Name != "Test Client" {
		t.Errorf("client = %+v", c)
	}

	if len(c.RedirectURIs) != 1 || c.RedirectURIs[0] != "https://client.example.com/callback" {
		t.Errorf("redirect uris = %v", c.RedirectURIs)
	}

	if want := fetchNow.Add(600 * time.Second); !exp.Equal(want) || !c.ExpiresAt.Equal(want) {
		t.Errorf("expiry = %v / %v, want %v", exp, c.ExpiresAt, want)
	}
}

func TestFetch_NameFallsBackToHost(t *testing.T) {
	t.Parallel()

	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		doc := docFor(r)
		delete(doc, "client_name")
		delete(doc, "token_endpoint_auth_method")
		writeJSON(t, w, doc)
	}))
	defer srv.Close()

	f := newTestFetcher(t, srv, CIMDOptions{AllowPrivate: true})

	c, exp, err := f.Fetch(context.Background(), srv.URL+testDocPath)
	if err != nil {
		t.Fatalf("Fetch: %v", err)
	}

	if want := strings.TrimPrefix(srv.URL, "https://"); c.Name != strings.Split(want, ":")[0] {
		t.Errorf("name = %q, want host of %q", c.Name, want)
	}

	if want := fetchNow.Add(time.Hour); !exp.Equal(want) {
		t.Errorf("expiry without max-age = %v, want %v", exp, want)
	}
}

func TestFetch_RejectsPrivateIP(t *testing.T) {
	t.Parallel()

	var hits int

	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits++

		writeJSON(t, w, docFor(r))
	}))
	defer srv.Close()

	f := newTestFetcher(t, srv, CIMDOptions{})

	_, _, err := f.Fetch(context.Background(), srv.URL+testDocPath)
	if !errors.Is(err, ErrForbiddenAddress) {
		t.Fatalf("err = %v, want ErrForbiddenAddress", err)
	}

	if hits != 0 {
		t.Errorf("server reached %d times", hits)
	}
}

// TestFetch_RejectsPrivateResolvedName covers the dial-time guard: a DNS name
// is only checked once resolved.
func TestFetch_RejectsPrivateResolvedName(t *testing.T) {
	t.Parallel()

	var hits int

	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits++

		writeJSON(t, w, docFor(r))
	}))
	defer srv.Close()

	f := newTestFetcher(t, srv, CIMDOptions{})

	id := strings.Replace(srv.URL, "127.0.0.1", "localhost", 1) + testDocPath

	_, _, err := f.Fetch(context.Background(), id)
	if !errors.Is(err, ErrForbiddenAddress) {
		t.Fatalf("err = %v, want ErrForbiddenAddress", err)
	}

	if hits != 0 {
		t.Errorf("server reached %d times", hits)
	}
}

func TestFetch_RejectsRedirectToPrivate(t *testing.T) {
	t.Parallel()

	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "https://10.0.0.1"+testDocPath, http.StatusFound)
	}))
	defer srv.Close()

	f := newTestFetcher(t, srv, CIMDOptions{})
	// Only the loopback test server is reachable: the redirect target must
	// still be refused.
	f.allowAddr = func(a netip.Addr) bool { return a.IsLoopback() }

	_, _, err := f.Fetch(context.Background(), srv.URL+testDocPath)
	if !errors.Is(err, ErrForbiddenAddress) {
		t.Fatalf("err = %v, want ErrForbiddenAddress", err)
	}
}

func TestFetch_RejectsRedirectToHTTP(t *testing.T) {
	t.Parallel()

	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "http://127.0.0.1"+testDocPath, http.StatusFound)
	}))
	defer srv.Close()

	f := newTestFetcher(t, srv, CIMDOptions{AllowPrivate: true})

	if _, _, err := f.Fetch(context.Background(), srv.URL+testDocPath); err == nil {
		t.Fatal("redirect to http accepted")
	}
}

func TestFetch_RedirectLimit(t *testing.T) {
	t.Parallel()

	var hops int

	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hops++

		http.Redirect(w, r, "/next", http.StatusFound)
	}))
	defer srv.Close()

	f := newTestFetcher(t, srv, CIMDOptions{AllowPrivate: true})

	if _, _, err := f.Fetch(context.Background(), srv.URL+testDocPath); err == nil {
		t.Fatal("endless redirects accepted")
	}

	if hops != maxRedirects+1 {
		t.Errorf("hops = %d, want %d", hops, maxRedirects+1)
	}
}

func TestFetch_Oversize(t *testing.T) {
	t.Parallel()

	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		doc := docFor(r)
		doc["padding"] = strings.Repeat("x", 2*int(defaultMaxBytes))
		writeJSON(t, w, doc)
	}))
	defer srv.Close()

	f := newTestFetcher(t, srv, CIMDOptions{AllowPrivate: true})

	if _, _, err := f.Fetch(context.Background(), srv.URL+testDocPath); err == nil {
		t.Fatal("oversize document accepted")
	}
}

func TestFetch_Timeout(t *testing.T) {
	t.Parallel()

	release := make(chan struct{})

	srv := httptest.NewTLSServer(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		select {
		case <-release:
		case <-r.Context().Done():
		}
	}))
	defer srv.Close()
	defer close(release)

	f := newTestFetcher(t, srv, CIMDOptions{AllowPrivate: true, Timeout: 100 * time.Millisecond})

	start := time.Now()

	if _, _, err := f.Fetch(context.Background(), srv.URL+testDocPath); err == nil {
		t.Fatal("slow server accepted")
	}

	if elapsed := time.Since(start); elapsed > 2*time.Second {
		t.Errorf("timeout took %v", elapsed)
	}
}

func TestFetch_ClientIDMismatch(t *testing.T) {
	t.Parallel()

	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		doc := docFor(r)
		doc["client_id"] = "https://evil.example.com/client.json"
		writeJSON(t, w, doc)
	}))
	defer srv.Close()

	f := newTestFetcher(t, srv, CIMDOptions{AllowPrivate: true})

	if _, _, err := f.Fetch(context.Background(), srv.URL+testDocPath); err == nil {
		t.Fatal("mismatched client_id accepted")
	}
}

func TestFetch_RejectsBadDocuments(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		mutate func(doc map[string]any)
		ctype  string
	}{
		{"confidential auth method", func(d map[string]any) { d["token_endpoint_auth_method"] = "client_secret_basic" }, ""},
		{"no redirect uris", func(d map[string]any) { delete(d, "redirect_uris") }, ""},
		{"empty redirect uris", func(d map[string]any) { d["redirect_uris"] = []string{} }, ""},
		{"relative redirect uri", func(d map[string]any) { d["redirect_uris"] = []string{"/callback"} }, ""},
		{"not json", func(map[string]any) {}, "text/html"},
		{"too many redirect uris", func(d map[string]any) {
			uris := make([]string, maxRedirectURIs+1)
			for i := range uris {
				uris[i] = fmt.Sprintf("https://client.example.com/cb%d", i)
			}

			d["redirect_uris"] = uris
		}, ""},
		{"redirect uri too long", func(d map[string]any) {
			d["redirect_uris"] = []string{"https://client.example.com/" + strings.Repeat("a", maxRedirectURIBytes)}
		}, ""},
		{"client name too long", func(d map[string]any) {
			d["client_name"] = strings.Repeat("é", maxClientNameRunes+1)
		}, ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				doc := docFor(r)
				tt.mutate(doc)

				body, err := json.Marshal(doc)
				if err != nil {
					t.Errorf("marshal: %v", err)
					return
				}

				ctype := tt.ctype
				if ctype == "" {
					ctype = "application/json"
				}

				w.Header().Set("Content-Type", ctype)

				if _, err := w.Write(body); err != nil {
					t.Errorf("write: %v", err)
				}
			}))
			defer srv.Close()

			f := newTestFetcher(t, srv, CIMDOptions{AllowPrivate: true})
			if _, _, err := f.Fetch(context.Background(), srv.URL+testDocPath); err == nil {
				t.Fatal("bad document accepted")
			}
		})
	}
}

func TestFetch_RejectsNon200(t *testing.T) {
	t.Parallel()

	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
		writeJSON(t, w, docFor(r))
	}))
	defer srv.Close()

	f := newTestFetcher(t, srv, CIMDOptions{AllowPrivate: true})

	if _, _, err := f.Fetch(context.Background(), srv.URL+testDocPath); err == nil {
		t.Fatal("404 accepted")
	}
}

func TestFetch_HTTPScheme(t *testing.T) {
	t.Parallel()

	var hits int

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits++

		writeJSON(t, w, docFor(r))
	}))
	defer srv.Close()

	f := newTestFetcher(t, nil, CIMDOptions{AllowPrivate: true})

	if _, _, err := f.Fetch(context.Background(), srv.URL+testDocPath); err == nil {
		t.Fatal("http client_id accepted")
	}

	if hits != 0 {
		t.Errorf("server reached %d times", hits)
	}
}

func TestFetch_CacheControlCappedAt24h(t *testing.T) {
	t.Parallel()

	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "max-age=604800")
		writeJSON(t, w, docFor(r))
	}))
	defer srv.Close()

	f := newTestFetcher(t, srv, CIMDOptions{AllowPrivate: true})

	_, exp, err := f.Fetch(context.Background(), srv.URL+testDocPath)
	if err != nil {
		t.Fatalf("Fetch: %v", err)
	}

	if want := fetchNow.Add(24 * time.Hour); !exp.Equal(want) {
		t.Errorf("expiry = %v, want %v", exp, want)
	}
}

func TestCacheExpiry(t *testing.T) {
	t.Parallel()

	tests := []struct {
		header string
		want   time.Duration
	}{
		{"", time.Hour},
		{"max-age=0", time.Hour},
		{"max-age=-5", time.Hour},
		{"max-age=abc", time.Hour},
		{cacheControlNoStore, time.Hour},
		{"max-age=60", time.Minute},
		{"public, MAX-AGE=\"120\"", 2 * time.Minute},
		{"max-age=86400", 24 * time.Hour},
		{"max-age=86401", 24 * time.Hour},
		{"max-age=99999999999999999999", 24 * time.Hour},
	}
	for _, tt := range tests {
		got := cacheTTL(tt.header, 24*time.Hour)
		if got != tt.want {
			t.Errorf("cacheTTL(%q) = %v, want %v", tt.header, got, tt.want)
		}
	}
}

func TestValidateClientIDURL(t *testing.T) {
	t.Parallel()

	tests := []struct {
		id string
		ok bool
	}{
		{"https://client.example.com/metadata.json", true},
		{"https://client.example.com/a/b?x=1", true},
		{"http://client.example.com/metadata.json", false},
		{"https://client.example.com", false},
		{"https://client.example.com/", false},
		{"https://client.example.com/m.json#frag", false},
		{"https://user:pw@client.example.com/m.json", false},
		{"https://client.example.com/a/../m.json", false},
		{"https://client.example.com/./m.json", false},
		{"https:///m.json", false},
		{"client-id", false},
		{"", false},
	}
	for _, tt := range tests {
		err := validateClientIDURL(tt.id)
		if (err == nil) != tt.ok {
			t.Errorf("validateClientIDURL(%q) err = %v, want ok=%v", tt.id, err, tt.ok)
		}
	}
}

func TestIsPublicAddr(t *testing.T) {
	t.Parallel()

	tests := []struct {
		addr string
		ok   bool
	}{
		{"8.8.8.8", true},
		{"2606:4700:4700::1111", true},
		{"127.0.0.1", false},
		{"::1", false},
		{"10.1.2.3", false},
		{"172.16.0.1", false},
		{"192.168.1.1", false},
		{"169.254.169.254", false},
		{"fe80::1", false},
		{"fc00::1", false},
		{"100.64.0.1", false},
		{"100.127.255.255", false},
		{"100.128.0.1", true},
		{"224.0.0.1", false},
		{"ff02::1", false},
		{"0.0.0.0", false},
		{"::", false},
		{"::ffff:127.0.0.1", false},
		{"::ffff:10.0.0.1", false},
		{"64:ff9b::a00:1", false},
		{"255.255.255.255", false},
	}
	for _, tt := range tests {
		if got := isPublicAddr(netip.MustParseAddr(tt.addr)); got != tt.ok {
			t.Errorf("isPublicAddr(%s) = %v, want %v", tt.addr, got, tt.ok)
		}
	}
}
