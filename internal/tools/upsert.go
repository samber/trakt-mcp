package tools

import (
	"context"
	"slices"
	"strings"

	"github.com/samber/google-contacts-mcp/internal/contacts"
)

// Upsert outcomes (spec §8.1).
const (
	statusCreated   = "created"
	statusEnriched  = "enriched"
	statusUnchanged = "unchanged"
	statusAmbiguous = "ambiguous"
)

// UpsertInput is the upsert_contact_by_email input.
type UpsertInput struct {
	Email      string         `json:"email" jsonschema:"e-mail address identifying the contact"`
	GivenName  string         `json:"given_name,omitempty" jsonschema:"set only if the contact has none"`
	FamilyName string         `json:"family_name,omitempty" jsonschema:"set only if the contact has none"`
	Company    string         `json:"company,omitempty" jsonschema:"set only if the contact has none"`
	JobTitle   string         `json:"job_title,omitempty" jsonschema:"set only if the contact has none"`
	Notes      string         `json:"notes,omitempty" jsonschema:"set only if the contact has none"`
	Emails     []TypedValue   `json:"emails,omitempty" jsonschema:"extra e-mails, appended when missing"`
	Phones     []TypedValue   `json:"phones,omitempty" jsonschema:"appended when missing (compared by digits)"`
	Websites   []TypedValue   `json:"websites,omitempty" jsonschema:"appended when missing; LinkedIn has type linkedin"`
	Addresses  []AddressValue `json:"addresses,omitempty" jsonschema:"appended when missing"`
	Group      string         `json:"group,omitempty" jsonschema:"contact group to ensure membership in: resource name or user group name"`
}

// Candidate is one of several contacts sharing the upserted e-mail.
type Candidate struct {
	ResourceName string `json:"resource_name"`
	DisplayName  string `json:"display_name,omitempty"`
	Organization string `json:"organization,omitempty"`
}

// UpsertOutput is the upsert_contact_by_email output.
type UpsertOutput struct {
	Status        string         `json:"status" jsonschema:"created, enriched, unchanged or ambiguous"`
	Contact       *ContactOutput `json:"contact,omitempty" jsonschema:"the contact, absent when ambiguous"`
	Candidates    []Candidate    `json:"candidates,omitempty" jsonschema:"matching contacts when ambiguous"`
	ChangedFields []string       `json:"changed_fields,omitempty" jsonschema:"People API fields written when enriched"`
}

// resultCount is 1 for the created or matched contact, else the number of
// ambiguous candidates.
func (o UpsertOutput) resultCount() int {
	if o.Contact != nil {
		return 1
	}

	return len(o.Candidates)
}

// upsertContact implements spec §8.1.
func upsertContact(ctx context.Context, p contacts.People, in UpsertInput) (UpsertOutput, error) {
	email := strings.TrimSpace(in.Email)

	key := contacts.NormalizeEmail(email)
	if key == "" {
		return UpsertOutput{}, newToolError("email is required")
	}
	// Resolve the group first so a bad name fails before any write.
	group, err := resolveUpsertGroup(ctx, p, in.Group)
	if err != nil {
		return UpsertOutput{}, err
	}

	incoming := contacts.Contact{
		GivenName:  in.GivenName,
		FamilyName: in.FamilyName,
		Company:    in.Company,
		JobTitle:   in.JobTitle,
		Notes:      in.Notes,
		Emails:     fromTypedValues(in.Emails),
		Phones:     fromTypedValues(in.Phones),
		Websites:   fromTypedValues(in.Websites),
		Addresses:  fromAddressValues(in.Addresses),
	}

	// searchContacts matches prefixes: keep only exact e-mail matches.
	found, err := p.Search(ctx, key, maxSearchLimit)
	if err != nil {
		return UpsertOutput{}, err
	}

	matches := exactEmailMatches(found, key)

	switch len(matches) {
	case 0:
		if !hasEmail(incoming, key) {
			incoming.Emails = append([]contacts.Typed{{Value: email}}, incoming.Emails...)
		}

		if group != "" {
			incoming.Groups = []string{group}
		}

		created, err := p.Create(ctx, incoming)
		if err != nil {
			return UpsertOutput{}, err
		}

		out := toOutput(created)

		return UpsertOutput{Status: statusCreated, Contact: &out}, nil
	case 1:
		return enrich(ctx, p, matches[0].ResourceName, incoming, group)
	default:
		out := UpsertOutput{Status: statusAmbiguous, Candidates: make([]Candidate, 0, len(matches))}
		for _, c := range matches {
			out.Candidates = append(out.Candidates, Candidate{ResourceName: c.ResourceName, DisplayName: c.DisplayName, Organization: c.Company})
		}

		return out, nil
	}
}

// resolveUpsertGroup validates and resolves the optional group name. It
// returns "" when no group was requested.
func resolveUpsertGroup(ctx context.Context, p contacts.People, name string) (string, error) {
	if name == "" {
		return "", nil
	}

	if err := checkGroup(name); err != nil {
		return "", err
	}

	return contacts.ResolveGroup(ctx, p, name)
}

// exactEmailMatches keeps the contacts that carry key as an e-mail address.
func exactEmailMatches(found []contacts.Contact, key string) []contacts.Contact {
	var matches []contacts.Contact

	for _, c := range found {
		if hasEmail(c, key) {
			matches = append(matches, c)
		}
	}

	return matches
}

// enrich fills the matched contact from incoming and ensures group
// membership. Search results may be partial, so it re-reads the contact for
// the complete person and a fresh etag.
func enrich(ctx context.Context, p contacts.People, resourceName string, incoming contacts.Contact, group string) (UpsertOutput, error) {
	existing, err := p.Get(ctx, resourceName)
	if err != nil {
		return UpsertOutput{}, err
	}

	merged, changed := contacts.Enrich(existing, incoming)

	result := existing
	if len(changed) > 0 {
		if result, err = p.Update(ctx, merged, changed); err != nil {
			return UpsertOutput{}, err
		}
	}

	if group != "" && !slices.Contains(result.Groups, group) {
		if err := p.ModifyMembers(ctx, group, []string{result.ResourceName}, nil); err != nil {
			// A bare ErrNotFound is Google's 404 on the group itself.
			if isBareNotFound(err) {
				return UpsertOutput{}, newToolError(msgGroupNotFound)
			}

			return UpsertOutput{}, err
		}

		result.Groups = append(slices.Clone(result.Groups), group)
		changed = append(changed, contacts.FieldMemberships)
		slices.Sort(changed)
	}

	out := toOutput(result)
	if len(changed) == 0 {
		return UpsertOutput{Status: statusUnchanged, Contact: &out}, nil
	}

	return UpsertOutput{Status: statusEnriched, Contact: &out, ChangedFields: changed}, nil
}

func hasEmail(c contacts.Contact, key string) bool {
	return slices.ContainsFunc(c.Emails, func(e contacts.Typed) bool { return contacts.NormalizeEmail(e.Value) == key })
}
