package tools

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"slices"
	"strings"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/samber/trakt-mcp/internal/contacts"
)

const (
	// contactExample shows the contact resource name shape in errors.
	contactExample = "people/c123"
	// groupExample shows the group resource name shape in errors.
	groupExample = "contactGroups/abc"

	// defaultSearchLimit keeps search answers short; callers can ask for more.
	defaultSearchLimit = 10
	// maxSearchLimit is the People API maximum for searchContacts.
	maxSearchLimit = 30
	// defaultPageSize is the list_contacts page when none is given (spec §8).
	defaultPageSize = 100
	// maxPageSize is the People API maximum for connections.list.
	maxPageSize = 1000

	// People API sort orders accepted by list_contacts.
	sortFirstName    = "FIRST_NAME_ASCENDING"
	sortLastName     = "LAST_NAME_ASCENDING"
	sortLastModified = "LAST_MODIFIED_DESCENDING"

	// writeWarning ends every write tool description (spec §8).
	writeWarning = " This changes the user's real address book."
)

// contactResourceRe and groupResourceRe are the only resource name shapes
// passed to Google. Anything else (people/me, query strings, dot segments)
// could address another resource or alter the request path.
var (
	contactResourceRe = regexp.MustCompile(`^people/c[0-9]+$`)
	groupResourceRe   = regexp.MustCompile(`^contactGroups/[A-Za-z0-9_-]+$`)
)

// sortOrders lists the accepted list_contacts sort values, in message order.
var sortOrders = []string{sortFirstName, sortLastName, sortLastModified}

// TypedValue is one value of a multi-valued field.
type TypedValue struct {
	Value string `json:"value" jsonschema:"the value: e-mail address, phone number or URL"`
	Type  string `json:"type,omitempty" jsonschema:"kind of value, e.g. home, work, mobile; linkedin for a LinkedIn profile URL"`
}

// AddressValue is a postal address.
type AddressValue struct {
	Formatted  string `json:"formatted,omitempty" jsonschema:"full address on one or more lines"`
	Street     string `json:"street,omitempty"`
	City       string `json:"city,omitempty"`
	Region     string `json:"region,omitempty"`
	PostalCode string `json:"postal_code,omitempty"`
	Country    string `json:"country,omitempty"`
	Type       string `json:"type,omitempty" jsonschema:"home, work or other"`
}

// ContactOutput is a contact as returned by the tools.
type ContactOutput struct {
	ResourceName string         `json:"resource_name" jsonschema:"contact id, e.g. people/c123"`
	Etag         string         `json:"etag,omitempty"`
	DisplayName  string         `json:"display_name,omitempty"`
	GivenName    string         `json:"given_name,omitempty"`
	FamilyName   string         `json:"family_name,omitempty"`
	Company      string         `json:"company,omitempty"`
	JobTitle     string         `json:"job_title,omitempty"`
	Notes        string         `json:"notes,omitempty"`
	Emails       []TypedValue   `json:"emails,omitempty"`
	Phones       []TypedValue   `json:"phones,omitempty"`
	Websites     []TypedValue   `json:"websites,omitempty"`
	Addresses    []AddressValue `json:"addresses,omitempty"`
	Groups       []string       `json:"groups,omitempty" jsonschema:"contact group resource names"`
}

// SearchInput is the search_contacts input.
type SearchInput struct {
	Query string `json:"query" jsonschema:"prefix of a name, nickname, e-mail, phone or organization"`
	Limit int    `json:"limit,omitempty" jsonschema:"maximum results, 1 to 30, default 10"`
}

// SearchOutput is the search_contacts output.
type SearchOutput struct {
	Contacts []ContactOutput `json:"contacts"`
}

// ListInput is the list_contacts input.
type ListInput struct {
	PageSize  int    `json:"page_size,omitempty" jsonschema:"contacts per page, 1 to 1000, default 100"`
	PageToken string `json:"page_token,omitempty" jsonschema:"next_page_token of the previous page"`
	Sort      string `json:"sort,omitempty" jsonschema:"FIRST_NAME_ASCENDING, LAST_NAME_ASCENDING or LAST_MODIFIED_DESCENDING"`
}

// ListOutput is the list_contacts output.
type ListOutput struct {
	Contacts      []ContactOutput `json:"contacts"`
	NextPageToken string          `json:"next_page_token,omitempty" jsonschema:"pass as page_token to get the next page; absent on the last page"`
}

// ResourceInput names one contact.
type ResourceInput struct {
	ResourceName string `json:"resource_name" jsonschema:"contact id, e.g. people/c123"`
}

// DeleteOutput is the delete_contact output.
type DeleteOutput struct {
	ResourceName string `json:"resource_name"`
	Deleted      bool   `json:"deleted"`
}

// CreateInput is the create_contact input.
type CreateInput struct {
	GivenName  string         `json:"given_name,omitempty"`
	FamilyName string         `json:"family_name,omitempty"`
	Company    string         `json:"company,omitempty"`
	JobTitle   string         `json:"job_title,omitempty"`
	Notes      string         `json:"notes,omitempty"`
	Emails     []TypedValue   `json:"emails,omitempty"`
	Phones     []TypedValue   `json:"phones,omitempty"`
	Websites   []TypedValue   `json:"websites,omitempty" jsonschema:"web pages; store a LinkedIn profile with type linkedin"`
	Addresses  []AddressValue `json:"addresses,omitempty"`
	Groups     []string       `json:"groups,omitempty" jsonschema:"contact groups, by resource name (contactGroups/abc) or user group name"`
}

// UpdateInput is the update_contact input. Nil means "leave unchanged"; a
// provided empty value clears the field, a provided list replaces it.
type UpdateInput struct {
	ResourceName string          `json:"resource_name" jsonschema:"contact id, e.g. people/c123"`
	GivenName    *string         `json:"given_name,omitempty"`
	FamilyName   *string         `json:"family_name,omitempty"`
	Company      *string         `json:"company,omitempty"`
	JobTitle     *string         `json:"job_title,omitempty"`
	Notes        *string         `json:"notes,omitempty"`
	Emails       *[]TypedValue   `json:"emails,omitempty" jsonschema:"replaces every e-mail"`
	Phones       *[]TypedValue   `json:"phones,omitempty" jsonschema:"replaces every phone"`
	Websites     *[]TypedValue   `json:"websites,omitempty" jsonschema:"replaces every website; LinkedIn has type linkedin"`
	Addresses    *[]AddressValue `json:"addresses,omitempty" jsonschema:"replaces every address"`
	Groups       *[]string       `json:"groups,omitempty" jsonschema:"replaces user group memberships; resource names or user group names"`
}

// GroupOutput is a contact group.
type GroupOutput struct {
	ResourceName string `json:"resource_name"`
	Name         string `json:"name"`
	Type         string `json:"type" jsonschema:"user or system"`
	MemberCount  int    `json:"member_count"`
}

// GroupsOutput is the list_contact_groups output.
type GroupsOutput struct {
	Groups []GroupOutput `json:"groups"`
}

// CreateGroupInput is the create_contact_group input.
type CreateGroupInput struct {
	Name string `json:"name" jsonschema:"name of the new group"`
}

// ModifyMembersInput is the modify_contact_group_members input.
type ModifyMembersInput struct {
	Group  string   `json:"group" jsonschema:"resource name (contactGroups/abc) or user group name"`
	Add    []string `json:"add,omitempty" jsonschema:"contact resource names to add"`
	Remove []string `json:"remove,omitempty" jsonschema:"contact resource names to remove"`
}

// ModifyMembersOutput is the modify_contact_group_members output.
type ModifyMembersOutput struct {
	Group   string `json:"group"`
	Added   int    `json:"added"`
	Removed int    `json:"removed"`
}

// resultCount implementations feed the result_count of the tool log line:
// contacts or groups returned, 1 for a single contact or group, 0 for delete.
func (o SearchOutput) resultCount() int      { return len(o.Contacts) }
func (o ListOutput) resultCount() int        { return len(o.Contacts) }
func (ContactOutput) resultCount() int       { return 1 }
func (DeleteOutput) resultCount() int        { return 0 }
func (o GroupsOutput) resultCount() int      { return len(o.Groups) }
func (GroupOutput) resultCount() int         { return 1 }
func (ModifyMembersOutput) resultCount() int { return 1 }

// Every tool declares its hints explicitly: the directories reject tools with
// a missing title, and OpenAI requires readOnlyHint, destructiveHint and
// openWorldHint to be set. openWorldHint is false because each tool acts on
// one bounded private account, the caller's Google Contacts.
var (
	hintFalse = false
	hintTrue  = true
)

func readOnly(title string) *mcp.ToolAnnotations {
	return &mcp.ToolAnnotations{Title: title, ReadOnlyHint: true, DestructiveHint: &hintFalse, OpenWorldHint: &hintFalse}
}

func writes(title string, destructive bool) *mcp.ToolAnnotations {
	d := &hintFalse
	if destructive {
		d = &hintTrue
	}

	return &mcp.ToolAnnotations{Title: title, DestructiveHint: d, OpenWorldHint: &hintFalse}
}

func registerTools(srv *mcp.Server, deps Deps) {
	mcp.AddTool(srv, &mcp.Tool{
		Name:        "search_contacts",
		Title:       "Search contacts",
		Description: "Search the user's Google Contacts by prefix of name, nickname, e-mail, phone or organization.",
		Annotations: readOnly("Search contacts"),
	}, handle(deps, false, searchContacts))
	mcp.AddTool(srv, &mcp.Tool{
		Name:        "list_contacts",
		Title:       "List contacts",
		Description: "List the user's Google Contacts one page at a time.",
		Annotations: readOnly("List contacts"),
	}, handle(deps, false, listContacts))
	mcp.AddTool(srv, &mcp.Tool{
		Name:        "get_contact",
		Title:       "Get contact",
		Description: "Get one contact with every handled field and its etag.",
		Annotations: readOnly("Get contact"),
	}, handle(deps, false, getContact))
	mcp.AddTool(srv, &mcp.Tool{
		Name:        "create_contact",
		Title:       "Create contact",
		Description: "Create a contact." + writeWarning,
		Annotations: writes("Create contact", false),
	}, handle(deps, true, createContact))
	mcp.AddTool(srv, &mcp.Tool{
		Name:  "update_contact",
		Title: "Update contact",
		Description: "Update the fields given, leaving the others unchanged. A given list replaces the whole list; " +
			"an empty value clears the field." + writeWarning,
		Annotations: writes("Update contact", false),
	}, handle(deps, true, updateContact))
	mcp.AddTool(srv, &mcp.Tool{
		Name:  "upsert_contact_by_email",
		Title: "Upsert contact by email",
		Description: "Find the contact with this exact e-mail and fill only its empty fields and missing values, " +
			"or create it when none exists. Never overwrites or removes a value. Several matches change nothing " +
			"and return the candidates." + writeWarning,
		Annotations: writes("Upsert contact by email", false),
	}, handle(deps, true, upsertContact))
	mcp.AddTool(srv, &mcp.Tool{
		Name:        "delete_contact",
		Title:       "Delete contact",
		Description: "Delete a contact permanently." + writeWarning,
		Annotations: writes("Delete contact", true),
	}, handle(deps, true, deleteContact))
	mcp.AddTool(srv, &mcp.Tool{
		Name:        "list_contact_groups",
		Title:       "List contact groups",
		Description: "List the user's contact groups (labels), user and system.",
		Annotations: readOnly("List contact groups"),
	}, handle(deps, false, listGroups))
	mcp.AddTool(srv, &mcp.Tool{
		Name:        "create_contact_group",
		Title:       "Create contact group",
		Description: "Create a contact group (label)." + writeWarning,
		Annotations: writes("Create contact group", false),
	}, handle(deps, true, createGroup))
	mcp.AddTool(srv, &mcp.Tool{
		Name:        "modify_contact_group_members",
		Title:       "Modify contact group members",
		Description: "Add contacts to and remove contacts from a contact group." + writeWarning,
		Annotations: writes("Modify contact group members", false),
	}, handle(deps, true, modifyMembers))
}

func searchContacts(ctx context.Context, p contacts.People, in SearchInput) (SearchOutput, error) {
	limit := in.Limit
	if limit <= 0 {
		limit = defaultSearchLimit
	}

	limit = min(limit, maxSearchLimit)

	found, err := p.Search(ctx, in.Query, limit)
	if err != nil {
		return SearchOutput{}, err
	}

	out := SearchOutput{Contacts: toOutputs(found)}

	return out, nil
}

func listContacts(ctx context.Context, p contacts.People, in ListInput) (ListOutput, error) {
	if in.Sort != "" && !slices.Contains(sortOrders, in.Sort) {
		return ListOutput{}, newToolError("sort must be one of " + strings.Join(sortOrders, ", "))
	}

	size := in.PageSize
	if size <= 0 {
		size = defaultPageSize
	}

	size = min(size, maxPageSize)

	page, next, err := p.List(ctx, size, in.PageToken, in.Sort)
	if err != nil {
		if in.PageToken != "" && errors.Is(err, contacts.ErrInvalidArgument) {
			return ListOutput{}, newToolError("invalid page_token")
		}

		return ListOutput{}, err
	}

	out := ListOutput{Contacts: toOutputs(page), NextPageToken: next}

	return out, nil
}

func getContact(ctx context.Context, p contacts.People, in ResourceInput) (ContactOutput, error) {
	if err := checkResourceName(in.ResourceName); err != nil {
		return ContactOutput{}, err
	}

	c, err := p.Get(ctx, in.ResourceName)
	if err != nil {
		return ContactOutput{}, err
	}

	return toOutput(c), nil
}

func createContact(ctx context.Context, p contacts.People, in CreateInput) (ContactOutput, error) {
	groups, err := resolveGroups(ctx, p, in.Groups)
	if err != nil {
		return ContactOutput{}, err
	}

	c := contacts.Contact{
		GivenName:  in.GivenName,
		FamilyName: in.FamilyName,
		Company:    in.Company,
		JobTitle:   in.JobTitle,
		Notes:      in.Notes,
		Emails:     fromTypedValues(in.Emails),
		Phones:     fromTypedValues(in.Phones),
		Websites:   fromTypedValues(in.Websites),
		Addresses:  fromAddressValues(in.Addresses),
		Groups:     groups,
	}

	created, err := p.Create(ctx, c)
	if err != nil {
		return ContactOutput{}, err
	}

	return toOutput(created), nil
}

func updateContact(ctx context.Context, p contacts.People, in UpdateInput) (ContactOutput, error) {
	if err := checkResourceName(in.ResourceName); err != nil {
		return ContactOutput{}, err
	}

	var groups []string

	if in.Groups != nil {
		var err error
		if groups, err = resolveGroups(ctx, p, *in.Groups); err != nil {
			return ContactOutput{}, err
		}
	}
	// A fresh read gives the current etag and the full person, which
	// ToPerson overlays so unmapped data survives the write.
	c, err := p.Get(ctx, in.ResourceName)
	if err != nil {
		return ContactOutput{}, err
	}

	fields := applyUpdateInput(&c, in, groups)

	if len(fields) == 0 {
		// Update would otherwise fall back to every non-empty field.
		return ContactOutput{}, newToolError("no field to update")
	}

	slices.Sort(fields)

	updated, err := p.Update(ctx, c, fields)
	if err != nil {
		return ContactOutput{}, err
	}

	return toOutput(updated), nil
}

// applyUpdateInput overlays the fields set in the input onto c and returns the
// People API field names it touched, deduplicated. groups is the already
// resolved membership list, used only when in.Groups is set.
func applyUpdateInput(c *contacts.Contact, in UpdateInput, groups []string) []string {
	var fields []string

	mark := func(field string) {
		if !slices.Contains(fields, field) {
			fields = append(fields, field)
		}
	}
	set := func(dst, src *string, field string) {
		if src != nil {
			*dst = *src

			mark(field)
		}
	}
	set(&c.GivenName, in.GivenName, contacts.FieldNames)
	set(&c.FamilyName, in.FamilyName, contacts.FieldNames)
	set(&c.Company, in.Company, contacts.FieldOrganizations)
	set(&c.JobTitle, in.JobTitle, contacts.FieldOrganizations)
	set(&c.Notes, in.Notes, contacts.FieldBiographies)

	if in.Emails != nil {
		c.Emails = fromTypedValues(*in.Emails)

		mark(contacts.FieldEmailAddresses)
	}

	if in.Phones != nil {
		c.Phones = fromTypedValues(*in.Phones)

		mark(contacts.FieldPhoneNumbers)
	}

	if in.Websites != nil {
		c.Websites = fromTypedValues(*in.Websites)

		mark(contacts.FieldURLs)
	}

	if in.Addresses != nil {
		c.Addresses = fromAddressValues(*in.Addresses)

		mark(contacts.FieldAddresses)
	}

	if in.Groups != nil {
		c.Groups = groups

		mark(contacts.FieldMemberships)
	}

	return fields
}

func deleteContact(ctx context.Context, p contacts.People, in ResourceInput) (DeleteOutput, error) {
	if err := checkResourceName(in.ResourceName); err != nil {
		return DeleteOutput{}, err
	}

	if err := p.Delete(ctx, in.ResourceName); err != nil {
		return DeleteOutput{}, err
	}

	return DeleteOutput{ResourceName: in.ResourceName, Deleted: true}, nil
}

func listGroups(ctx context.Context, p contacts.People, _ struct{}) (GroupsOutput, error) {
	groups, err := p.ListGroups(ctx)
	if err != nil {
		return GroupsOutput{}, mapError(err, msgGroupNotFound)
	}

	out := GroupsOutput{Groups: make([]GroupOutput, 0, len(groups))}
	for _, g := range groups {
		out.Groups = append(out.Groups, toGroupOutput(g))
	}

	return out, nil
}

func createGroup(ctx context.Context, p contacts.People, in CreateGroupInput) (GroupOutput, error) {
	if strings.TrimSpace(in.Name) == "" {
		return GroupOutput{}, newToolError("name is required")
	}

	g, err := p.CreateGroup(ctx, in.Name)
	if err != nil {
		return GroupOutput{}, mapError(err, msgGroupNotFound)
	}

	return toGroupOutput(g), nil
}

func modifyMembers(ctx context.Context, p contacts.People, in ModifyMembersInput) (ModifyMembersOutput, error) {
	if len(in.Add) == 0 && len(in.Remove) == 0 {
		return ModifyMembersOutput{}, newToolError("add or remove must list at least one contact")
	}

	if err := checkGroup(in.Group); err != nil {
		return ModifyMembersOutput{}, err
	}

	for _, rn := range slices.Concat(in.Add, in.Remove) {
		if err := checkResourceName(rn); err != nil {
			return ModifyMembersOutput{}, err
		}
	}

	if err := p.ModifyMembers(ctx, in.Group, in.Add, in.Remove); err != nil {
		// A bare ErrNotFound is Google's 404 on the group itself; members
		// Google cannot find come back wrapped with their names.
		if isBareNotFound(err) {
			return ModifyMembersOutput{}, newToolError(msgGroupNotFound)
		}

		return ModifyMembersOutput{}, mapError(err, msgContactNotFound)
	}

	out := ModifyMembersOutput{Group: in.Group, Added: len(in.Add), Removed: len(in.Remove)}

	return out, nil
}

// groupCache lists the user's groups at most once per tool call, so resolving
// several group names costs one People API call.
type groupCache struct {
	p      contacts.People
	groups []contacts.Group
	loaded bool
}

func (g *groupCache) ListGroups(ctx context.Context) ([]contacts.Group, error) {
	if !g.loaded {
		groups, err := g.p.ListGroups(ctx)
		if err != nil {
			return nil, err
		}

		g.groups, g.loaded = groups, true
	}

	return g.groups, nil
}

// resolveGroups maps resource names or user-group names to resource names,
// keeping order and dropping duplicates.
func resolveGroups(ctx context.Context, p contacts.People, groups []string) ([]string, error) {
	cache := &groupCache{p: p}

	out := make([]string, 0, len(groups))
	for _, g := range groups {
		if err := checkGroup(g); err != nil {
			return nil, err
		}

		rn, err := contacts.ResolveGroup(ctx, cache, g)
		if err != nil {
			return nil, err
		}

		if !slices.Contains(out, rn) {
			out = append(out, rn)
		}
	}

	return out, nil
}

func checkResourceName(rn string) error {
	if !contactResourceRe.MatchString(rn) {
		return newToolError(fmt.Sprintf("resource_name must look like %q", contactExample))
	}

	return nil
}

// checkGroup accepts a user-group display name, or a group resource name of
// the expected shape.
func checkGroup(g string) error {
	if contacts.IsGroupResourceName(g) && !groupResourceRe.MatchString(g) {
		return newToolError(fmt.Sprintf("group resource name must look like %q", groupExample))
	}

	return nil
}

func toOutputs(cs []contacts.Contact) []ContactOutput {
	out := make([]ContactOutput, 0, len(cs))
	for _, c := range cs {
		out = append(out, toOutput(c))
	}

	return out
}

func toOutput(c contacts.Contact) ContactOutput {
	return ContactOutput{
		ResourceName: c.ResourceName,
		Etag:         c.Etag,
		DisplayName:  c.DisplayName,
		GivenName:    c.GivenName,
		FamilyName:   c.FamilyName,
		Company:      c.Company,
		JobTitle:     c.JobTitle,
		Notes:        c.Notes,
		Emails:       toTypedValues(c.Emails),
		Phones:       toTypedValues(c.Phones),
		Websites:     toTypedValues(c.Websites),
		Addresses:    toAddressValues(c.Addresses),
		Groups:       slices.Clone(c.Groups),
	}
}

func toGroupOutput(g contacts.Group) GroupOutput {
	return GroupOutput{ResourceName: g.ResourceName, Name: g.Name, Type: g.Type, MemberCount: g.MemberCount}
}

func toTypedValues(ts []contacts.Typed) []TypedValue {
	var out []TypedValue
	for _, t := range ts {
		out = append(out, TypedValue(t))
	}

	return out
}

func fromTypedValues(vs []TypedValue) []contacts.Typed {
	var out []contacts.Typed

	for _, v := range vs {
		if strings.TrimSpace(v.Value) != "" {
			out = append(out, contacts.Typed(v))
		}
	}

	return out
}

func toAddressValues(as []contacts.Address) []AddressValue {
	var out []AddressValue
	for _, a := range as {
		out = append(out, AddressValue(a))
	}

	return out
}

func fromAddressValues(vs []AddressValue) []contacts.Address {
	var out []contacts.Address
	for _, v := range vs {
		out = append(out, contacts.Address(v))
	}

	return out
}

// isBareNotFound reports whether err is the unwrapped contacts.ErrNotFound
// sentinel, which means Google answered 404 for the group itself. Errors that
// wrap it carry the names of members Google could not find and must not match.
func isBareNotFound(err error) bool {
	return errors.Is(err, contacts.ErrNotFound) && errors.Unwrap(err) == nil
}
