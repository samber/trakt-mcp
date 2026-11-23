package google

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"

	"google.golang.org/api/googleapi"
	"google.golang.org/api/people/v1"

	"github.com/samber/trakt-mcp/internal/contacts"
)

const (
	// personFields is the readMask/personFields of every person read: the
	// fields contacts.Contact maps, plus metadata for the CONTACT source etag.
	personFields = "names,emailAddresses,phoneNumbers,organizations,urls,addresses,biographies,memberships,metadata"
	// groupFields is the groupFields/readGroupFields of every group read.
	groupFields = "groupType,memberCount,name"
	// selfResource names the authenticated user in connections.list.
	selfResource = "people/me"
	// maxSearchPageSize is the People API maximum for searchContacts.
	maxSearchPageSize = 30
	// maxListPageSize is the People API maximum for connections.list.
	maxListPageSize = 1000
	// maxGroupPageSize is the People API maximum for contactGroups.list.
	maxGroupPageSize = 1000

	// defaultRetryAfter is used when Google sends 429 without Retry-After.
	defaultRetryAfter = 30 * time.Second
	// retryAfterHeader carries Google's back-off hint on 429.
	retryAfterHeader = "Retry-After"
	// reasonFailedPrecondition is the error reason of a stale etag on update.
	reasonFailedPrecondition = "failedPrecondition"
	// statusFailedPrecondition is the same condition in the v2 error format.
	statusFailedPrecondition = "FAILED_PRECONDITION"

	// Google contact group types, mapped to contacts.GroupTypeUser/System.
	googleGroupUser   = "USER_CONTACT_GROUP"
	googleGroupSystem = "SYSTEM_CONTACT_GROUP"

	// statusNoResponse is the status reported for a call that got no HTTP
	// response.
	statusNoResponse = 0

	// People API operation names, used in errors and as the metrics method
	// label.
	opSearchWarmup  = "search warmup"
	opSearch        = "search"
	opList          = "list"
	opGet           = "get"
	opListGroups    = "list groups"
	opCreate        = "create"
	opUpdate        = "update"
	opDelete        = "delete"
	opCreateGroup   = "create group"
	opModifyMembers = "modify group members"
)

// CallHook observes one People API call: its method (the operation name),
// HTTP status (0 without a response) and duration. ctx is the caller's.
type CallHook func(ctx context.Context, method string, code int, d time.Duration)

// peopleClient implements contacts.People for one user.
type peopleClient struct {
	svc    *people.Service
	userID string
	warmed *sync.Map
	// onCall observes every People API call; nil disables it.
	onCall CallHook
	// tokens is the client's token source, refreshed on a People API 401.
	tokens *clientTokenSource
}

var _ contacts.People = (*peopleClient)(nil)

// Search runs searchContacts. Google serves it from a lazily built cache, so
// the first search of a user in this process is preceded by an empty-query
// warmup. The real query follows at once: its results may still lag right
// after the warmup, which Google documents as a few seconds.
func (p *peopleClient) Search(ctx context.Context, query string, limit int) ([]contacts.Contact, error) {
	if _, done := p.warmed.Load(p.userID); !done {
		if _, err := retryRead(ctx, p, opSearchWarmup, func() (*people.SearchResponse, error) {
			return p.svc.People.SearchContacts().Query("").ReadMask(personFields).Context(ctx).Do()
		}); err != nil {
			return nil, err
		}

		p.warmed.Store(p.userID, true)
	}

	if limit <= 0 || limit > maxSearchPageSize {
		limit = maxSearchPageSize
	}

	resp, err := retryRead(ctx, p, opSearch, func() (*people.SearchResponse, error) {
		return p.svc.People.SearchContacts().Query(query).ReadMask(personFields).PageSize(int64(limit)).Context(ctx).Do()
	})
	if err != nil {
		return nil, err
	}

	out := make([]contacts.Contact, 0, len(resp.Results))
	for _, r := range resp.Results {
		if r != nil && r.Person != nil {
			out = append(out, contacts.FromPerson(r.Person))
		}
	}

	return out, nil
}

// List returns one page of the user's contacts and the next page token, ""
// on the last page. pageSize <= 0 and sortOrder "" keep Google's defaults.
func (p *peopleClient) List(ctx context.Context, pageSize int, pageToken, sortOrder string) ([]contacts.Contact, string, error) {
	resp, err := retryRead(ctx, p, opList, func() (*people.ListConnectionsResponse, error) {
		call := p.svc.People.Connections.List(selfResource).PersonFields(personFields).Context(ctx)
		if pageSize > 0 {
			call = call.PageSize(int64(min(pageSize, maxListPageSize)))
		}

		if pageToken != "" {
			call = call.PageToken(pageToken)
		}

		if sortOrder != "" {
			call = call.SortOrder(sortOrder)
		}

		return call.Do()
	})
	if err != nil {
		return nil, "", err
	}

	out := make([]contacts.Contact, 0, len(resp.Connections))
	for _, person := range resp.Connections {
		if person != nil {
			out = append(out, contacts.FromPerson(person))
		}
	}

	return out, resp.NextPageToken, nil
}

// Get reads one contact.
func (p *peopleClient) Get(ctx context.Context, resourceName string) (contacts.Contact, error) {
	person, err := retryRead(ctx, p, opGet, func() (*people.Person, error) {
		return p.svc.People.Get(resourceName).PersonFields(personFields).Context(ctx).Do()
	})
	if err != nil {
		return contacts.Contact{}, err
	}

	return contacts.FromPerson(person), nil
}

// Create creates a contact and returns it as stored by Google.
func (p *peopleClient) Create(ctx context.Context, c contacts.Contact) (contacts.Contact, error) {
	person, err := authed(ctx, p, opCreate, func() (*people.Person, error) {
		return p.svc.People.CreateContact(c.ToPerson()).PersonFields(personFields).Context(ctx).Do()
	})
	if err != nil {
		return contacts.Contact{}, mapError(opCreate, err)
	}

	return contacts.FromPerson(person), nil
}

// Update writes fields of c, checked against c.Etag. Nil or empty fields
// default to c.UpdateFields(). A stale etag returns contacts.ErrConflict.
func (p *peopleClient) Update(ctx context.Context, c contacts.Contact, fields []string) (contacts.Contact, error) {
	if c.Etag == "" {
		return contacts.Contact{}, errors.New("people update: missing etag")
	}

	if len(fields) == 0 {
		fields = c.UpdateFields()
	}

	if len(fields) == 0 {
		return contacts.Contact{}, errors.New("people update: no field to update")
	}

	person, err := authed(ctx, p, opUpdate, func() (*people.Person, error) {
		return p.svc.People.UpdateContact(c.ResourceName, c.ToPerson()).
			UpdatePersonFields(strings.Join(fields, ",")).PersonFields(personFields).Context(ctx).Do()
	})
	if err != nil {
		return contacts.Contact{}, mapError(opUpdate, err)
	}

	return contacts.FromPerson(person), nil
}

// Delete deletes a contact.
func (p *peopleClient) Delete(ctx context.Context, resourceName string) error {
	_, err := authed(ctx, p, opDelete, func() (*people.Empty, error) {
		return p.svc.People.DeleteContact(resourceName).Context(ctx).Do()
	})

	return mapError(opDelete, err)
}

// ListGroups returns every user and system contact group.
func (p *peopleClient) ListGroups(ctx context.Context) ([]contacts.Group, error) {
	var out []contacts.Group

	pageToken := ""

	for {
		resp, err := retryRead(ctx, p, opListGroups, func() (*people.ListContactGroupsResponse, error) {
			call := p.svc.ContactGroups.List().GroupFields(groupFields).PageSize(maxGroupPageSize).Context(ctx)
			if pageToken != "" {
				call = call.PageToken(pageToken)
			}

			return call.Do()
		})
		if err != nil {
			return nil, err
		}

		for _, g := range resp.ContactGroups {
			if g != nil {
				out = append(out, fromGroup(g))
			}
		}

		if resp.NextPageToken == "" {
			return out, nil
		}

		pageToken = resp.NextPageToken
	}
}

// CreateGroup creates a user contact group.
func (p *peopleClient) CreateGroup(ctx context.Context, name string) (contacts.Group, error) {
	g, err := authed(ctx, p, opCreateGroup, func() (*people.ContactGroup, error) {
		return p.svc.ContactGroups.Create(&people.CreateContactGroupRequest{
			ContactGroup:    &people.ContactGroup{Name: name},
			ReadGroupFields: groupFields,
		}).Context(ctx).Do()
	})
	if err != nil {
		return contacts.Group{}, mapError(opCreateGroup, err)
	}

	return fromGroup(g), nil
}

// ModifyMembers adds and removes contacts in a group named by resource name
// or by user-group display name (spec §8.1). Contacts Google cannot find are
// reported as contacts.ErrNotFound.
func (p *peopleClient) ModifyMembers(ctx context.Context, group string, add, remove []string) error {
	resourceName, err := p.resolveGroup(ctx, group)
	if err != nil {
		return err
	}

	resp, err := authed(ctx, p, opModifyMembers, func() (*people.ModifyContactGroupMembersResponse, error) {
		return p.svc.ContactGroups.Members.Modify(resourceName, &people.ModifyContactGroupMembersRequest{
			ResourceNamesToAdd:    add,
			ResourceNamesToRemove: remove,
		}).Context(ctx).Do()
	})
	if err != nil {
		return mapError(opModifyMembers, err)
	}

	if len(resp.NotFoundResourceNames) > 0 {
		return fmt.Errorf("%w: %s", contacts.ErrNotFound, strings.Join(resp.NotFoundResourceNames, ", "))
	}

	return nil
}

// resolveGroup resolves a group resource name or user-group display name.
func (p *peopleClient) resolveGroup(ctx context.Context, group string) (string, error) {
	return contacts.ResolveGroup(ctx, p, group)
}

// fromGroup maps a Google contact group. FormattedName is the localized label
// of system groups ("My Contacts"); for user groups it equals Name.
func fromGroup(g *people.ContactGroup) contacts.Group {
	name := g.FormattedName
	if name == "" {
		name = g.Name
	}

	typ := strings.ToLower(g.GroupType)
	switch g.GroupType {
	case googleGroupUser:
		typ = contacts.GroupTypeUser
	case googleGroupSystem:
		typ = contacts.GroupTypeSystem
	}

	return contacts.Group{ResourceName: g.ResourceName, Name: name, Type: typ, MemberCount: int(g.MemberCount)}
}

// retryRead runs an idempotent read, retrying it once on a Google 5xx.
// Each attempt is observed separately.
func retryRead[T any](ctx context.Context, p *peopleClient, op string, call func() (T, error)) (T, error) {
	v, err := authed(ctx, p, op, call)

	var gerr *googleapi.Error
	if errors.As(err, &gerr) && gerr.Code >= http.StatusInternalServerError && ctx.Err() == nil {
		v, err = authed(ctx, p, op, call)
	}

	if err != nil {
		var zero T
		return zero, mapError(op, err)
	}

	return v, nil
}

// authed runs one People API call and, when Google answers 401, refreshes
// the user's token and runs the call once more. A revoked grant makes that
// refresh fail with invalid_grant, which deletes the user, so the user's data
// goes the next time they use the connector after revoking access. Google
// applies nothing on a 401, so repeating a write here cannot apply it twice.
// A second 401 is returned as is: the grant may still be live.
func authed[T any](ctx context.Context, p *peopleClient, op string, call func() (T, error)) (T, error) {
	v, err := observe(ctx, p, op, call)
	if callStatus(err) != http.StatusUnauthorized || p.tokens == nil || ctx.Err() != nil {
		return v, err
	}

	if ferr := p.tokens.forceRefresh(); ferr != nil {
		var zero T
		return zero, ferr
	}

	return observe(ctx, p, op, call)
}

// observe runs one People API call and reports its method, HTTP status and
// duration to p.onCall.
func observe[T any](ctx context.Context, p *peopleClient, method string, call func() (T, error)) (T, error) {
	start := time.Now()

	v, err := call()
	if p.onCall != nil {
		p.onCall(ctx, method, callStatus(err), time.Since(start))
	}

	return v, err
}

// callStatus is the HTTP status of a finished call: Google's code on an API
// error, 0 when no response arrived (transport error, token refresh failure).
// The People client turns every non-2xx answer into a *googleapi.Error, so a
// nil error means 200.
func callStatus(err error) int {
	if err == nil {
		return http.StatusOK
	}

	if gerr, ok := errors.AsType[*googleapi.Error](err); ok {
		return gerr.Code
	}

	return statusNoResponse
}

// mapError converts a People API error to the contacts errors. Messages
// never carry Google's response body or the request URL, which holds the
// search query.
func mapError(op string, err error) error {
	if err == nil {
		return nil
	}

	if errors.Is(err, contacts.ErrUpstreamAuth) {
		return contacts.ErrUpstreamAuth
	}

	if gerr, ok := errors.AsType[*googleapi.Error](err); ok {
		switch {
		case gerr.Code == http.StatusUnauthorized:
			return contacts.ErrUpstreamAuth
		case gerr.Code == http.StatusNotFound:
			return contacts.ErrNotFound
		case gerr.Code == http.StatusTooManyRequests:
			return contacts.RateLimitedError{RetryAfter: retryAfter(gerr.Header)}
		case gerr.Code == http.StatusBadRequest && isFailedPrecondition(gerr):
			return contacts.ErrConflict
		case gerr.Code == http.StatusBadRequest:
			return fmt.Errorf("people %s: google returned HTTP %d: %w", op, gerr.Code, contacts.ErrInvalidArgument)
		}

		return contacts.UpstreamStatusError{Op: op, Status: gerr.Code}
	}

	if errors.Is(err, context.Canceled) {
		return fmt.Errorf("people %s: %w", op, context.Canceled)
	}

	if cause := transportCause(err); cause != nil {
		return contacts.UnreachableError{Op: op, Err: cause}
	}

	if uerr, ok := errors.AsType[*url.Error](err); ok {
		return fmt.Errorf("people %s: %w", op, uerr.Err)
	}

	return fmt.Errorf("people %s: %w", op, err)
}

// errNoAnswer stands for a call that timed out or whose connection dropped
// before Google answered.
var errNoAnswer = errors.New("no answer before the connection closed or timed out")

// transportCause returns the network-level reason a call got no answer from
// Google, or nil when err is something else. The result never wraps a
// *url.Error, whose text holds the request URL.
func transportCause(err error) error {
	var (
		dnsErr *net.DNSError
		opErr  *net.OpError
		uerr   *url.Error
	)
	switch {
	case errors.As(err, &dnsErr):
		return dnsErr
	case errors.As(err, &opErr):
		return opErr
	case errors.Is(err, context.DeadlineExceeded):
		return fmt.Errorf("%w: %w", errNoAnswer, context.DeadlineExceeded)
	case errors.As(err, &uerr) && (uerr.Timeout() || errors.Is(uerr.Err, io.EOF) || errors.Is(uerr.Err, io.ErrUnexpectedEOF)):
		return errNoAnswer
	}

	return nil
}

// retryAfter parses Retry-After as seconds or an HTTP date.
func retryAfter(h http.Header) time.Duration {
	v := strings.TrimSpace(h.Get(retryAfterHeader))
	if v == "" {
		return defaultRetryAfter
	}

	if s, err := strconv.Atoi(v); err == nil && s > 0 {
		return time.Duration(s) * time.Second
	}

	if t, err := http.ParseTime(v); err == nil {
		if d := time.Until(t); d > 0 {
			return d
		}
	}

	return defaultRetryAfter
}

// isFailedPrecondition reports a stale-etag error, found either in the v1
// error items or in the v2 "status" field of the body.
func isFailedPrecondition(gerr *googleapi.Error) bool {
	for _, item := range gerr.Errors {
		if item.Reason == reasonFailedPrecondition {
			return true
		}
	}

	var body struct {
		Error struct {
			Status string `json:"status"`
		} `json:"error"`
	}

	return json.Unmarshal([]byte(gerr.Body), &body) == nil && body.Error.Status == statusFailedPrecondition
}
