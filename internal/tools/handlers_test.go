package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"reflect"
	"slices"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/auth"
	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/samber/google-contacts-mcp/internal/contacts"
	"github.com/samber/google-contacts-mcp/internal/limits"
)

const (
	// successContentBlocks is the text blocks of a successful result: the
	// serialized structured output only.
	successContentBlocks = 1

	testUserID = "user-1"
	testToken  = "good-token"
	// testMetadataURL is what the 401 header must point to.
	testMetadataURL = "https://mcp.example.com/.well-known/oauth-protected-resource"

	// testGivenName is the given name shared by most fixture contacts.
	testGivenName = "Ada"
	// testFamilyName is the family name used when a test enriches a contact.
	testFamilyName = "Lovelace"
	// testJobTitle is the job title written by update and upsert tests.
	testJobTitle = "Countess"
	// testEmail is the primary e-mail address of fixture contacts.
	testEmail = "ada@x.io"
	// testResourceName is a well-formed contact resource name.
	testResourceName = "people/c1"
	// testGroupName is a user group name as a caller types it.
	testGroupName = "Investors"
	// testGroupNameLower is testGroupName in lower case, to check case-insensitive group lookup.
	testGroupNameLower = "investors"
	// testTypeWork is the label type of work e-mail addresses.
	testTypeWork = "work"
	// testCompany is the company of fixture contacts.
	testCompany = "Analytical"
	// testGroupResourceName is the resource name of the fixture user group.
	testGroupResourceName = "contactGroups/inv"
	// toolCreateContactGroup is the MCP tool name that creates a contact group.
	toolCreateContactGroup = "create_contact_group"
	// toolSearchContacts is the MCP tool name that searches contacts.
	toolSearchContacts = "search_contacts"
	// keyStatus is the structured-log and metric label key holding the call outcome.
	keyStatus = "status"

	// Tool argument names, matching the JSON schema property names.
	argResourceName = "resource_name"
	argQuery        = "query"
	argGivenName    = "given_name"
	argType         = "type"
	argAdd          = "add"
	argEmail        = "email"
	argGroup        = "group"
	argValue        = "value"
)

// fakePeople is an in-memory contacts.People. Update honours the field mask
// and the etag like Google does.
type fakePeople struct {
	mu       sync.Mutex
	contacts map[string]contacts.Contact
	groups   []contacts.Group
	seq      int
	calls    []string
	// searchStrip drops everything but emails and the resource name from
	// search results, as a partial readMask would.
	searchStrip bool
	// errs forces an error for a method name.
	errs map[string]error

	lastUpdateFields []string
	lastList         listArgs
	modifies         []modifyCall
}

type modifyCall struct {
	group       string
	add, remove []string
}

func newFakePeople() *fakePeople {
	return &fakePeople{contacts: map[string]contacts.Contact{}, errs: map[string]error{}}
}

func (f *fakePeople) record(method string) error {
	f.calls = append(f.calls, method)
	return f.errs[method]
}

func (f *fakePeople) put(c contacts.Contact) contacts.Contact {
	f.mu.Lock()
	defer f.mu.Unlock()

	if c.ResourceName == "" {
		f.seq++
		c.ResourceName = "people/c" + strconv.Itoa(f.seq)
	}

	c.Etag = "etag-" + strconv.Itoa(len(f.calls)) + "-" + c.ResourceName
	f.contacts[c.ResourceName] = c

	return c
}

func (f *fakePeople) lookup(rn string) (contacts.Contact, bool) {
	f.mu.Lock()
	defer f.mu.Unlock()

	c, ok := f.contacts[rn]

	return c, ok
}

func (f *fakePeople) stored(rn string) contacts.Contact {
	c, _ := f.lookup(rn)
	return c
}

func (f *fakePeople) setErr(method string, err error) {
	f.mu.Lock()
	defer f.mu.Unlock()

	f.errs[method] = err
}

func (f *fakePeople) setGroups(groups []contacts.Group) {
	f.mu.Lock()
	defer f.mu.Unlock()

	f.groups = groups
}

func (f *fakePeople) updateFields() []string {
	f.mu.Lock()
	defer f.mu.Unlock()

	return slices.Clone(f.lastUpdateFields)
}

type listArgs struct {
	pageSize         int
	pageToken, order string
}

func (f *fakePeople) listArgs() listArgs {
	f.mu.Lock()
	defer f.mu.Unlock()

	return f.lastList
}

func (f *fakePeople) modifyCalls() []modifyCall {
	f.mu.Lock()
	defer f.mu.Unlock()

	return slices.Clone(f.modifies)
}

func (f *fakePeople) callsOf(method string) int {
	f.mu.Lock()
	defer f.mu.Unlock()

	n := 0

	for _, c := range f.calls {
		if c == method {
			n++
		}
	}

	return n
}

func (f *fakePeople) writes() int {
	return f.callsOf("Create") + f.callsOf("Update") + f.callsOf("Delete") + f.callsOf("ModifyMembers") + f.callsOf("CreateGroup")
}

func (f *fakePeople) sorted() []contacts.Contact {
	out := make([]contacts.Contact, 0, len(f.contacts))
	for _, c := range f.contacts {
		out = append(out, c)
	}

	slices.SortFunc(out, func(a, b contacts.Contact) int { return strings.Compare(a.ResourceName, b.ResourceName) })

	return out
}

func (f *fakePeople) Search(_ context.Context, query string, limit int) ([]contacts.Contact, error) {
	f.mu.Lock()
	defer f.mu.Unlock()

	if err := f.record("Search"); err != nil {
		return nil, err
	}

	q := strings.ToLower(query)

	var out []contacts.Contact

	for _, c := range f.sorted() {
		match := strings.HasPrefix(strings.ToLower(c.GivenName), q)
		for _, e := range c.Emails {
			match = match || strings.HasPrefix(strings.ToLower(e.Value), q)
		}

		if !match {
			continue
		}

		if f.searchStrip {
			c = contacts.Contact{ResourceName: c.ResourceName, Emails: c.Emails, DisplayName: c.DisplayName}
		}

		out = append(out, c)
	}

	if limit > 0 && len(out) > limit {
		out = out[:limit]
	}

	return out, nil
}

func (f *fakePeople) List(_ context.Context, pageSize int, pageToken, sortOrder string) ([]contacts.Contact, string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()

	f.lastList = listArgs{pageSize: pageSize, pageToken: pageToken, order: sortOrder}
	if err := f.record("List"); err != nil {
		return nil, "", err
	}

	return f.sorted(), "next", nil
}

func (f *fakePeople) Get(_ context.Context, resourceName string) (contacts.Contact, error) {
	f.mu.Lock()
	defer f.mu.Unlock()

	if err := f.record("Get"); err != nil {
		return contacts.Contact{}, err
	}

	c, ok := f.contacts[resourceName]
	if !ok {
		return contacts.Contact{}, contacts.ErrNotFound
	}

	return c, nil
}

func (f *fakePeople) Create(_ context.Context, c contacts.Contact) (contacts.Contact, error) {
	f.mu.Lock()
	if err := f.record("Create"); err != nil {
		f.mu.Unlock()
		return contacts.Contact{}, err
	}
	f.mu.Unlock()

	c.ResourceName = ""

	return f.put(c), nil
}

func (f *fakePeople) Update(_ context.Context, c contacts.Contact, fields []string) (contacts.Contact, error) {
	f.mu.Lock()
	if err := f.record("Update"); err != nil {
		f.mu.Unlock()
		return contacts.Contact{}, err
	}

	f.lastUpdateFields = slices.Clone(fields)
	cur, ok := f.contacts[c.ResourceName]
	f.mu.Unlock()

	if !ok {
		return contacts.Contact{}, contacts.ErrNotFound
	}

	if c.Etag == "" || c.Etag != cur.Etag {
		return contacts.Contact{}, contacts.ErrConflict
	}

	if err := applyFields(&cur, c, fields); err != nil {
		return contacts.Contact{}, err
	}

	return f.put(cur), nil
}

// applyFields copies the masked fields of src onto dst, as Google applies an
// update mask. An unknown field name is an error.
func applyFields(dst *contacts.Contact, src contacts.Contact, fields []string) error {
	for _, field := range fields {
		switch field {
		case contacts.FieldNames:
			dst.GivenName, dst.FamilyName = src.GivenName, src.FamilyName
		case contacts.FieldOrganizations:
			dst.Company, dst.JobTitle = src.Company, src.JobTitle
		case contacts.FieldBiographies:
			dst.Notes = src.Notes
		case contacts.FieldEmailAddresses:
			dst.Emails = src.Emails
		case contacts.FieldPhoneNumbers:
			dst.Phones = src.Phones
		case contacts.FieldURLs:
			dst.Websites = src.Websites
		case contacts.FieldAddresses:
			dst.Addresses = src.Addresses
		case contacts.FieldMemberships:
			dst.Groups = src.Groups
		default:
			return fmt.Errorf("fake: unknown field %q", field)
		}
	}

	return nil
}

func (f *fakePeople) Delete(_ context.Context, resourceName string) error {
	f.mu.Lock()
	defer f.mu.Unlock()

	if err := f.record("Delete"); err != nil {
		return err
	}

	if _, ok := f.contacts[resourceName]; !ok {
		return contacts.ErrNotFound
	}

	delete(f.contacts, resourceName)

	return nil
}

func (f *fakePeople) ListGroups(_ context.Context) ([]contacts.Group, error) {
	f.mu.Lock()
	defer f.mu.Unlock()

	if err := f.record("ListGroups"); err != nil {
		return nil, err
	}

	return slices.Clone(f.groups), nil
}

func (f *fakePeople) CreateGroup(_ context.Context, name string) (contacts.Group, error) {
	f.mu.Lock()
	defer f.mu.Unlock()

	if err := f.record("CreateGroup"); err != nil {
		return contacts.Group{}, err
	}

	g := contacts.Group{ResourceName: "contactGroups/g" + strconv.Itoa(len(f.groups)+1), Name: name, Type: contacts.GroupTypeUser}
	f.groups = append(f.groups, g)

	return g, nil
}

func (f *fakePeople) ModifyMembers(ctx context.Context, group string, add, remove []string) error {
	f.mu.Lock()
	if err := f.record("ModifyMembers"); err != nil {
		f.mu.Unlock()
		return err
	}
	f.mu.Unlock()

	rn, err := contacts.ResolveGroup(ctx, f, group)
	if err != nil {
		return err
	}

	f.mu.Lock()
	defer f.mu.Unlock()

	f.modifies = append(f.modifies, modifyCall{group: rn, add: add, remove: remove})
	for _, r := range add {
		c, ok := f.contacts[r]
		if !ok {
			return fmt.Errorf("%w: %s", contacts.ErrNotFound, r)
		}

		c.Groups = append(c.Groups, rn)
		f.contacts[r] = c
	}

	return nil
}

// fakeFactory hands out the same fakePeople for the expected user.
type fakeFactory struct {
	people *fakePeople
	mu     sync.Mutex
	err    error
}

func (f *fakeFactory) setErr(err error) {
	f.mu.Lock()
	defer f.mu.Unlock()

	f.err = err
}

func (f *fakeFactory) People(_ context.Context, userID string) (contacts.People, error) {
	f.mu.Lock()
	err := f.err
	f.mu.Unlock()

	if err != nil {
		return nil, err
	}

	if userID != testUserID {
		return nil, fmt.Errorf("unexpected user %q", userID)
	}

	return f.people, nil
}

type fakeToucher struct {
	mu      sync.Mutex
	touched []string
}

func (f *fakeToucher) ids() []string {
	f.mu.Lock()
	defer f.mu.Unlock()

	return slices.Clone(f.touched)
}

func (f *fakeToucher) TouchUser(_ context.Context, id string) error {
	f.mu.Lock()
	defer f.mu.Unlock()

	f.touched = append(f.touched, id)

	return nil
}

// fakeVerifier accepts testToken only, with the uid Extra key oauth.UserID reads.
func fakeVerifier(_ context.Context, token string, _ *http.Request) (*auth.TokenInfo, error) {
	if token != testToken {
		return nil, fmt.Errorf("%w: unknown token", auth.ErrInvalidToken)
	}

	return &auth.TokenInfo{
		Scopes:     []string{"contacts"},
		Expiration: time.Now().Add(time.Hour),
		UserID:     testUserID,
		Extra:      map[string]any{"uid": testUserID},
	}, nil
}

type harness struct {
	t       *testing.T
	people  *fakePeople
	factory *fakeFactory
	toucher *fakeToucher
	srv     *httptest.Server
	session *mcp.ClientSession
}

type harnessOption func(*Deps)

func newHarness(t *testing.T, opts ...harnessOption) *harness {
	t.Helper()
	return newHarnessWrapped(t, nil, opts...)
}

// newHarnessWrapped is newHarness with wrap applied around the handler.
func newHarnessWrapped(t *testing.T, wrap func(http.Handler) http.Handler, opts ...harnessOption) *harness {
	t.Helper()
	h := &harness{t: t, people: newFakePeople(), toucher: &fakeToucher{}}
	h.factory = &fakeFactory{people: h.people}

	deps := Deps{
		People:              h.factory,
		Store:               h.toucher,
		Limiter:             limits.New(1000, 1000),
		Verifier:            fakeVerifier,
		ResourceMetadataURL: testMetadataURL,
		Version:             "test",
	}
	for _, o := range opts {
		o(&deps)
	}

	handler := NewHandler(deps)
	if wrap != nil {
		handler = wrap(handler)
	}

	h.srv = httptest.NewServer(handler)
	t.Cleanup(h.srv.Close)
	h.session = h.connect("")

	return h
}

// bearerTransport adds the test bearer token to every request.
type bearerTransport struct{ token string }

func (b bearerTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	r = r.Clone(r.Context())
	r.Header.Set("Authorization", "Bearer "+b.token)

	return http.DefaultTransport.RoundTrip(r)
}

func (h *harness) connect(version string) *mcp.ClientSession {
	h.t.Helper()

	client := mcp.NewClient(&mcp.Implementation{Name: "test-client", Version: "1"}, nil)
	transport := &mcp.StreamableClientTransport{
		Endpoint:             h.srv.URL,
		HTTPClient:           &http.Client{Transport: bearerTransport{token: testToken}},
		MaxRetries:           -1,
		DisableStandaloneSSE: true,
	}

	cs, err := client.Connect(context.Background(), transport, &mcp.ClientSessionOptions{ProtocolVersion: version})
	if err != nil {
		h.t.Fatalf("Connect(%q): %v", version, err)
	}

	h.t.Cleanup(func() {
		if err := cs.Close(); err != nil {
			h.t.Logf("close session: %v", err)
		}
	})

	return cs
}

// call runs a tool and returns its result; protocol errors fail the test.
func (h *harness) call(name string, args any) *mcp.CallToolResult {
	h.t.Helper()

	res, err := h.session.CallTool(context.Background(), &mcp.CallToolParams{Name: name, Arguments: args})
	if err != nil {
		h.t.Fatalf("CallTool(%s): %v", name, err)
	}

	return res
}

// ok runs a tool, requires success and decodes its structured output.
func (h *harness) ok(name string, args, out any) *mcp.CallToolResult {
	h.t.Helper()

	res := h.call(name, args)
	if res.IsError {
		h.t.Fatalf("%s: tool error %q", name, resultText(res))
	}

	requireSerializedOutput(h.t, name, res)

	if out != nil {
		raw, err := json.Marshal(res.StructuredContent)
		if err != nil {
			h.t.Fatalf("marshal structured content: %v", err)
		}

		if err := json.Unmarshal(raw, out); err != nil {
			h.t.Fatalf("decode %s output %s: %v", name, raw, err)
		}
	}

	return res
}

// fail runs a tool and requires a tool error with exactly want.
func (h *harness) fail(name string, args any, want string) {
	h.t.Helper()

	res := h.call(name, args)
	if !res.IsError {
		h.t.Fatalf("%s: IsError = false, want %q", name, want)
	}

	if got := resultText(res); got != want {
		h.t.Fatalf("%s: error = %q, want %q", name, got, want)
	}
}

func resultText(res *mcp.CallToolResult) string {
	var parts []string

	for _, c := range res.Content {
		if tc, ok := c.(*mcp.TextContent); ok {
			parts = append(parts, tc.Text)
		}
	}

	return strings.Join(parts, "\n")
}

// jsonText returns the text block of a successful result: the serialized
// structured output.
func jsonText(res *mcp.CallToolResult) string {
	if len(res.Content) == 0 {
		return ""
	}

	tc, ok := res.Content[0].(*mcp.TextContent)
	if !ok {
		return ""
	}

	return tc.Text
}

// requireSerializedOutput fails unless a successful result carries exactly one
// text block holding the JSON of its structured content, the only part of a
// result some clients hand to the model.
func requireSerializedOutput(t *testing.T, name string, res *mcp.CallToolResult) {
	t.Helper()

	if len(res.Content) != successContentBlocks {
		t.Fatalf("%s: %d content blocks, want %d", name, len(res.Content), successContentBlocks)
	}

	var fromText, fromStructured any
	if err := json.Unmarshal([]byte(jsonText(res)), &fromText); err != nil {
		t.Fatalf("%s: content JSON %q: %v", name, jsonText(res), err)
	}

	raw, err := json.Marshal(res.StructuredContent)
	if err != nil {
		t.Fatalf("%s: marshal structured content: %v", name, err)
	}

	if err := json.Unmarshal(raw, &fromStructured); err != nil {
		t.Fatalf("%s: decode structured content %s: %v", name, raw, err)
	}

	if !reflect.DeepEqual(fromText, fromStructured) {
		t.Errorf("%s: content JSON %s differs from structured content %s", name, jsonText(res), raw)
	}
}

func TestSearchContacts_ContentCarriesContacts(t *testing.T) {
	t.Parallel()

	h := newHarness(t)
	c := h.people.put(contacts.Contact{GivenName: testGivenName, Emails: []contacts.Typed{{Value: testEmail, Type: testTypeWork}}})

	res := h.ok(toolSearchContacts, map[string]any{argQuery: "ad"}, nil)

	var out SearchOutput
	if err := json.Unmarshal([]byte(jsonText(res)), &out); err != nil {
		t.Fatalf("content JSON %q: %v", jsonText(res), err)
	}

	if len(out.Contacts) != 1 || out.Contacts[0].ResourceName != c.ResourceName || out.Contacts[0].GivenName != testGivenName ||
		len(out.Contacts[0].Emails) != 1 || out.Contacts[0].Emails[0].Value != testEmail {
		t.Errorf("content contacts = %+v, want %s with %s", out.Contacts, c.ResourceName, testEmail)
	}
}

func TestReadTools_ContentMatchesStructuredContent(t *testing.T) {
	t.Parallel()

	h := newHarness(t)
	c := h.people.put(contacts.Contact{GivenName: testGivenName, Emails: []contacts.Typed{{Value: testEmail, Type: testTypeWork}}})
	h.people.setGroups([]contacts.Group{{ResourceName: "contactGroups/myContacts", Name: "My Contacts", Type: contacts.GroupTypeSystem, MemberCount: 1}})

	tests := []struct {
		tool string
		args map[string]any
	}{
		{toolSearchContacts, map[string]any{argQuery: "ad"}},
		{"list_contacts", map[string]any{}},
		{"get_contact", map[string]any{argResourceName: c.ResourceName}},
		{"list_contact_groups", map[string]any{}},
	}

	for _, tt := range tests {
		t.Run(tt.tool, func(t *testing.T) {
			t.Parallel()

			// h.ok already runs requireSerializedOutput; the explicit call
			// keeps this test failing on its own if that ever changes.
			res := h.ok(tt.tool, tt.args, nil)
			requireSerializedOutput(t, tt.tool, res)
		})
	}
}

func TestGetContact_Touch(t *testing.T) {
	t.Parallel()

	h := newHarness(t)
	c := h.people.put(contacts.Contact{GivenName: testGivenName, Emails: []contacts.Typed{{Value: testEmail, Type: testTypeWork}}})

	var out ContactOutput

	h.ok("get_contact", map[string]any{argResourceName: c.ResourceName}, &out)

	if out.ResourceName != c.ResourceName || out.GivenName != testGivenName || out.Etag != c.Etag || len(out.Emails) != 1 || out.Emails[0].Type != testTypeWork {
		t.Errorf("output = %+v", out)
	}

	if touched := h.toucher.ids(); len(touched) == 0 || touched[0] != testUserID {
		t.Errorf("touched = %v", touched)
	}

	h.fail("get_contact", map[string]any{argResourceName: "people/c999"}, "contact not found")
	h.fail("get_contact", map[string]any{argResourceName: "contactGroups/x"}, `resource_name must look like "people/c123"`)
}

func TestSearchContacts(t *testing.T) {
	t.Parallel()

	h := newHarness(t)
	h.people.put(contacts.Contact{GivenName: testGivenName})
	h.people.put(contacts.Contact{GivenName: "Adam"})
	h.people.put(contacts.Contact{GivenName: "Bob"})

	var out SearchOutput

	h.ok(toolSearchContacts, map[string]any{argQuery: "ad"}, &out)

	if len(out.Contacts) != 2 {
		t.Errorf("contacts = %+v", out.Contacts)
	}
}

func TestCreateContact_GroupsByName(t *testing.T) {
	t.Parallel()

	h := newHarness(t)
	h.people.setGroups([]contacts.Group{
		{ResourceName: testGroupResourceName, Name: testGroupName, Type: contacts.GroupTypeUser},
		{ResourceName: "contactGroups/friends", Name: "Friends", Type: contacts.GroupTypeSystem},
	})

	var out ContactOutput

	h.ok("create_contact", map[string]any{
		argGivenName: testGivenName,
		"emails":     []map[string]string{{argValue: testEmail, argType: testTypeWork}},
		"websites":   []map[string]string{{argValue: "https://linkedin.com/in/ada", argType: "linkedin"}},
		"groups":     []string{testGroupNameLower, "contactGroups/raw"},
	}, &out)

	if out.ResourceName == "" {
		t.Errorf("out = %+v", out)
	}

	stored := h.people.stored(out.ResourceName)
	if !slices.Equal(stored.Groups, []string{testGroupResourceName, "contactGroups/raw"}) {
		t.Errorf("groups = %v", stored.Groups)
	}

	if len(stored.Websites) != 1 || stored.Websites[0].Type != contacts.WebsiteTypeLinkedIn {
		t.Errorf("websites = %+v", stored.Websites)
	}

	writes := h.people.writes()
	h.fail("create_contact", map[string]any{argGivenName: "X", "groups": []string{"friends"}}, `group "friends" not found`)

	if h.people.writes() != writes {
		t.Error("create ran despite an unknown group")
	}
}

func TestUpdateContact_OnlyProvidedFields(t *testing.T) {
	t.Parallel()

	h := newHarness(t)
	c := h.people.put(contacts.Contact{
		GivenName: testGivenName, FamilyName: testFamilyName, Company: testCompany, Notes: "keep me",
		Emails: []contacts.Typed{{Value: testEmail}},
		Phones: []contacts.Typed{{Value: "+33 1"}},
	})

	var out ContactOutput

	h.ok("update_contact", map[string]any{
		argResourceName: c.ResourceName,
		"job_title":     testJobTitle,
		"notes":         "",
		"phones":        []map[string]string{{argValue: "+44 2", argType: "mobile"}},
	}, &out)

	want := []string{contacts.FieldBiographies, contacts.FieldOrganizations, contacts.FieldPhoneNumbers}
	if !slices.Equal(h.people.updateFields(), want) {
		t.Errorf("fields = %v, want %v", h.people.updateFields(), want)
	}

	stored := h.people.stored(c.ResourceName)
	if stored.GivenName != testGivenName || stored.FamilyName != testFamilyName || stored.Company != testCompany || stored.JobTitle != testJobTitle {
		t.Errorf("stored = %+v", stored)
	}

	if stored.Notes != "" {
		t.Errorf("notes = %q, want cleared", stored.Notes)
	}

	if len(stored.Phones) != 1 || stored.Phones[0].Value != "+44 2" || len(stored.Emails) != 1 {
		t.Errorf("phones = %+v emails = %+v", stored.Phones, stored.Emails)
	}

	if h.people.callsOf("Get") != 1 {
		t.Errorf("Get calls = %d, want 1 (fresh etag)", h.people.callsOf("Get"))
	}

	h.fail("update_contact", map[string]any{argResourceName: c.ResourceName}, "no field to update")
}

func TestUpdateContact_Conflict(t *testing.T) {
	t.Parallel()

	h := newHarness(t)
	c := h.people.put(contacts.Contact{GivenName: testGivenName})
	h.people.setErr("Update", contacts.ErrConflict)
	h.fail("update_contact", map[string]any{argResourceName: c.ResourceName, argGivenName: "Bea"}, "contact changed, re-read it")

	if n := h.people.callsOf("Update"); n != 1 {
		t.Errorf("Update calls = %d, want 1 (no silent retry)", n)
	}
}

func TestDeleteContact(t *testing.T) {
	t.Parallel()

	h := newHarness(t)
	c := h.people.put(contacts.Contact{GivenName: testGivenName})

	var out DeleteOutput

	h.ok("delete_contact", map[string]any{argResourceName: c.ResourceName}, &out)

	if !out.Deleted || out.ResourceName != c.ResourceName {
		t.Errorf("out = %+v", out)
	}

	if _, ok := h.people.lookup(c.ResourceName); ok {
		t.Error("contact still stored")
	}

	h.fail("delete_contact", map[string]any{argResourceName: c.ResourceName}, "contact not found")
}

func TestListContacts_PageSizeClamp(t *testing.T) {
	t.Parallel()

	cases := []struct {
		in   any
		want int
	}{{nil, 100}, {0, 100}, {-5, 100}, {1, 1}, {1000, 1000}, {5000, 1000}}
	for _, tc := range cases {
		t.Run(fmt.Sprint(tc.in), func(t *testing.T) {
			t.Parallel()

			h := newHarness(t)

			args := map[string]any{}
			if tc.in != nil {
				args["page_size"] = tc.in
			}

			var out ListOutput
			h.ok("list_contacts", args, &out)

			if h.people.listArgs().pageSize != tc.want {
				t.Errorf("page size = %d, want %d", h.people.listArgs().pageSize, tc.want)
			}

			if out.NextPageToken != "next" {
				t.Errorf("next = %q", out.NextPageToken)
			}
		})
	}
}

func TestListContacts_Sort(t *testing.T) {
	t.Parallel()

	h := newHarness(t)
	h.ok("list_contacts", map[string]any{"sort": "LAST_MODIFIED_DESCENDING", "page_token": "tok"}, nil)

	if h.people.listArgs().order != "LAST_MODIFIED_DESCENDING" || h.people.listArgs().pageToken != "tok" {
		t.Errorf("list = %+v", h.people.listArgs())
	}

	h.fail("list_contacts", map[string]any{"sort": "RANDOM"}, "sort must be one of FIRST_NAME_ASCENDING, LAST_NAME_ASCENDING, LAST_MODIFIED_DESCENDING")
}

func TestListContacts_BadPageToken(t *testing.T) {
	t.Parallel()

	h := newHarness(t)
	h.people.setErr("List", fmt.Errorf("people list: google returned HTTP 400: %w", contacts.ErrInvalidArgument))
	h.fail("list_contacts", map[string]any{"page_token": "bogus"}, "invalid page_token")
}

func TestRateLimitedToolError(t *testing.T) {
	t.Parallel()

	h := newHarness(t, func(d *Deps) { d.Limiter = limits.New(1000, 1) })
	c := h.people.put(contacts.Contact{GivenName: testGivenName})
	h.ok("update_contact", map[string]any{argResourceName: c.ResourceName, argGivenName: "Bea"}, nil)

	res := h.call("update_contact", map[string]any{argResourceName: c.ResourceName, argGivenName: "Cy"})
	if !res.IsError || !strings.HasPrefix(resultText(res), "rate limited, retry after ") || !strings.HasSuffix(resultText(res), "s") {
		t.Fatalf("result = %v %q", res.IsError, resultText(res))
	}

	if n := h.people.callsOf("Update"); n != 1 {
		t.Errorf("Update calls = %d, want 1", n)
	}
	// Reads use their own bucket.
	h.ok("get_contact", map[string]any{argResourceName: c.ResourceName}, nil)

	h.people.setErr("Get", contacts.RateLimitedError{RetryAfter: 1500 * time.Millisecond})
	h.fail("get_contact", map[string]any{argResourceName: c.ResourceName}, "rate limited, retry after 2s")
}

func TestUpstreamAuthMessage(t *testing.T) {
	t.Parallel()

	h := newHarness(t)
	h.factory.setErr(contacts.ErrUpstreamAuth)
	h.fail(toolSearchContacts, map[string]any{argQuery: "a"}, "Google access was revoked; reconnect this connector")
	h.factory.setErr(nil)
	h.people.setErr("Get", fmt.Errorf("people get: %w", contacts.ErrUpstreamAuth))
	h.fail("get_contact", map[string]any{argResourceName: testResourceName}, "Google access was revoked; reconnect this connector")
}

func TestContactGroups(t *testing.T) {
	t.Parallel()

	h := newHarness(t)
	h.people.setGroups([]contacts.Group{{ResourceName: "contactGroups/myContacts", Name: "My Contacts", Type: contacts.GroupTypeSystem, MemberCount: 4}})

	var created GroupOutput
	h.ok(toolCreateContactGroup, map[string]any{"name": testGroupName}, &created)

	if created.ResourceName == "" || created.Name != testGroupName || created.Type != contacts.GroupTypeUser {
		t.Errorf("created = %+v", created)
	}

	var list GroupsOutput

	h.ok("list_contact_groups", map[string]any{}, &list)

	if len(list.Groups) != 2 || list.Groups[0].MemberCount != 4 {
		t.Errorf("groups = %+v", list.Groups)
	}

	c := h.people.put(contacts.Contact{GivenName: testGivenName})

	var mod ModifyMembersOutput
	h.ok("modify_contact_group_members", map[string]any{argGroup: testGroupNameLower, argAdd: []string{c.ResourceName}}, &mod)

	if mod.Added != 1 || mod.Removed != 0 {
		t.Errorf("modify = %+v", mod)
	}

	h.fail("modify_contact_group_members", map[string]any{argGroup: "nope", argAdd: []string{c.ResourceName}}, `group "nope" not found`)
	h.fail("modify_contact_group_members", map[string]any{argGroup: testGroupNameLower, argAdd: []string{"people/c999"}}, "contact not found")
	h.fail("modify_contact_group_members", map[string]any{argGroup: testGroupNameLower}, "add or remove must list at least one contact")
	h.people.setErr("ModifyMembers", contacts.ErrNotFound)
	h.fail("modify_contact_group_members", map[string]any{argGroup: "contactGroups/gone", argAdd: []string{c.ResourceName}}, "group not found")
}

func TestToolErrorsHideUnknownCauses(t *testing.T) {
	t.Parallel()

	h := newHarness(t)
	h.people.setErr("Search", contacts.UpstreamStatusError{Op: "search", Status: 503})
	h.fail(toolSearchContacts, map[string]any{argQuery: "secret-query"}, "people search: google returned HTTP 503")
}

func TestToolAnnotations(t *testing.T) {
	t.Parallel()

	srv := NewServer(Deps{People: &fakeFactory{people: newFakePeople()}, Store: &fakeToucher{}, Limiter: limits.New(10, 10), Version: "test"})
	st, ct := mcp.NewInMemoryTransports()
	ctx := context.Background()

	ss, err := srv.Connect(ctx, st, nil)
	if err != nil {
		t.Fatalf("server connect: %v", err)
	}

	t.Cleanup(func() {
		if err := ss.Close(); err != nil {
			t.Logf("close: %v", err)
		}
	})

	cs, err := mcp.NewClient(&mcp.Implementation{Name: "c", Version: "1"}, nil).Connect(ctx, ct, nil)
	if err != nil {
		t.Fatalf("client connect: %v", err)
	}

	t.Cleanup(func() {
		if err := cs.Close(); err != nil {
			t.Logf("close: %v", err)
		}
	})

	res, err := cs.ListTools(ctx, nil)
	if err != nil {
		t.Fatalf("ListTools: %v", err)
	}

	reads := []string{toolSearchContacts, "list_contacts", "get_contact", "list_contact_groups"}
	writes := []string{"create_contact", "update_contact", "upsert_contact_by_email", toolCreateContactGroup, "modify_contact_group_members"}

	got := map[string]*mcp.Tool{}
	for _, tool := range res.Tools {
		got[tool.Name] = tool
	}

	if len(got) != len(reads)+len(writes)+1 {
		t.Errorf("tools = %d, want %d", len(got), len(reads)+len(writes)+1)
	}

	checkReadAnnotations(t, got, reads)
	checkWriteAnnotations(t, got, writes)
	checkDeleteAnnotations(t, got)

	for name, tool := range got {
		if tool.OutputSchema == nil {
			t.Errorf("%s: no output schema", name)
		}

		checkDirectoryMetadata(t, name, tool)
	}
}

// checkDirectoryMetadata requires what both directories reject tools without:
// Claude's wants a title, OpenAI's wants every hint set explicitly.
func checkDirectoryMetadata(t *testing.T, name string, tool *mcp.Tool) {
	t.Helper()

	if tool.Title == "" || tool.Annotations == nil || tool.Annotations.Title != tool.Title {
		t.Errorf("%s: want title on the tool and in its annotations", name)
	}

	if tool.Annotations == nil || tool.Annotations.DestructiveHint == nil ||
		tool.Annotations.OpenWorldHint == nil || *tool.Annotations.OpenWorldHint {
		t.Errorf("%s: want explicit destructiveHint and openWorldHint false", name)
	}
}

// checkReadAnnotations requires the read-only hint on every named tool.
func checkReadAnnotations(t *testing.T, got map[string]*mcp.Tool, names []string) {
	t.Helper()

	for _, name := range names {
		tool := got[name]
		if tool == nil || tool.Annotations == nil || !tool.Annotations.ReadOnlyHint {
			t.Errorf("%s: want readOnlyHint", name)
		}
	}
}

// checkWriteAnnotations requires a non-destructive write hint and the
// address-book warning on every named tool.
func checkWriteAnnotations(t *testing.T, got map[string]*mcp.Tool, names []string) {
	t.Helper()

	for _, name := range names {
		tool := got[name]
		if tool == nil || tool.Annotations == nil || tool.Annotations.ReadOnlyHint ||
			tool.Annotations.DestructiveHint == nil || *tool.Annotations.DestructiveHint {
			t.Errorf("%s: want destructiveHint false", name)
			continue
		}

		if !strings.Contains(tool.Description, "real address book") {
			t.Errorf("%s: description lacks the address book warning", name)
		}
	}
}

// checkDeleteAnnotations requires the destructive hint on delete_contact.
func checkDeleteAnnotations(t *testing.T, got map[string]*mcp.Tool) {
	t.Helper()

	del := got["delete_contact"]
	if del == nil || del.Annotations == nil || del.Annotations.ReadOnlyHint ||
		del.Annotations.DestructiveHint == nil || !*del.Annotations.DestructiveHint {
		t.Error("delete_contact: want destructiveHint true")
	}
}

func TestCheckResourceName(t *testing.T) {
	t.Parallel()

	for _, rn := range []string{testResourceName, "people/c1234567890123"} {
		if err := checkResourceName(rn); err != nil {
			t.Errorf("checkResourceName(%q) = %v, want nil", rn, err)
		}
	}

	for _, rn := range []string{
		"", "people/", "people/c", "people/me", "people/c1?x=1", "people/../x", "people/c1/x",
		"people/c1#x", "people/1", "people/cabc", "otherContacts/c1", "contactGroups/x", " people/c1", "people/c1\n",
	} {
		if err := checkResourceName(rn); err == nil {
			t.Errorf("checkResourceName(%q) = nil, want an error", rn)
		}
	}
}

func TestCheckGroup(t *testing.T) {
	t.Parallel()

	for _, g := range []string{"contactGroups/myContacts", "contactGroups/3a4b_c-d", testGroupNameLower, "Family & friends"} {
		if err := checkGroup(g); err != nil {
			t.Errorf("checkGroup(%q) = %v, want nil", g, err)
		}
	}

	for _, g := range []string{"contactGroups/", "contactGroups/../x", "contactGroups/a?b=1", "contactGroups/a/members", "contactGroups/a b"} {
		if err := checkGroup(g); err == nil {
			t.Errorf("checkGroup(%q) = nil, want an error", g)
		}
	}
}

func TestResourceNames_RejectedBeforeGoogle(t *testing.T) {
	t.Parallel()

	h := newHarness(t)
	for _, rn := range []string{"people/me", "people/c1?x=1", "people/../x"} {
		h.fail("get_contact", map[string]any{argResourceName: rn}, `resource_name must look like "people/c123"`)
		h.fail("delete_contact", map[string]any{argResourceName: rn}, `resource_name must look like "people/c123"`)
		h.fail("update_contact", map[string]any{argResourceName: rn, argGivenName: "X"}, `resource_name must look like "people/c123"`)
		h.fail("modify_contact_group_members", map[string]any{argGroup: "contactGroups/abc", argAdd: []string{rn}},
			`resource_name must look like "people/c123"`)
	}

	wantGroup := `group resource name must look like "contactGroups/abc"`
	h.fail("modify_contact_group_members", map[string]any{argGroup: "contactGroups/../x", argAdd: []string{testResourceName}}, wantGroup)
	h.fail("create_contact", map[string]any{argGivenName: "A", "groups": []string{"contactGroups/a?b=1"}}, wantGroup)
	h.fail("upsert_contact_by_email", map[string]any{argEmail: "a@x.io", argGroup: "contactGroups/a/b"}, wantGroup)

	for _, m := range []string{"Get", "Delete", "Update", "ModifyMembers", "Create", "Search", "ListGroups"} {
		if n := h.people.callsOf(m); n != 0 {
			t.Errorf("%s called %d times for an invalid resource name", m, n)
		}
	}
}
