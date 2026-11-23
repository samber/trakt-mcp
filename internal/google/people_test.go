package google

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"google.golang.org/api/googleapi"
	"google.golang.org/api/people/v1"

	"github.com/samber/google-contacts-mcp/internal/contacts"
)

func (e *testEnv) people(t *testing.T) contacts.People {
	t.Helper()

	p, err := e.up.People(context.Background(), e.userID)
	if err != nil {
		t.Fatalf("People: %v", err)
	}

	return p
}

func TestSearch_WarmupOncePerUser(t *testing.T) {
	t.Parallel()

	env := newTestEnv(t)

	ctx := context.Background()
	for _, q := range []string{"ada", "bob", "carl"} {
		// A fresh client per call, as each MCP request gets one.
		got, err := env.people(t).Search(ctx, q, 100)
		if err != nil {
			t.Fatalf("Search(%q): %v", q, err)
		}

		if len(got) != 1 || got[0].ResourceName != "people/c1" {
			t.Fatalf("Search(%q) = %+v", q, got)
		}
	}

	env.fake.mu.Lock()
	empty := env.fake.emptySearches
	env.fake.mu.Unlock()

	if empty != 1 {
		t.Errorf("warmup calls = %d, want 1", empty)
	}

	if n := env.fake.count(routeSearch); n != 4 {
		t.Errorf("search calls = %d, want 4", n)
	}

	query, _, auth, _ := env.fake.last(routeSearch)

	v, err := url.ParseQuery(query)
	if err != nil {
		t.Fatalf("ParseQuery: %v", err)
	}

	if v.Get("query") != "carl" || v.Get("pageSize") != "30" || v.Get("readMask") != personFields {
		t.Errorf("search query = %q", query)
	}

	if auth != "Bearer access-1" {
		t.Errorf("Authorization = %q", auth)
	}
}

func googleError(w http.ResponseWriter, code int, status string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	_, _ = fmt.Fprintf(w, `{"error":{"code":%d,"message":"private detail","status":%q}}`, code, status)
}

// errorMappingCase is one upstream failure and the error the client must map it to.
type errorMappingCase struct {
	name      string
	route     string
	code      int
	status    string
	header    map[string]string
	call      func(contacts.People) error
	wantIs    error
	wantRetry time.Duration // RateLimitedError.RetryAfter, 0 when not rate limited
	wantHits  int
}

// assertMappedError checks the mapped error, the absence of leaked upstream details and the attempt count.
func assertMappedError(t *testing.T, env *testEnv, tc errorMappingCase, err error) {
	t.Helper()

	if err == nil {
		t.Fatal("err = nil")
	}

	if strings.Contains(err.Error(), "private detail") {
		t.Errorf("error leaks the Google response body: %v", err)
	}

	if tc.wantIs != nil && !errors.Is(err, tc.wantIs) {
		t.Errorf("err = %v, want %v", err, tc.wantIs)
	}

	var rl contacts.RateLimitedError
	if gotRL := errors.As(err, &rl); gotRL != (tc.wantRetry != 0) || rl.RetryAfter != tc.wantRetry {
		t.Errorf("err = %v (rate limited %v, retry %v), want retry %v", err, gotRL, rl.RetryAfter, tc.wantRetry)
	}

	if tc.wantIs == nil && tc.wantRetry == 0 && !strings.Contains(err.Error(), strconv.Itoa(tc.code)) {
		t.Errorf("err = %v, want the HTTP status", err)
	}

	if n := env.fake.count(tc.route); n != tc.wantHits {
		t.Errorf("hits = %d, want %d", n, tc.wantHits)
	}
}

func TestErrorMapping(t *testing.T) {
	t.Parallel()

	cases := []errorMappingCase{
		{name: "404 get", route: routeGet, code: 404, status: "NOT_FOUND", call: getC1, wantIs: contacts.ErrNotFound, wantHits: 1},
		{name: "400 failedPrecondition update", route: routeUpdate, code: 400, status: "FAILED_PRECONDITION", call: updateC1, wantIs: contacts.ErrConflict, wantHits: 1},
		{name: "429 with Retry-After", route: routeGet, code: 429, status: "RESOURCE_EXHAUSTED", header: map[string]string{"Retry-After": "7"}, call: getC1, wantRetry: 7 * time.Second, wantHits: 1},
		{name: "429 default Retry-After", route: routeGet, code: 429, status: "RESOURCE_EXHAUSTED", call: getC1, wantRetry: 30 * time.Second, wantHits: 1},
		{name: "401 get refreshed and retried once", route: routeGet, code: 401, status: "UNAUTHENTICATED", call: getC1, wantIs: contacts.ErrUpstreamAuth, wantHits: 2},
		{name: "503 get retried once", route: routeGet, code: 503, status: "UNAVAILABLE", call: getC1, wantHits: 2},
		{name: "503 create not retried", route: routeCreate, code: 503, status: "UNAVAILABLE", call: createOne, wantHits: 1},
		{name: "400 other update", route: routeUpdate, code: 400, status: "INVALID_ARGUMENT", call: updateC1, wantHits: 1},
		{name: "400 bad page token list", route: routeList, code: 400, status: "INVALID_ARGUMENT", call: listBadToken, wantIs: contacts.ErrInvalidArgument, wantHits: 1},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			env := newTestEnv(t)
			env.fake.override = func(w http.ResponseWriter, _ *http.Request, rt string) bool {
				if rt != tc.route {
					return false
				}

				for k, v := range tc.header {
					w.Header().Set(k, v)
				}

				googleError(w, tc.code, tc.status)

				return true
			}

			assertMappedError(t, env, tc, tc.call(env.people(t)))
		})
	}
}

func listBadToken(p contacts.People) error {
	_, _, err := p.List(context.Background(), 10, "bogus", "")
	return err
}

func getC1(p contacts.People) error {
	_, err := p.Get(context.Background(), "people/c1")
	return err
}

func updateC1(p contacts.People) error {
	c := contacts.FromPerson(testPerson("people/c1", "e1"))
	_, err := p.Update(context.Background(), c, []string{contacts.FieldNames})

	return err
}

func createOne(p contacts.People) error {
	_, err := p.Create(context.Background(), contacts.Contact{GivenName: "New"})
	return err
}

func TestGet_RetriesOnceThenSucceeds(t *testing.T) {
	t.Parallel()

	env := newTestEnv(t)
	env.fake.override = func(w http.ResponseWriter, _ *http.Request, rt string) bool {
		if rt == routeGet && env.fake.count(routeGet) == 1 {
			googleError(w, http.StatusInternalServerError, "INTERNAL")
			return true
		}

		return false
	}

	c, err := env.people(t).Get(context.Background(), "people/c9")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}

	if c.ResourceName != "people/c9" || c.Etag != "e1" || c.GivenName != "Ada" {
		t.Errorf("Get = %+v", c)
	}

	query, _, _, _ := env.fake.last(routeGet)
	if v, _ := url.ParseQuery(query); v.Get("personFields") != personFields {
		t.Errorf("query = %q", query)
	}
}

func TestList_Pagination(t *testing.T) {
	t.Parallel()

	env := newTestEnv(t)
	p := env.people(t)
	ctx := context.Background()

	got, next, err := p.List(ctx, 50, "", "LAST_MODIFIED_DESCENDING")
	if err != nil {
		t.Fatalf("List: %v", err)
	}

	if len(got) != 1 || next != "page-2" {
		t.Errorf("List = %d contacts, next %q", len(got), next)
	}

	query, _, _, _ := env.fake.last(routeList)

	v, err := url.ParseQuery(query)
	if err != nil {
		t.Fatalf("ParseQuery: %v", err)
	}

	if v.Get("pageSize") != "50" || v.Get("sortOrder") != "LAST_MODIFIED_DESCENDING" || v.Get("personFields") != personFields {
		t.Errorf("list query = %q", query)
	}

	_, next, err = p.List(ctx, 50, "page-2", "")
	if err != nil {
		t.Fatalf("List page 2: %v", err)
	}

	if next != "" {
		t.Errorf("next = %q, want empty", next)
	}

	query, _, _, _ = env.fake.last(routeList)
	if v, _ := url.ParseQuery(query); v.Get("pageToken") != "page-2" || v.Has("sortOrder") {
		t.Errorf("page 2 query = %q", query)
	}
}

// assertUpdateSent checks the path, query and body of the last update request.
func assertUpdateSent(t *testing.T, env *testEnv) {
	t.Helper()

	query, body, _, path := env.fake.last(routeUpdate)
	if path != "/v1/people/c1:updateContact" {
		t.Errorf("path = %q", path)
	}

	v, err := url.ParseQuery(query)
	if err != nil {
		t.Fatalf("ParseQuery: %v", err)
	}

	if v.Get("updatePersonFields") != contacts.FieldOrganizations || v.Get("personFields") != personFields {
		t.Errorf("update query = %q", query)
	}

	var sent people.Person
	if err := json.Unmarshal(body, &sent); err != nil {
		t.Fatalf("decode body: %v", err)
	}

	if sent.Metadata == nil || len(sent.Metadata.Sources) != 1 || sent.Metadata.Sources[0].Etag != "e1" || sent.Metadata.Sources[0].Type != "CONTACT" {
		t.Errorf("sent metadata = %+v", sent.Metadata)
	}

	if len(sent.Organizations) != 1 || sent.Organizations[0].Name != "Analytical Engines" {
		t.Errorf("sent organizations = %+v", sent.Organizations)
	}
}

func TestUpdate_SendsEtagAndFields(t *testing.T) {
	t.Parallel()

	env := newTestEnv(t)
	c := contacts.FromPerson(testPerson("people/c1", "e1"))
	c.Company = "Analytical Engines"

	got, err := env.people(t).Update(context.Background(), c, []string{contacts.FieldOrganizations})
	if err != nil {
		t.Fatalf("Update: %v", err)
	}

	assertUpdateSent(t, env)

	if got.Etag != "e2" || got.Company != "Analytical Engines" {
		t.Errorf("Update = %+v, want the response contact", got)
	}

	// Without explicit fields, the contact's own non-empty fields are sent.
	if _, err := env.people(t).Update(context.Background(), c, nil); err != nil {
		t.Fatalf("Update nil fields: %v", err)
	}

	query, _, _, _ := env.fake.last(routeUpdate)
	if v, _ := url.ParseQuery(query); v.Get("updatePersonFields") != strings.Join(c.UpdateFields(), ",") {
		t.Errorf("update query = %q", query)
	}

	c.Etag = ""
	if _, err := env.people(t).Update(context.Background(), c, nil); err == nil {
		t.Error("Update without etag: err = nil")
	}
}

func TestCreateDeleteAndGroups(t *testing.T) {
	t.Parallel()

	env := newTestEnv(t)
	p := env.people(t)
	ctx := context.Background()

	c, err := p.Create(ctx, contacts.Contact{GivenName: "New", Emails: []contacts.Typed{{Value: "new@example.com"}}})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	if c.ResourceName != "people/new" || c.GivenName != "New" {
		t.Errorf("Create = %+v", c)
	}

	if err := p.Delete(ctx, "people/c1"); err != nil {
		t.Fatalf("Delete: %v", err)
	}

	if _, _, _, path := env.fake.last(routeDelete); path != "/v1/people/c1:deleteContact" {
		t.Errorf("delete path = %q", path)
	}
}

func TestListAndCreateGroups(t *testing.T) {
	t.Parallel()

	env := newTestEnv(t)
	p := env.people(t)
	ctx := context.Background()

	env.fake.groups = []*people.ContactGroup{
		{ResourceName: "contactGroups/1", Name: testGroupFriendsClub, FormattedName: testGroupFriendsClub, GroupType: googleGroupUser, MemberCount: 3},
		{ResourceName: "contactGroups/myContacts", Name: "myContacts", FormattedName: "My Contacts", GroupType: "SYSTEM_CONTACT_GROUP"},
	}

	groups, err := p.ListGroups(ctx)
	if err != nil {
		t.Fatalf("ListGroups: %v", err)
	}

	want := []contacts.Group{
		{ResourceName: "contactGroups/1", Name: testGroupFriendsClub, Type: contacts.GroupTypeUser, MemberCount: 3},
		{ResourceName: "contactGroups/myContacts", Name: "My Contacts", Type: contacts.GroupTypeSystem},
	}
	if len(groups) != len(want) || groups[0] != want[0] || groups[1] != want[1] {
		t.Errorf("ListGroups = %+v, want %+v", groups, want)
	}

	g, err := p.CreateGroup(ctx, "Investors")
	if err != nil {
		t.Fatalf("CreateGroup: %v", err)
	}

	if g.ResourceName != "contactGroups/new" || g.Name != "Investors" || g.Type != contacts.GroupTypeUser {
		t.Errorf("CreateGroup = %+v", g)
	}
}

// modifyMembersCase is one group reference and the request or error it must produce.
type modifyMembersCase struct {
	group, wantTarget, wantErr string
	wantListed                 bool
}

// assertModifyOutcome checks either the expected error (with no modify request sent) or the modify request path and body.
func assertModifyOutcome(t *testing.T, env *testEnv, wantErr, wantTarget string, err error) {
	t.Helper()

	if wantErr != "" {
		if err == nil || err.Error() != wantErr {
			t.Fatalf("err = %v, want %q", err, wantErr)
		}

		if n := env.fake.count(routeModify); n != 0 {
			t.Errorf("modify calls = %d, want 0", n)
		}

		return
	}

	if err != nil {
		t.Fatalf("ModifyMembers: %v", err)
	}

	_, body, _, path := env.fake.last(routeModify)
	if path != wantTarget {
		t.Errorf("path = %q, want %q", path, wantTarget)
	}

	var req people.ModifyContactGroupMembersRequest
	if err := json.Unmarshal(body, &req); err != nil {
		t.Fatalf("decode: %v", err)
	}

	if len(req.ResourceNamesToAdd) != 1 || req.ResourceNamesToAdd[0] != testContactA ||
		len(req.ResourceNamesToRemove) != 1 || req.ResourceNamesToRemove[0] != "people/b" {
		t.Errorf("modify body = %s", body)
	}
}

func TestModifyMembers_ByName(t *testing.T) {
	t.Parallel()

	groups := []*people.ContactGroup{
		{ResourceName: "contactGroups/1", Name: testGroupFriendsClub, FormattedName: testGroupFriendsClub, GroupType: googleGroupUser},
		{ResourceName: "contactGroups/friends", Name: "friends", FormattedName: "Friends", GroupType: "SYSTEM_CONTACT_GROUP"},
		{ResourceName: "contactGroups/2", Name: "Dup", FormattedName: "Dup", GroupType: googleGroupUser},
		{ResourceName: "contactGroups/3", Name: "dup", FormattedName: "dup", GroupType: googleGroupUser},
	}

	cases := []modifyMembersCase{
		{group: "contactGroups/xyz", wantTarget: "/v1/contactGroups/xyz/members:modify"},
		{group: testGroupFriendsClub, wantTarget: "/v1/contactGroups/1/members:modify", wantListed: true},
		{group: "fRiEnDs cLuB", wantTarget: "/v1/contactGroups/1/members:modify", wantListed: true},
		{group: "friends", wantErr: `group "friends" not found`, wantListed: true},
		{group: "DUP", wantErr: `group "DUP" is ambiguous (2 matches)`, wantListed: true},
		{group: "missing", wantErr: `group "missing" not found`, wantListed: true},
	}
	for _, tc := range cases {
		t.Run(tc.group, func(t *testing.T) {
			t.Parallel()

			env := newTestEnv(t)
			env.fake.groups = groups

			err := env.people(t).ModifyMembers(context.Background(), tc.group, []string{testContactA}, []string{"people/b"})
			if listed := env.fake.count(routeListGroups) > 0; listed != tc.wantListed {
				t.Errorf("listed groups = %v, want %v", listed, tc.wantListed)
			}

			assertModifyOutcome(t, env, tc.wantErr, tc.wantTarget, err)
		})
	}
}

func TestModifyMembers_ReportsNotFoundMembers(t *testing.T) {
	t.Parallel()

	env := newTestEnv(t)
	env.fake.override = func(w http.ResponseWriter, _ *http.Request, rt string) bool {
		if rt != routeModify {
			return false
		}

		env.fake.json(w, people.ModifyContactGroupMembersResponse{NotFoundResourceNames: []string{testContactA}})

		return true
	}

	err := env.people(t).ModifyMembers(context.Background(), "contactGroups/1", []string{testContactA}, nil)
	if !errors.Is(err, contacts.ErrNotFound) || !strings.Contains(err.Error(), testContactA) {
		t.Errorf("err = %v, want ErrNotFound naming people/a", err)
	}
}

func TestMalformedJSON(t *testing.T) {
	t.Parallel()

	env := newTestEnv(t)

	env.fake.override = func(w http.ResponseWriter, _ *http.Request, rt string) bool {
		if rt != routeGet {
			return false
		}

		w.Header().Set("Content-Type", "application/json")
		env.fake.write(w, `{"resourceName": "people/c1", "names": [`)

		return true
	}
	if _, err := env.people(t).Get(context.Background(), "people/c1"); err == nil {
		t.Fatal("err = nil, want a decode error")
	}
}

func TestSlowResponse(t *testing.T) {
	t.Parallel()

	env := newTestEnv(t)
	release := make(chan struct{})

	t.Cleanup(func() { close(release) })

	env.fake.override = func(_ http.ResponseWriter, r *http.Request, rt string) bool {
		if rt != routeGet {
			return false
		}

		select {
		case <-r.Context().Done():
		case <-release:
		}

		return true
	}

	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()

	done := make(chan error, 1)

	go func() {
		_, err := env.people(t).Get(ctx, "people/c1")
		done <- err
	}()

	select {
	case err := <-done:
		if !errors.Is(err, context.DeadlineExceeded) {
			t.Errorf("err = %v, want context.DeadlineExceeded", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("Get did not return after its context expired")
	}
}

func TestErrorsNeverCarryQuery(t *testing.T) {
	t.Parallel()

	env := newTestEnv(t)
	// Skip the warmup so the failing request is the one carrying the query.
	env.up.warmed.Store(env.userID, true)
	// Dropping the connection makes net/http return a *url.Error, whose text
	// holds the request URL; a cancelled context would not, since gensupport
	// then returns the bare context error.
	env.fake.override = func(w http.ResponseWriter, _ *http.Request, rt string) bool {
		if rt != routeSearch {
			return false
		}

		hj, ok := w.(http.Hijacker)
		if !ok {
			t.Error("response writer is not a Hijacker")
			return false
		}

		conn, _, err := hj.Hijack()
		if err != nil {
			t.Errorf("Hijack: %v", err)
			return true
		}

		if err := conn.Close(); err != nil {
			t.Errorf("Close: %v", err)
		}

		return true
	}

	_, err := env.people(t).Search(context.Background(), "secret-name", 5)
	if err == nil {
		t.Fatal("err = nil")
	}

	if n := env.fake.count(routeSearch); n < 1 {
		t.Fatalf("search calls = %d, want the real query sent", n)
	}

	if strings.Contains(err.Error(), "secret-name") {
		t.Errorf("error leaks the search query: %v", err)
	}
}

type hookCall struct {
	method string
	code   int
}

func TestCallHook_ObservesEveryAttempt(t *testing.T) {
	t.Parallel()

	env := newTestEnv(t)

	var calls []hookCall

	env.up.onCall = func(_ context.Context, method string, code int, d time.Duration) {
		if d < 0 {
			t.Errorf("negative duration %v", d)
		}

		calls = append(calls, hookCall{method, code})
	}
	env.fake.override = func(w http.ResponseWriter, _ *http.Request, rt string) bool {
		if rt == routeGet && env.fake.count(routeGet) == 1 {
			googleError(w, http.StatusInternalServerError, "INTERNAL")
			return true
		}

		return false
	}

	p := env.people(t)
	if _, err := p.Get(context.Background(), "people/c9"); err != nil {
		t.Fatalf("Get: %v", err)
	}

	if err := p.Delete(context.Background(), "people/c9"); err != nil {
		t.Fatalf("Delete: %v", err)
	}

	want := []hookCall{{"get", http.StatusInternalServerError}, {"get", http.StatusOK}, {opDelete, http.StatusOK}}
	if fmt.Sprint(calls) != fmt.Sprint(want) {
		t.Fatalf("hook calls = %v, want %v", calls, want)
	}
}

func TestUpstreamStatusError(t *testing.T) {
	t.Parallel()

	err := mapError("list", &googleapi.Error{Code: http.StatusBadGateway, Body: "secret body"})

	var ue contacts.UpstreamStatusError
	if !errors.As(err, &ue) || ue.Status != http.StatusBadGateway || ue.Op != "list" {
		t.Fatalf("mapError = %#v, want UpstreamStatusError list 502", err)
	}

	if strings.Contains(err.Error(), "secret") {
		t.Fatalf("error carries the body: %v", err)
	}

	if callStatus(errors.New("dial tcp: refused")) != statusNoResponse {
		t.Fatal("transport error must report no status")
	}
}

func TestMapError_TransportErrors(t *testing.T) {
	t.Parallel()

	const queryURL = "https://people.googleapis.com/v1/people:searchContacts?query=private-name"

	cases := []struct {
		name string
		err  error
	}{
		{"dns", &url.Error{Op: testURLErrOpGet, URL: queryURL, Err: &net.OpError{
			Op: "dial", Net: "tcp",
			Err: &net.DNSError{Err: "no such host", Name: "people.googleapis.com", IsNotFound: true},
		}}},
		{"refused", &url.Error{Op: testURLErrOpGet, URL: queryURL, Err: &net.OpError{
			Op: "dial", Net: "tcp",
			Err: syscall.ECONNREFUSED,
		}}},
		{"client timeout", &url.Error{Op: testURLErrOpGet, URL: queryURL, Err: os.ErrDeadlineExceeded}},
		{"dropped", &url.Error{Op: testURLErrOpGet, URL: queryURL, Err: io.EOF}},
		{"deadline", context.DeadlineExceeded},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			err := mapError(opSearch, tc.err)

			if _, ok := errors.AsType[contacts.UnreachableError](err); !ok {
				t.Fatalf("mapError = %T %v, want UnreachableError", err, err)
			}

			if strings.Contains(err.Error(), "private-name") || strings.Contains(err.Error(), "https://") {
				t.Errorf("error carries the URL: %v", err)
			}
		})
	}
	// Google answered: not a transport error.
	if err := mapError(opSearch, &url.Error{Op: testURLErrOpGet, URL: queryURL, Err: errors.New("token endpoint returned HTTP 400")}); errors.As(err, new(contacts.UnreachableError)) {
		t.Errorf("non-network url.Error mapped to unreachable: %v", err)
	}
}

func TestMapError_Canceled(t *testing.T) {
	t.Parallel()

	err := mapError(opGet, context.Canceled)
	if !errors.Is(err, context.Canceled) || errors.As(err, new(contacts.UnreachableError)) {
		t.Errorf("mapError(canceled) = %v, want context.Canceled, not unreachable", err)
	}
}

func TestGet_ConnectionRefusedIsUnreachable(t *testing.T) {
	t.Parallel()

	env := newTestEnv(t)
	closed := httptest.NewServer(http.NotFoundHandler())
	closed.Close()
	env.up.peopleURL = closed.URL + "/"
	_, err := env.people(t).Get(context.Background(), "people/c1")

	if _, ok := errors.AsType[contacts.UnreachableError](err); !ok {
		t.Fatalf("err = %T %v, want UnreachableError", err, err)
	}

	if strings.Contains(err.Error(), "people/c1") || strings.Contains(err.Error(), "http://") {
		t.Errorf("error carries the request URL: %v", err)
	}
}

func TestGet_CanceledContext(t *testing.T) {
	t.Parallel()

	env := newTestEnv(t)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	_, err := env.people(t).Get(ctx, "people/c1")
	if !errors.Is(err, context.Canceled) || errors.As(err, new(contacts.UnreachableError)) {
		t.Errorf("err = %v, want context.Canceled", err)
	}
}
