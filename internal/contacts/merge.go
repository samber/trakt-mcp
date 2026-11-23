package contacts

import (
	"slices"
	"strings"
	"unicode"
)

// NormalizeEmail trims and lowercases an e-mail address for comparison.
func NormalizeEmail(s string) string {
	return strings.ToLower(strings.TrimSpace(s))
}

// Enrich merges incoming into existing without modifying or removing any
// existing value (spec §8.1). Single-valued fields are set only when empty;
// multi-valued values are appended when no equivalent value exists.
// ResourceName, Etag, DisplayName and Groups always come from existing.
// changed lists the People API fields that differ, sorted, or nil.
func Enrich(existing, incoming Contact) (Contact, []string) {
	merged := existing

	var changed []string

	mark := func(field string) {
		if !slices.Contains(changed, field) {
			changed = append(changed, field)
		}
	}

	fill := func(dst *string, src, field string) {
		if *dst == "" && strings.TrimSpace(src) != "" {
			*dst = src

			mark(field)
		}
	}
	fill(&merged.GivenName, incoming.GivenName, FieldNames)
	fill(&merged.FamilyName, incoming.FamilyName, FieldNames)
	fill(&merged.Company, incoming.Company, FieldOrganizations)
	fill(&merged.JobTitle, incoming.JobTitle, FieldOrganizations)
	fill(&merged.Notes, incoming.Notes, FieldBiographies)

	var ok bool
	if merged.Emails, ok = appendNew(existing.Emails, incoming.Emails, emailKey); ok {
		mark(FieldEmailAddresses)
	}

	if merged.Phones, ok = appendNew(existing.Phones, incoming.Phones, phoneKey); ok {
		mark(FieldPhoneNumbers)
	}

	if merged.Websites, ok = appendNew(existing.Websites, incoming.Websites, websiteKey); ok {
		mark(FieldURLs)
	}

	if merged.Addresses, ok = appendNew(existing.Addresses, incoming.Addresses, addressKey); ok {
		mark(FieldAddresses)
	}

	slices.Sort(changed)

	return merged, changed
}

// appendNew returns existing plus every incoming item whose key is non-empty
// and not already present. It copies before appending so the result never
// shares a backing array with the caller's slice.
func appendNew[T any](existing, incoming []T, key func(T) string) ([]T, bool) {
	out := existing
	copied := false

	for _, item := range incoming {
		k := key(item)
		if k == "" || slices.ContainsFunc(out, func(e T) bool { return key(e) == k }) {
			continue
		}

		if !copied {
			out = slices.Clone(existing)
			copied = true
		}

		out = append(out, item)
	}

	return out, copied
}

func emailKey(t Typed) string { return NormalizeEmail(t.Value) }

// phoneKey keeps digits only, so "+33 6 12" equals "+33612". A value with no
// digit falls back to its trimmed lowercase text.
func phoneKey(t Typed) string {
	digits := strings.Map(func(r rune) rune {
		if r >= '0' && r <= '9' {
			return r
		}

		return -1
	}, t.Value)
	if digits != "" {
		return digits
	}

	return strings.ToLower(strings.TrimSpace(t.Value))
}

// websiteKey lowercases and drops the scheme, a leading "www." and trailing
// slashes, so "https://www.linkedin.com/in/x/" equals "linkedin.com/in/x".
func websiteKey(t Typed) string {
	s := strings.ToLower(strings.TrimSpace(t.Value))
	if i := strings.Index(s, "://"); i >= 0 {
		s = s[i+len("://"):]
	}

	s = strings.TrimPrefix(s, "www.")

	return strings.TrimRight(s, "/")
}

// addressKey compares by formatted value, lowercased with whitespace
// collapsed. Without a formatted value it falls back to the joined
// components, so two structured-only addresses are not all equal.
func addressKey(a Address) string {
	s := a.Formatted
	if strings.TrimSpace(s) == "" {
		s = strings.Join([]string{a.Street, a.City, a.Region, a.PostalCode, a.Country}, " ")
	}

	return strings.Join(strings.FieldsFunc(strings.ToLower(s), unicode.IsSpace), " ")
}
