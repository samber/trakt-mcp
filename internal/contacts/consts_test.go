package contacts

// Fixture values shared by the contact and merge tests. Each one is reused in
// several tests, so naming it keeps the expected values in sync.
const (
	// testGivenName is the given name of the fixture contact.
	testGivenName = "Ada"
	// testJobTitleEngineer is the job title of the fixture contact's first organization.
	testJobTitleEngineer = "Engineer"
	// testJobTitleIntern is the job title of the fixture contact's secondary organization.
	testJobTitleIntern = "Intern"
	// testJobTitleCTO is a job title used to check that a title can be set without a company.
	testJobTitleCTO = "CTO"
	// testJobTitleCEO is a job title used to check that enrichment keeps an existing title.
	testJobTitleCEO = "CEO"
	// testCompanyAcme is a company name used by organization overlay tests.
	testCompanyAcme = "Acme"
	// testMiddleNameKing is a name sub-field (middle name or family name) that mapping must preserve.
	testMiddleNameKing = "King"
	// testEmailWork is the fixture contact's work email address.
	testEmailWork = "ada@example.com"
	// testEmailHome is the fixture contact's home email address.
	testEmailHome = "ada@home.example"
	// testEmailShort is a minimal email address for enrichment and normalization tests.
	testEmailShort = "a@b.com"
	// testPhoneFR is the fixture contact's mobile number, with spaces as typed by users.
	testPhoneFR = "+33 6 12 34 56 78"
	// testPhoneUK is a second phone number for phone overlay tests.
	testPhoneUK = "+44 20 0000 0000"
	// testPhoneUS is a third phone number for enrichment tests.
	testPhoneUS = "+1 555 0100"
	// testAddressFormatted is the formatted form of the fixture contact's address.
	testAddressFormatted = "1 Main St, London"
	// testStreet is the street line of the fixture contact's address.
	testStreet = "1 Main St"
	// testGroupMyContacts is the resource name of the system "My Contacts" group.
	testGroupMyContacts = "contactGroups/myContacts"
	// testGroupUser is the resource name of a user-created group.
	testGroupUser = "contactGroups/abc"
	// testResourceC1 is the resource name of the contact used by merge and etag tests.
	testResourceC1 = "people/c1"
	// testSourceProfile is the People API source type of a Google profile entry.
	testSourceProfile = "PROFILE"
	// testFamilyName is the family name of the fixture contact.
	testFamilyName = "Lovelace"
	// testDisplayName is the output-only display name of the fixture contact.
	testDisplayName = "Ada Lovelace"
	// testCompanyOld is the name of a secondary organization that overlay tests must keep.
	testCompanyOld = "Old Corp"
	// testTypeWork is the People API "work" type of an email, phone or address.
	testTypeWork = "work"
	// testTypeHome is the People API "home" type of an email, phone or address.
	testTypeHome = "home"
	// testTypeMobile is the People API "mobile" type of a phone number.
	testTypeMobile = "mobile"
)
