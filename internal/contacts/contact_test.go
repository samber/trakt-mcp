package contacts

import (
	"errors"
	"reflect"
	"testing"
	"time"

	"google.golang.org/api/people/v1"
)

func fullPerson() *people.Person {
	return &people.Person{
		ResourceName: "people/c123",
		Etag:         "etag-1",
		Names: []*people.Name{
			{GivenName: testGivenName, FamilyName: testFamilyName, DisplayName: testDisplayName},
			{GivenName: "Other", FamilyName: "Name", DisplayName: "Other Name"},
		},
		Organizations: []*people.Organization{
			{Name: "Analytical Engines", Title: testJobTitleEngineer},
			{Name: testCompanyOld, Title: testJobTitleIntern},
		},
		Biographies: []*people.Biography{{Value: "First note"}, {Value: "Second note"}},
		EmailAddresses: []*people.EmailAddress{
			{Value: testEmailWork, Type: testTypeWork},
			nil,
			{Value: testEmailHome, Type: testTypeHome},
		},
		PhoneNumbers: []*people.PhoneNumber{{Value: testPhoneFR, Type: testTypeMobile}},
		Urls: []*people.Url{
			{Value: "https://www.linkedin.com/in/ada", Type: "linkedin"},
			{Value: "https://ada.example", Type: "homePage"},
		},
		Addresses: []*people.Address{{
			FormattedValue: testAddressFormatted,
			StreetAddress:  testStreet,
			City:           "London",
			Region:         "Greater London",
			PostalCode:     "N1",
			Country:        "UK",
			Type:           testTypeHome,
		}},
		Memberships: []*people.Membership{
			{ContactGroupMembership: &people.ContactGroupMembership{ContactGroupResourceName: testGroupMyContacts}},
			{DomainMembership: &people.DomainMembership{InViewerDomain: true}},
			{ContactGroupMembership: &people.ContactGroupMembership{ContactGroupResourceName: testGroupUser}},
		},
	}
}

func TestFromPerson_Full(t *testing.T) {
	t.Parallel()

	got := withoutRaw(FromPerson(fullPerson()))

	want := Contact{
		ResourceName: "people/c123",
		Etag:         "etag-1",
		GivenName:    testGivenName,
		FamilyName:   testFamilyName,
		DisplayName:  testDisplayName,
		Company:      "Analytical Engines",
		JobTitle:     testJobTitleEngineer,
		Notes:        "First note",
		Emails: []Typed{
			{Value: testEmailWork, Type: testTypeWork},
			{Value: testEmailHome, Type: testTypeHome},
		},
		Phones: []Typed{{Value: testPhoneFR, Type: testTypeMobile}},
		Websites: []Typed{
			{Value: "https://www.linkedin.com/in/ada", Type: WebsiteTypeLinkedIn},
			{Value: "https://ada.example", Type: "homePage"},
		},
		Addresses: []Address{{
			Formatted:  testAddressFormatted,
			Street:     testStreet,
			City:       "London",
			Region:     "Greater London",
			PostalCode: "N1",
			Country:    "UK",
			Type:       testTypeHome,
		}},
		Groups: []string{testGroupMyContacts, testGroupUser},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("FromPerson mismatch:\n got %#v\nwant %#v", got, want)
	}
}

func TestFromPerson_EmptyPerson(t *testing.T) {
	t.Parallel()

	if got := withoutRaw(FromPerson(&people.Person{})); !reflect.DeepEqual(got, Contact{}) {
		t.Fatalf("FromPerson(empty) = %#v, want zero Contact", got)
	}

	if got := FromPerson(nil); !reflect.DeepEqual(got, Contact{}) {
		t.Fatalf("FromPerson(nil) = %#v, want zero Contact", got)
	}
	// nil entries inside slices must not panic either.
	p := &people.Person{
		Names:         []*people.Name{nil},
		Organizations: []*people.Organization{nil},
		Biographies:   []*people.Biography{nil},
		Memberships:   []*people.Membership{nil, {}},
	}
	if got := withoutRaw(FromPerson(p)); !reflect.DeepEqual(got, Contact{}) {
		t.Fatalf("FromPerson(nil entries) = %#v, want zero Contact", got)
	}
}

func TestToPerson_RoundTrip(t *testing.T) {
	t.Parallel()

	c := withoutRaw(FromPerson(fullPerson()))
	// DisplayName is output-only in the People API, so ToPerson never sends it.
	c.DisplayName = ""

	got := withoutRaw(FromPerson(c.ToPerson()))
	if !reflect.DeepEqual(got, c) {
		t.Fatalf("round trip mismatch:\n got %#v\nwant %#v", got, c)
	}
}

// hasMappedEntries reports whether p carries any mapped multi-valued field or
// source metadata.
func hasMappedEntries(p *people.Person) bool {
	return p.Names != nil || p.Organizations != nil || p.Biographies != nil ||
		p.EmailAddresses != nil || p.PhoneNumbers != nil || p.Urls != nil ||
		p.Addresses != nil || p.Metadata != nil
}

func TestToPerson_OmitsEmptyStructs(t *testing.T) {
	t.Parallel()

	p := Contact{DisplayName: "Only Display"}.ToPerson()
	if hasMappedEntries(p) {
		t.Fatalf("ToPerson sent empty structs: %#v", p)
	}
	// A new contact always joins myContacts, so it shows in the main list.
	if len(p.Memberships) != 1 || membershipGroup(p.Memberships[0]) != myContactsGroup {
		t.Fatalf("memberships = %#v, want only %s", p.Memberships, myContactsGroup)
	}

	p = Contact{JobTitle: testJobTitleCTO}.ToPerson()
	if len(p.Organizations) != 1 || p.Organizations[0].Title != testJobTitleCTO || p.Organizations[0].Name != "" {
		t.Fatalf("organizations = %#v, want one entry with title only", p.Organizations)
	}
}

func TestToPerson_SetsEtagSource(t *testing.T) {
	t.Parallel()

	p := Contact{ResourceName: testResourceC1, Etag: "etag-42"}.ToPerson()
	if p.Etag != "etag-42" {
		t.Fatalf("Etag = %q, want etag-42", p.Etag)
	}

	if p.Metadata == nil || len(p.Metadata.Sources) != 1 {
		t.Fatalf("Metadata.Sources = %#v, want one source", p.Metadata)
	}

	src := p.Metadata.Sources[0]
	if src.Etag != "etag-42" || src.Type != sourceTypeContact {
		t.Fatalf("source = %#v, want etag-42 / %s", src, sourceTypeContact)
	}

	if p := (Contact{}).ToPerson(); p.Metadata != nil {
		t.Fatalf("Metadata = %#v without etag, want nil", p.Metadata)
	}

	// Fetched person: only the CONTACT source is sent, with its id and etag.
	fetched := FromPerson(&people.Person{
		ResourceName: testResourceC1,
		Etag:         "P",
		Metadata: &people.PersonMetadata{
			ObjectType: "PERSON",
			Sources: []*people.Source{
				{Type: testSourceProfile, Id: "prof", Etag: "PE"},
				{Type: sourceTypeContact, Id: "abc123", Etag: "C"},
			},
		},
	})
	p = fetched.ToPerson()

	want := []*people.Source{{Type: sourceTypeContact, Id: "abc123", Etag: "C"}}
	if p.Metadata == nil || !reflect.DeepEqual(p.Metadata.Sources, want) {
		t.Fatalf("Metadata = %#v, want sources %#v", p.Metadata, want)
	}
}

func withoutRaw(c Contact) Contact {
	c.raw = nil
	return c
}

func TestFromPerson_EtagFromContactSource(t *testing.T) {
	t.Parallel()

	p := &people.Person{
		Etag: "P",
		Metadata: &people.PersonMetadata{Sources: []*people.Source{
			{Type: testSourceProfile, Etag: "PE"},
			{Type: sourceTypeContact, Id: "abc", Etag: "C"},
		}},
	}
	if got := FromPerson(p).Etag; got != "C" {
		t.Fatalf("Etag = %q, want C", got)
	}
	// No CONTACT source: fall back to the person etag.
	p.Metadata.Sources = p.Metadata.Sources[:1]
	if got := FromPerson(p).Etag; got != "P" {
		t.Fatalf("Etag = %q, want P", got)
	}
}

func TestToPerson_PreservesExtraOrganizationsAndSubfields(t *testing.T) {
	t.Parallel()

	start := &people.Date{Year: 2020, Month: 1, Day: 2}
	c := FromPerson(&people.Person{Organizations: []*people.Organization{
		{Name: testCompanyAcme, Department: "R&D", Type: testTypeWork, StartDate: start, Current: true},
		{Name: testCompanyOld, Title: testJobTitleIntern},
	}})
	c.JobTitle = testJobTitleCTO
	got := c.ToPerson().Organizations

	want := []*people.Organization{
		{Name: testCompanyAcme, Title: testJobTitleCTO, Department: "R&D", Type: testTypeWork, StartDate: start, Current: true},
		{Name: testCompanyOld, Title: testJobTitleIntern},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("organizations:\n got %#v\nwant %#v", got, want)
	}
}

func TestToPerson_PreservesNameSubfields(t *testing.T) {
	t.Parallel()

	c := FromPerson(&people.Person{Names: []*people.Name{{
		GivenName: testGivenName, MiddleName: testMiddleNameKing, HonorificPrefix: "Lady",
		HonorificSuffix: "Countess", PhoneticGivenName: "Ay-da",
	}}})
	c.FamilyName = testFamilyName
	got := c.ToPerson().Names

	want := []*people.Name{{
		GivenName: testGivenName, FamilyName: testFamilyName, MiddleName: testMiddleNameKing, HonorificPrefix: "Lady",
		HonorificSuffix: "Countess", PhoneticGivenName: "Ay-da",
	}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("names:\n got %#v\nwant %#v", got, want)
	}
}

func TestToPerson_ClearsUnstructuredNameOnRename(t *testing.T) {
	t.Parallel()

	fetched := &people.Person{Names: []*people.Name{{
		GivenName: testGivenName, FamilyName: testFamilyName, UnstructuredName: testDisplayName,
	}}}

	cases := []struct {
		name, given, family, want string
	}{
		{name: "unchanged keeps it", given: testGivenName, family: testFamilyName, want: testDisplayName},
		{name: "given changed", given: "Augusta", family: testFamilyName},
		{name: "family changed", given: testGivenName, family: testMiddleNameKing},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			c := FromPerson(fetched)

			c.GivenName, c.FamilyName = tc.given, tc.family
			if got := c.ToPerson().Names[0].UnstructuredName; got != tc.want {
				t.Errorf("UnstructuredName = %q, want %q", got, tc.want)
			}

			if fetched.Names[0].UnstructuredName != testDisplayName {
				t.Error("ToPerson mutated the fetched person")
			}
		})
	}
}

func TestToPerson_ReusesMatchingAddressEntry(t *testing.T) {
	t.Parallel()

	c := FromPerson(&people.Person{Addresses: []*people.Address{{
		FormattedValue: testAddressFormatted, ExtendedAddress: "Flat 2", PoBox: "PO 9",
		CountryCode: "GB", Type: testTypeHome,
	}}})
	c, _ = Enrich(c, Contact{Addresses: []Address{{Formatted: "2 High St", Type: testTypeWork}}})
	got := c.ToPerson().Addresses

	want := []*people.Address{
		{FormattedValue: testAddressFormatted, ExtendedAddress: "Flat 2", PoBox: "PO 9", CountryCode: "GB", Type: testTypeHome},
		{FormattedValue: "2 High St", Type: testTypeWork},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("addresses:\n got %#v\nwant %#v", got, want)
	}
}

func TestToPerson_PreservesPrimaryFlag(t *testing.T) {
	t.Parallel()

	primary := &people.FieldMetadata{Primary: true}
	c := FromPerson(&people.Person{
		EmailAddresses: []*people.EmailAddress{{Value: testEmailShort, Type: testTypeWork, Metadata: primary}},
		PhoneNumbers:   []*people.PhoneNumber{{Value: "+33 6 12", CanonicalForm: "+33612", Metadata: primary}},
		Urls:           []*people.Url{{Value: "https://a.example", Metadata: primary}},
	})
	c.Emails[0].Type = testTypeHome

	p := c.ToPerson()
	if e := p.EmailAddresses[0]; e.Metadata != primary || e.Type != testTypeHome || e.Value != testEmailShort {
		t.Fatalf("email = %#v, want primary kept and type updated", e)
	}

	if ph := p.PhoneNumbers[0]; ph.Metadata != primary || ph.CanonicalForm != "+33612" {
		t.Fatalf("phone = %#v, want primary and canonical form kept", ph)
	}

	if u := p.Urls[0]; u.Metadata != primary {
		t.Fatalf("url = %#v, want primary kept", u)
	}
}

func TestToPerson_KeepsSystemMemberships(t *testing.T) {
	t.Parallel()

	c := FromPerson(fullPerson())
	c.Groups = []string{"contactGroups/new"}

	var got []string

	domain := 0

	for _, m := range c.ToPerson().Memberships {
		if m.DomainMembership != nil {
			domain++
			continue
		}

		got = append(got, m.ContactGroupMembership.ContactGroupResourceName)
	}

	want := []string{testGroupMyContacts, "contactGroups/new"}
	if !reflect.DeepEqual(got, want) || domain != 1 {
		t.Fatalf("memberships = %v (domain %d), want %v and 1 domain", got, domain, want)
	}
}

func TestToPerson_DropsRemovedEmail(t *testing.T) {
	t.Parallel()

	c := FromPerson(fullPerson())
	c.Emails = []Typed{{Value: testEmailHome, Type: testTypeHome}}

	got := c.ToPerson().EmailAddresses
	if len(got) != 1 || got[0].Value != testEmailHome {
		t.Fatalf("emails = %#v, want only ada@home.example", got)
	}
}

func TestToPerson_DoesNotMutateFetchedPerson(t *testing.T) {
	t.Parallel()

	p := fullPerson()
	c := FromPerson(p)
	c.GivenName, c.Emails[0].Type = "Changed", "other"
	_ = c.ToPerson()

	if !reflect.DeepEqual(p, fullPerson()) {
		t.Fatalf("source person mutated: %#v", p)
	}
}

func TestEnrichThenToPerson_NoDataLoss(t *testing.T) {
	t.Parallel()

	primary := &people.FieldMetadata{Primary: true}
	rich := func() *people.Person {
		return &people.Person{
			ResourceName: "people/c9",
			Etag:         "P",
			Metadata: &people.PersonMetadata{Sources: []*people.Source{
				{Type: sourceTypeContact, Id: "src9", Etag: "C"},
			}},
			Names: []*people.Name{{
				GivenName: testGivenName, FamilyName: testFamilyName, MiddleName: testMiddleNameKing,
				DisplayName: testDisplayName, Metadata: primary,
			}},
			Organizations: []*people.Organization{
				{Name: testCompanyAcme, Department: "R&D", Metadata: primary},
				{Name: testCompanyOld, Title: testJobTitleIntern},
			},
			Biographies:    []*people.Biography{{Value: "note", ContentType: "TEXT_PLAIN"}},
			EmailAddresses: []*people.EmailAddress{{Value: testEmailWork, Type: testTypeWork, Metadata: primary}},
			PhoneNumbers:   []*people.PhoneNumber{{Value: "+33612345678", Type: testTypeMobile, CanonicalForm: "+33612345678"}},
			Urls:           []*people.Url{{Value: "https://www.linkedin.com/in/ada", Type: WebsiteTypeLinkedIn}},
			Addresses:      []*people.Address{{FormattedValue: testStreet, PoBox: "PO 9", CountryCode: "GB"}},
			Birthdays:      []*people.Birthday{{Text: "1815-12-10"}},
			Memberships: []*people.Membership{
				{ContactGroupMembership: &people.ContactGroupMembership{ContactGroupResourceName: testGroupMyContacts}},
				{ContactGroupMembership: &people.ContactGroupMembership{ContactGroupResourceName: testGroupUser}},
			},
		}
	}

	merged, changed := Enrich(FromPerson(rich()), Contact{
		JobTitle: testJobTitleEngineer,
		Phones:   []Typed{{Value: testPhoneFR}, {Value: testPhoneUK, Type: testTypeWork}},
	})
	if want := []string{FieldOrganizations, FieldPhoneNumbers}; !reflect.DeepEqual(changed, want) {
		t.Fatalf("changed = %v, want %v", changed, want)
	}

	got := merged.ToPerson()

	want := rich()
	want.Organizations[0].Title = testJobTitleEngineer
	want.PhoneNumbers = append(want.PhoneNumbers, &people.PhoneNumber{Value: testPhoneUK, Type: testTypeWork})

	want.Metadata = &people.PersonMetadata{Sources: []*people.Source{{Type: sourceTypeContact, Id: "src9", Etag: "C"}}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("data lost:\n got %#v\nwant %#v", got, want)
	}

	if merged.Etag != "C" {
		t.Fatalf("Etag = %q, want C", merged.Etag)
	}
}

func TestUpdateFields(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		c    Contact
		want []string
	}{
		{"empty", Contact{ResourceName: testResourceC1, Etag: "e", DisplayName: "X"}, nil},
		{"family only", Contact{FamilyName: "L"}, []string{FieldNames}},
		{"job only", Contact{JobTitle: testJobTitleCTO}, []string{FieldOrganizations}},
		{"all", FromPerson(fullPerson()), []string{
			FieldAddresses, FieldBiographies, FieldEmailAddresses, FieldMemberships,
			FieldNames, FieldOrganizations, FieldPhoneNumbers, FieldURLs,
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			if got := tc.c.UpdateFields(); !reflect.DeepEqual(got, tc.want) {
				t.Fatalf("UpdateFields = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestNormalizeEmail(t *testing.T) {
	t.Parallel()

	cases := map[string]string{
		"A@B.com ":           testEmailShort,
		"\tUser@Example.ORG": "user@example.org",
		"":                   "",
		"   ":                "",
	}
	for in, want := range cases {
		if got := NormalizeEmail(in); got != want {
			t.Errorf("NormalizeEmail(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestRateLimitedError(t *testing.T) {
	t.Parallel()

	cases := []struct {
		d    time.Duration
		want string
	}{
		{30 * time.Second, "rate limited, retry after 30s"},
		{1500 * time.Millisecond, "rate limited, retry after 2s"},
		{0, "rate limited, retry after 0s"},
	}
	for _, tc := range cases {
		var err error = RateLimitedError{RetryAfter: tc.d}
		if got := err.Error(); got != tc.want {
			t.Errorf("Error() = %q, want %q", got, tc.want)
		}

		var rl RateLimitedError
		if !errors.As(err, &rl) || rl.RetryAfter != tc.d {
			t.Errorf("errors.As failed for %v", tc.d)
		}
	}
}

func TestSentinelErrorsDistinct(t *testing.T) {
	t.Parallel()

	errs := []error{ErrNotFound, ErrConflict, ErrUpstreamAuth}
	for i, a := range errs {
		for j, b := range errs {
			if i != j && errors.Is(a, b) {
				t.Fatalf("%v matches %v", a, b)
			}
		}
	}
}

func TestToPerson_CreateIncludesMyContacts(t *testing.T) {
	t.Parallel()

	groupsOf := func(p *people.Person) []string {
		var out []string
		for _, m := range p.Memberships {
			out = append(out, membershipGroup(m))
		}

		return out
	}

	cases := []struct {
		groups []string
		want   []string
	}{
		{nil, []string{testGroupMyContacts}},
		{[]string{testGroupUser}, []string{testGroupMyContacts, testGroupUser}},
		// Already listed: kept once, in the caller's order.
		{[]string{testGroupUser, testGroupMyContacts}, []string{testGroupUser, testGroupMyContacts}},
	}
	for _, tc := range cases {
		got := groupsOf(Contact{GivenName: testGivenName, Groups: tc.groups}.ToPerson())
		if !reflect.DeepEqual(got, tc.want) {
			t.Errorf("groups %v: memberships = %v, want %v", tc.groups, got, tc.want)
		}
	}
	// A fetched contact keeps exactly its own memberships.
	fetched := FromPerson(&people.Person{ResourceName: testResourceC1, Memberships: []*people.Membership{
		{ContactGroupMembership: &people.ContactGroupMembership{ContactGroupResourceName: testGroupUser}},
	}})
	if got := groupsOf(fetched.ToPerson()); !reflect.DeepEqual(got, []string{testGroupUser}) {
		t.Errorf("fetched memberships = %v, want only contactGroups/abc", got)
	}
}

func TestGroupError(t *testing.T) {
	t.Parallel()

	if got := (GroupError{Group: "x"}).Error(); got != `group "x" not found` {
		t.Errorf("not found = %q", got)
	}

	if got := (GroupError{Group: "x", Matches: 3}).Error(); got != `group "x" is ambiguous (3 matches)` {
		t.Errorf("ambiguous = %q", got)
	}

	if got := (UpstreamStatusError{Op: "list", Status: 502}).Error(); got != "people list: google returned HTTP 502" {
		t.Errorf("upstream = %q", got)
	}
}

// profileLinked is a contact linked to a Google profile: the profile's
// entries come first, as Google lists them, followed by the contact's own.
func profileLinked() *people.Person {
	profile := &people.FieldMetadata{Primary: true, Source: &people.Source{Type: testSourceProfile, Id: "prof"}}
	contact := &people.FieldMetadata{Source: &people.Source{Type: sourceTypeContact, Id: "src1"}}

	return &people.Person{
		ResourceName: testResourceC1,
		Etag:         "P",
		Metadata: &people.PersonMetadata{Sources: []*people.Source{
			{Type: testSourceProfile, Id: "prof", Etag: "PE"},
			{Type: sourceTypeContact, Id: "src1", Etag: "C"},
		}},
		Names: []*people.Name{
			{GivenName: "Profile", FamilyName: "Name", Metadata: profile},
			{GivenName: testGivenName, FamilyName: testFamilyName, Metadata: contact},
		},
		Organizations: []*people.Organization{
			{Name: "Profile Corp", Metadata: profile},
			{Name: testCompanyAcme, Title: testJobTitleCTO, Metadata: contact},
		},
		Biographies: []*people.Biography{
			{Value: "profile bio", Metadata: profile},
			{Value: "contact note", Metadata: contact},
		},
		EmailAddresses: []*people.EmailAddress{
			{Value: "ada@profile.example", Metadata: profile},
			{Value: testEmailWork, Type: testTypeWork, Metadata: contact},
		},
		PhoneNumbers: []*people.PhoneNumber{{Value: testPhoneUS, Metadata: profile}},
		Urls:         []*people.Url{{Value: "https://profile.example", Metadata: profile}},
		Addresses:    []*people.Address{{FormattedValue: "Profile Street", Metadata: profile}},
	}
}

// assertNoProfileEntry fails when p carries any entry from a non-CONTACT source.
func assertNoProfileEntry(t *testing.T, p *people.Person) {
	t.Helper()

	check := func(field string, m *people.FieldMetadata) {
		if !isOwned(m) {
			t.Errorf("%s: sent a %s-source entry", field, m.Source.Type)
		}
	}
	for _, e := range p.Names {
		check("names", e.Metadata)
	}

	for _, e := range p.Organizations {
		check("organizations", e.Metadata)
	}

	for _, e := range p.Biographies {
		check("biographies", e.Metadata)
	}

	for _, e := range p.EmailAddresses {
		check("emailAddresses", e.Metadata)
	}

	for _, e := range p.PhoneNumbers {
		check("phoneNumbers", e.Metadata)
	}

	for _, e := range p.Urls {
		check("urls", e.Metadata)
	}

	for _, e := range p.Addresses {
		check("addresses", e.Metadata)
	}
}

func TestFromPerson_PrefersContactSource(t *testing.T) {
	t.Parallel()

	c := FromPerson(profileLinked())
	if c.GivenName != testGivenName || c.FamilyName != testFamilyName {
		t.Errorf("name = %q %q, want the CONTACT name Ada Lovelace", c.GivenName, c.FamilyName)
	}

	if c.Company != testCompanyAcme || c.JobTitle != testJobTitleCTO {
		t.Errorf("organization = %q/%q, want the CONTACT one Acme/CTO", c.Company, c.JobTitle)
	}

	if c.Notes != "contact note" {
		t.Errorf("notes = %q, want the CONTACT biography", c.Notes)
	}
	// Profile values stay visible for matching and display.
	if len(c.Emails) != 2 || len(c.Phones) != 1 || len(c.Websites) != 1 || len(c.Addresses) != 1 {
		t.Errorf("multi-valued fields = %d/%d/%d/%d, want 2/1/1/1 incl. profile entries",
			len(c.Emails), len(c.Phones), len(c.Websites), len(c.Addresses))
	}
	// Without a CONTACT entry, the first entry is still read.
	p := profileLinked()

	p.Names = p.Names[:1]
	if got := FromPerson(p).GivenName; got != "Profile" {
		t.Errorf("fallback name = %q, want Profile", got)
	}
}

func TestToPerson_UpdateSendsOnlyContactEntries(t *testing.T) {
	t.Parallel()

	c := FromPerson(profileLinked())
	c.GivenName = "Augusta"
	c.Emails = append(c.Emails, Typed{Value: "new@example.com"})
	got := c.ToPerson()
	assertNoProfileEntry(t, got)

	if len(got.Names) != 1 || got.Names[0].GivenName != "Augusta" || got.Names[0].FamilyName != testFamilyName {
		t.Errorf("names = %#v, want the CONTACT entry renamed to Augusta", got.Names)
	}

	if len(got.Organizations) != 1 || got.Organizations[0].Name != testCompanyAcme {
		t.Errorf("organizations = %#v, want only Acme", got.Organizations)
	}

	var emails []string
	for _, e := range got.EmailAddresses {
		emails = append(emails, e.Value)
	}

	if want := []string{testEmailWork, "new@example.com"}; !reflect.DeepEqual(emails, want) {
		t.Errorf("emails = %v, want %v", emails, want)
	}

	if len(got.PhoneNumbers) != 0 || len(got.Urls) != 0 || len(got.Addresses) != 0 {
		t.Errorf("profile-only phones/urls/addresses were copied: %#v %#v %#v",
			got.PhoneNumbers, got.Urls, got.Addresses)
	}
}

func TestEnrichThenToPerson_ProfileLinked(t *testing.T) {
	t.Parallel()

	merged, changed := Enrich(FromPerson(profileLinked()), Contact{
		// Already on the profile: not added, not copied into the contact.
		Emails: []Typed{{Value: "ADA@profile.example"}, {Value: "extra@example.com"}},
		Phones: []Typed{{Value: testPhoneUS}, {Value: testPhoneUK}},
	})
	if want := []string{FieldEmailAddresses, FieldPhoneNumbers}; !reflect.DeepEqual(changed, want) {
		t.Fatalf("changed = %v, want %v", changed, want)
	}

	got := merged.ToPerson()
	assertNoProfileEntry(t, got)

	if len(got.EmailAddresses) != 2 || got.EmailAddresses[1].Value != "extra@example.com" {
		t.Errorf("emails = %#v, want ada@example.com and extra@example.com", got.EmailAddresses)
	}

	if len(got.PhoneNumbers) != 1 || got.PhoneNumbers[0].Value != testPhoneUK {
		t.Errorf("phones = %#v, want only the new number", got.PhoneNumbers)
	}
}

func TestToPerson_ProfileOnlyNameIsNotCopied(t *testing.T) {
	t.Parallel()

	p := profileLinked()
	p.Names = p.Names[:1]
	c := FromPerson(p)

	c.Company = "Changed"
	if got := c.ToPerson().Names; len(got) != 0 {
		t.Errorf("names = %#v, want none: the profile name is not the contact's", got)
	}

	c.GivenName = "Renamed"

	got := c.ToPerson().Names
	if len(got) != 1 || got[0].GivenName != "Renamed" || got[0].FamilyName != "Name" || !isOwned(got[0].Metadata) {
		t.Errorf("names = %#v, want one new contact name Renamed Name", got)
	}
}
