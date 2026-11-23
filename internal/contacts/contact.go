// Package contacts holds the contact model, its mapping to Google People API
// v1 types, and the enrich rules used by upsert_contact_by_email. It does no
// network I/O: the People interface is implemented elsewhere.
package contacts

import (
	"context"
	"slices"
	"strings"

	"google.golang.org/api/people/v1"
)

// People API person field names, used both as updatePersonFields entries and
// as the "changed" list returned by Enrich.
const (
	// FieldNames covers GivenName and FamilyName.
	FieldNames = "names"
	// FieldOrganizations covers Company and JobTitle.
	FieldOrganizations = "organizations"
	// FieldBiographies covers Notes.
	FieldBiographies = "biographies"
	// FieldEmailAddresses covers Emails.
	FieldEmailAddresses = "emailAddresses"
	// FieldPhoneNumbers covers Phones.
	FieldPhoneNumbers = "phoneNumbers"
	// FieldURLs covers Websites.
	FieldURLs = "urls"
	// FieldAddresses covers Addresses.
	FieldAddresses = "addresses"
	// FieldMemberships covers Groups.
	FieldMemberships = "memberships"
)

const (
	// WebsiteTypeLinkedIn marks a website as the contact's LinkedIn profile;
	// the People API has no dedicated LinkedIn field.
	WebsiteTypeLinkedIn = "linkedin"
	// sourceTypeContact is the People API source type of a user-owned contact;
	// updates must carry that source with its current etag.
	sourceTypeContact = "CONTACT"
	// contactGroupPrefix starts every contact group resource name.
	contactGroupPrefix = "contactGroups/"
	// myContactsGroup is the system group behind the user's main Contacts
	// list; a contact created outside it only shows under "Other contacts".
	myContactsGroup = contactGroupPrefix + "myContacts"
)

// systemGroupIDs are the People API system contact groups. Their memberships
// are managed by Google, so ToPerson keeps them even when Groups omits them.
var systemGroupIDs = []string{"myContacts", "starred", "friends", "family", "coworkers", "chatBuddies", "all", "blocked"}

// Typed is one value of a multi-valued field with its type (home, work, ...).
type Typed struct {
	Value, Type string
}

// Address is a postal address.
type Address struct {
	Formatted, Street, City, Region, PostalCode, Country, Type string
}

// Contact is the subset of a People API person handled by the server.
type Contact struct {
	ResourceName, Etag                 string // Etag is the CONTACT source etag
	GivenName, FamilyName, DisplayName string
	Company, JobTitle, Notes           string
	Emails, Phones, Websites           []Typed // LinkedIn = Websites{Type: WebsiteTypeLinkedIn}
	Addresses                          []Address
	Groups                             []string // contactGroups/... resource names

	// raw is a copy of the fetched person, nil for contacts built by callers.
	// ToPerson overlays the Contact on it so writes keep every field and
	// sub-field this model does not map (spec §8.1: never remove a value).
	raw *people.Person
}

// Group is a contact group. Type is "user" or "system".
type Group struct {
	ResourceName, Name, Type string
	MemberCount              int
}

// People is the Google People API surface the tools depend on.
type People interface {
	Search(ctx context.Context, query string, limit int) ([]Contact, error)
	List(ctx context.Context, pageSize int, pageToken, sortOrder string) ([]Contact, string, error)
	Get(ctx context.Context, resourceName string) (Contact, error)
	Create(ctx context.Context, c Contact) (Contact, error)
	Update(ctx context.Context, c Contact, fields []string) (Contact, error) // c.Etag required
	Delete(ctx context.Context, resourceName string) error
	ListGroups(ctx context.Context) ([]Group, error)
	CreateGroup(ctx context.Context, name string) (Group, error)
	ModifyMembers(ctx context.Context, group string, add, remove []string) error
}

// FromPerson maps a People API person to a Contact. Single-valued fields come
// from the first entry owned by the CONTACT source, falling back to the first
// entry: for a contact linked to a Google profile, the first entry can be the
// profile's, which updateContact cannot change. Multi-valued fields list
// every entry, profile ones included, so matching and display see them.
func FromPerson(p *people.Person) Contact {
	if p == nil {
		return Contact{}
	}

	c := Contact{ResourceName: p.ResourceName, Etag: p.Etag, raw: clonePerson(p)}
	// updateContact checks the CONTACT source etag, which differs from the
	// person etag for contacts linked to a Google profile.
	if src := contactSource(p); src != nil && src.Etag != "" {
		c.Etag = src.Etag
	}

	if n := preferContact(p.Names, nameMeta); n != nil {
		c.GivenName, c.FamilyName, c.DisplayName = n.GivenName, n.FamilyName, n.DisplayName
	}

	if o := preferContact(p.Organizations, orgMeta); o != nil {
		c.Company, c.JobTitle = o.Name, o.Title
	}

	if b := preferContact(p.Biographies, bioMeta); b != nil {
		c.Notes = b.Value
	}

	c.Emails = typedValues(p.EmailAddresses, func(e *people.EmailAddress) Typed { return Typed{Value: e.Value, Type: e.Type} })
	c.Phones = typedValues(p.PhoneNumbers, func(e *people.PhoneNumber) Typed { return Typed{Value: e.Value, Type: e.Type} })
	c.Websites = typedValues(p.Urls, func(e *people.Url) Typed { return Typed{Value: e.Value, Type: e.Type} })

	for _, a := range p.Addresses {
		if a != nil {
			c.Addresses = append(c.Addresses, fromAddress(a))
		}
	}

	for _, m := range p.Memberships {
		if g := membershipGroup(m); g != "" {
			c.Groups = append(c.Groups, g)
		}
	}

	return c
}

// typedValues maps every non-nil entry to a Typed, or returns nil when there
// is none.
func typedValues[E any](entries []*E, toTyped func(*E) Typed) []Typed {
	var out []Typed

	for _, e := range entries {
		if e != nil {
			out = append(out, toTyped(e))
		}
	}

	return out
}

// ToPerson maps a Contact to a People API person for create or update.
// When the Contact came from FromPerson, it starts from the fetched person and
// edits only mapped fields: extra entries, sub-fields (middle name, department,
// PO box, ...) and primary flags survive. Multi-valued lists follow the
// Contact (a value missing from it is dropped). DisplayName is output-only and
// never sent. Without a fetched person, empty fields produce no entry.
//
// Only entries owned by the CONTACT source (or carrying no source) are sent:
// Google rejects or ignores edits to profile entries, and copying them into
// the contact would duplicate them. A Contact value that only matches a
// profile entry is therefore left out rather than created as a contact entry.
func (c Contact) ToPerson() *people.Person {
	p := clonePerson(c.raw)
	if p == nil {
		p = &people.Person{Etag: c.Etag}
	}

	p.ResourceName = c.ResourceName
	p.Metadata = c.sourceMetadata()

	c.overlayName(p)
	c.overlayOrganization(p)
	c.overlayBiography(p)

	p.EmailAddresses = overlayTyped(p.EmailAddresses, emailMeta, c.Emails, newTypedEntry(
		func(e *people.EmailAddress) (string, string) { return e.Value, e.Type },
		func(e *people.EmailAddress, v, t string) { e.Value, e.Type = v, t },
		emailKey))
	p.PhoneNumbers = overlayTyped(p.PhoneNumbers, phoneMeta, c.Phones, newTypedEntry(
		func(e *people.PhoneNumber) (string, string) { return e.Value, e.Type },
		func(e *people.PhoneNumber, v, t string) { e.Value, e.Type = v, t },
		phoneKey))
	p.Urls = overlayTyped(p.Urls, urlMeta, c.Websites, newTypedEntry(
		func(e *people.Url) (string, string) { return e.Value, e.Type },
		func(e *people.Url, v, t string) { e.Value, e.Type = v, t },
		websiteKey))

	addrs, foreignAddrs := splitForeign(p.Addresses, addressMeta)
	p.Addresses = overlay(addrs, c.Addresses, foreignKeys(foreignAddrs,
		func(e *people.Address) string { return addressKey(fromAddress(e)) }), overlayFuncs[*people.Address, Address]{
		key:     func(e *people.Address) string { return addressKey(fromAddress(e)) },
		valKey:  addressKey,
		create:  toAddress,
		setType: func(e *people.Address, a Address) { e.Type = a.Type },
	})

	p.Memberships = c.memberships(p.Memberships)
	if c.raw == nil {
		p.Memberships = withMyContacts(p.Memberships)
	}

	return p
}

// overlayName applies the Contact's given and family names to p.Names. An
// entry owned by the profile is never copied into the contact.
func (c Contact) overlayName(p *people.Person) {
	var profileName *people.Name

	p.Names, profileName = splitOwned(p.Names, nameMeta)
	if c.GivenName == "" && c.FamilyName == "" && len(p.Names) == 0 {
		return
	}

	if len(p.Names) == 0 && profileName != nil &&
		profileName.GivenName == c.GivenName && profileName.FamilyName == c.FamilyName {
		return
	}

	n := pickOrNew(&p.Names, nameMeta)
	// UnstructuredName is derived from the structured parts but may win
	// over them on write, so a stale one would undo a rename.
	if n.GivenName != c.GivenName || n.FamilyName != c.FamilyName {
		n.UnstructuredName = ""
	}

	n.GivenName, n.FamilyName = c.GivenName, c.FamilyName
}

// overlayOrganization applies the Contact's company and job title to
// p.Organizations, leaving profile-owned entries out.
func (c Contact) overlayOrganization(p *people.Person) {
	var profileOrg *people.Organization

	p.Organizations, profileOrg = splitOwned(p.Organizations, orgMeta)
	if c.Company == "" && c.JobTitle == "" && len(p.Organizations) == 0 {
		return
	}

	if len(p.Organizations) == 0 && profileOrg != nil &&
		profileOrg.Name == c.Company && profileOrg.Title == c.JobTitle {
		return
	}

	o := pickOrNew(&p.Organizations, orgMeta)
	o.Name, o.Title = c.Company, c.JobTitle
}

// overlayBiography applies the Contact's notes to p.Biographies, leaving
// profile-owned entries out.
func (c Contact) overlayBiography(p *people.Person) {
	var profileBio *people.Biography

	p.Biographies, profileBio = splitOwned(p.Biographies, bioMeta)
	if c.Notes == "" && len(p.Biographies) == 0 {
		return
	}

	if len(p.Biographies) == 0 && profileBio != nil && profileBio.Value == c.Notes {
		return
	}

	pickOrNew(&p.Biographies, bioMeta).Value = c.Notes
}

// typedEntry adapts one People API entry type E, whose value is a Typed, to
// the overlay machinery.
type typedEntry[E any] struct {
	toTyped func(*E) Typed
	create  func(Typed) *E
	setType func(*E, string)
	key     func(Typed) string // key of a Typed value, same normalization for raw entries
}

// newTypedEntry builds the adapter of an entry type E from its value/type
// getter and setter. setType rewrites the type and keeps the value.
func newTypedEntry[E any](get func(*E) (value, typ string), set func(e *E, value, typ string), key func(Typed) string) typedEntry[E] {
	return typedEntry[E]{
		toTyped: func(e *E) Typed {
			v, t := get(e)

			return Typed{Value: v, Type: t}
		},
		create: func(t Typed) *E {
			e := new(E)
			set(e, t.Value, t.Type)

			return e
		},
		setType: func(e *E, typ string) {
			v, _ := get(e)
			set(e, v, typ)
		},
		key: key,
	}
}

// overlayTyped overlays values on the contact-owned entries of raw. Profile
// entries (foreign) are not sent; a value matching only one of them, by value
// alone, is skipped.
func overlayTyped[E any](raw []*E, meta func(*E) *people.FieldMetadata, values []Typed, f typedEntry[E]) []*E {
	owned, foreign := splitForeign(raw, meta)

	return overlay(owned, values,
		foreignKeys(foreign, func(e *E) string { return f.key(Typed{Value: f.toTyped(e).Value}) }),
		typedOverlay(f.toTyped, f.create, f.setType, f.key))
}

// withMyContacts puts a myContacts membership first unless ms already has one.
func withMyContacts(ms []*people.Membership) []*people.Membership {
	for _, m := range ms {
		if membershipGroup(m) == myContactsGroup {
			return ms
		}
	}

	mine := &people.Membership{ContactGroupMembership: &people.ContactGroupMembership{ContactGroupResourceName: myContactsGroup}}

	return append([]*people.Membership{mine}, ms...)
}

// sourceMetadata returns the metadata carrying the CONTACT source and its
// etag, or nil when the Contact has no etag (create).
func (c Contact) sourceMetadata() *people.PersonMetadata {
	src := &people.Source{Type: sourceTypeContact}
	if raw := contactSource(c.raw); raw != nil {
		src.Id = raw.Id
	}

	src.Etag = c.Etag
	if src.Etag == "" {
		return nil
	}

	return &people.PersonMetadata{Sources: []*people.Source{src}}
}

// memberships keeps raw system-group and domain memberships, which Google
// manages, and makes user-group memberships follow c.Groups.
func (c Contact) memberships(raw []*people.Membership) []*people.Membership {
	var out []*people.Membership

	present := map[string]bool{}
	userEntries := map[string]*people.Membership{}

	for _, m := range raw {
		if m == nil {
			continue
		}

		g := membershipGroup(m)
		switch {
		case g == "" || isSystemGroup(g):
			out = append(out, m)
			present[g] = true
		case userEntries[g] == nil:
			userEntries[g] = m
		}
	}

	for _, g := range c.Groups {
		if g == "" || present[g] {
			continue
		}

		present[g] = true

		m := userEntries[g]
		if m == nil {
			m = &people.Membership{
				ContactGroupMembership: &people.ContactGroupMembership{ContactGroupResourceName: g},
			}
		}

		out = append(out, m)
	}

	return out
}

// UpdateFields returns the sorted People API field names for every non-empty
// field of c, suitable for updatePersonFields.
func (c Contact) UpdateFields() []string {
	var fields []string

	add := func(cond bool, name string) {
		if cond {
			fields = append(fields, name)
		}
	}
	add(c.GivenName != "" || c.FamilyName != "", FieldNames)
	add(c.Company != "" || c.JobTitle != "", FieldOrganizations)
	add(c.Notes != "", FieldBiographies)
	add(len(c.Emails) > 0, FieldEmailAddresses)
	add(len(c.Phones) > 0, FieldPhoneNumbers)
	add(len(c.Websites) > 0, FieldURLs)
	add(len(c.Addresses) > 0, FieldAddresses)
	add(len(c.Groups) > 0, FieldMemberships)
	slices.Sort(fields)

	return fields
}

// overlayFuncs adapts one People API entry type E to one Contact value type V.
type overlayFuncs[E any, V any] struct {
	key     func(E) string // key of a raw entry
	valKey  func(V) string // key of a Contact value, same normalization
	create  func(V) E      // new entry for a value absent from raw
	setType func(E, V)     // copies the value's type onto a reused entry
}

func typedOverlay[E any](toTyped func(E) Typed, create func(Typed) E, setType func(E, string), key func(Typed) string) overlayFuncs[E, Typed] {
	return overlayFuncs[E, Typed]{
		key:     func(e E) string { return key(toTyped(e)) },
		valKey:  key,
		create:  create,
		setType: func(e E, t Typed) { setType(e, t.Type) },
	}
}

// overlay builds the entry list for values: a value whose key matches an
// unused raw entry reuses that entry (keeping metadata, primary flag and
// unmapped sub-fields) with only its type updated; a value matching only a
// key in foreign (a profile entry) is skipped; other values become new
// entries; raw entries matching no value are dropped. raw entries must already
// be private copies, since they are modified in place.
func overlay[E comparable, V any](raw []E, values []V, foreign map[string]bool, f overlayFuncs[E, V]) []E {
	var (
		zero E
		out  []E
	)

	used := make([]bool, len(raw))

	for _, v := range values {
		k := f.valKey(v)
		idx := -1

		for i, e := range raw {
			if k != "" && !used[i] && e != zero && f.key(e) == k {
				idx = i
				break
			}
		}

		if idx < 0 {
			if !foreign[k] {
				out = append(out, f.create(v))
			}

			continue
		}

		used[idx] = true
		f.setType(raw[idx], v)
		out = append(out, raw[idx])
	}

	return out
}

// Field metadata accessors, one per People API entry type, so the
// source-ownership helpers stay generic.
func nameMeta(e *people.Name) *people.FieldMetadata          { return e.Metadata }
func orgMeta(e *people.Organization) *people.FieldMetadata   { return e.Metadata }
func bioMeta(e *people.Biography) *people.FieldMetadata      { return e.Metadata }
func emailMeta(e *people.EmailAddress) *people.FieldMetadata { return e.Metadata }
func phoneMeta(e *people.PhoneNumber) *people.FieldMetadata  { return e.Metadata }
func urlMeta(e *people.Url) *people.FieldMetadata            { return e.Metadata }
func addressMeta(e *people.Address) *people.FieldMetadata    { return e.Metadata }

// isContactSource reports whether an entry comes from the CONTACT source.
func isContactSource(m *people.FieldMetadata) bool {
	return m != nil && m.Source != nil && m.Source.Type == sourceTypeContact
}

// isOwned reports whether updateContact may send an entry: it comes from the
// CONTACT source or names no source at all (entries built locally).
func isOwned(m *people.FieldMetadata) bool {
	return m == nil || m.Source == nil || m.Source.Type == sourceTypeContact
}

// pickIndex returns the index of the first non-nil CONTACT-source entry, else
// of the first non-nil entry, else -1.
func pickIndex[E any](list []*E, meta func(*E) *people.FieldMetadata) int {
	first := -1

	for i, e := range list {
		if e == nil {
			continue
		}

		if isContactSource(meta(e)) {
			return i
		}

		if first < 0 {
			first = i
		}
	}

	return first
}

// preferContact returns the entry FromPerson reads single values from.
func preferContact[E any](list []*E, meta func(*E) *people.FieldMetadata) *E {
	if i := pickIndex(list, meta); i >= 0 {
		return list[i]
	}

	return nil
}

// pickOrNew returns the entry ToPerson writes single values to: the one
// pickIndex selects, or a new first entry when there is none.
func pickOrNew[E any](list *[]*E, meta func(*E) *people.FieldMetadata) *E {
	if i := pickIndex(*list, meta); i >= 0 {
		return (*list)[i]
	}

	e := new(E)
	*list = append([]*E{e}, *list...)

	return e
}

// splitOwned keeps the owned entries of list (nil ones included, so the
// order is kept) and returns the first foreign entry, the value FromPerson
// fell back to when no owned entry exists.
func splitOwned[E any](list []*E, meta func(*E) *people.FieldMetadata) ([]*E, *E) {
	owned, foreign := splitForeign(list, meta)
	if len(foreign) > 0 {
		return owned, foreign[0]
	}

	return owned, nil
}

// splitForeign separates owned entries from foreign (profile) ones.
func splitForeign[E any](list []*E, meta func(*E) *people.FieldMetadata) (owned, foreign []*E) {
	if list == nil {
		return nil, nil
	}

	for _, e := range list {
		if e != nil && !isOwned(meta(e)) {
			foreign = append(foreign, e)
			continue
		}

		owned = append(owned, e)
	}

	return owned, foreign
}

// foreignKeys returns the comparison keys of foreign entries.
func foreignKeys[E any](foreign []*E, key func(*E) string) map[string]bool {
	if len(foreign) == 0 {
		return nil
	}

	out := make(map[string]bool, len(foreign))
	for _, e := range foreign {
		if k := key(e); k != "" {
			out[k] = true
		}
	}

	return out
}

func fromAddress(a *people.Address) Address {
	return Address{
		Formatted:  a.FormattedValue,
		Street:     a.StreetAddress,
		City:       a.City,
		Region:     a.Region,
		PostalCode: a.PostalCode,
		Country:    a.Country,
		Type:       a.Type,
	}
}

func toAddress(a Address) *people.Address {
	return &people.Address{
		FormattedValue: a.Formatted,
		StreetAddress:  a.Street,
		City:           a.City,
		Region:         a.Region,
		PostalCode:     a.PostalCode,
		Country:        a.Country,
		Type:           a.Type,
	}
}

// membershipGroup returns the contact group resource name of m, or "" for nil
// and domain memberships.
func membershipGroup(m *people.Membership) string {
	if m == nil || m.ContactGroupMembership == nil {
		return ""
	}

	return m.ContactGroupMembership.ContactGroupResourceName
}

func isSystemGroup(resourceName string) bool {
	id, ok := strings.CutPrefix(resourceName, contactGroupPrefix)
	return ok && slices.Contains(systemGroupIDs, id)
}

// contactSource returns the CONTACT entry of p.Metadata.Sources, or nil.
func contactSource(p *people.Person) *people.Source {
	if p == nil || p.Metadata == nil {
		return nil
	}

	for _, s := range p.Metadata.Sources {
		if s != nil && s.Type == sourceTypeContact {
			return s
		}
	}

	return nil
}

// clonePerson copies p and every entry of the lists ToPerson edits, so edits
// never reach the caller's person. Nested pointers inside entries (field
// metadata, dates, membership details) stay shared: this package never
// writes through them.
func clonePerson(p *people.Person) *people.Person {
	if p == nil {
		return nil
	}

	cp := *p
	cp.Names = cloneEntries(p.Names)
	cp.Organizations = cloneEntries(p.Organizations)
	cp.Biographies = cloneEntries(p.Biographies)
	cp.EmailAddresses = cloneEntries(p.EmailAddresses)
	cp.PhoneNumbers = cloneEntries(p.PhoneNumbers)
	cp.Urls = cloneEntries(p.Urls)
	cp.Addresses = cloneEntries(p.Addresses)

	cp.Memberships = cloneEntries(p.Memberships)
	if p.Metadata != nil {
		md := *p.Metadata
		md.Sources = cloneEntries(p.Metadata.Sources)
		cp.Metadata = &md
	}

	return &cp
}

func cloneEntries[E any](list []*E) []*E {
	if list == nil {
		return nil
	}

	out := make([]*E, len(list))
	for i, e := range list {
		if e != nil {
			v := *e
			out[i] = &v
		}
	}

	return out
}
