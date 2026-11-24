package tools

import (
	"errors"
	"regexp"
	"strings"
)

// ID is a validated Trakt identifier: safe to put in a request path, since
// it holds only lowercase letters, digits and hyphens.
type ID string

// IDKinds is the set of identifier forms a tool argument accepts.
type IDKinds uint8

const (
	// Slug is a Trakt slug such as breaking-bad.
	Slug IDKinds = 1 << iota
	// Numeric is a Trakt numeric id such as 1388.
	Numeric
	// IMDbTitle is an IMDb title id such as tt0903747.
	IMDbTitle
	// IMDbPerson is an IMDb person id such as nm0186505.
	IMDbPerson
)

const (
	// maxIDLength bounds an identifier: the longest Trakt slugs (long titles
	// plus a year suffix) stay well under it, and a bound keeps a hostile
	// argument from reaching Trakt as a huge path.
	maxIDLength = 128
	// imdbTitlePrefix and imdbPersonPrefix start IMDb title and person ids.
	imdbTitlePrefix  = "tt"
	imdbPersonPrefix = "nm"
)

// Identifier shapes (spec §8). Each excludes "/", "?", "#", "%", "." and
// whitespace, so no path or query injection reaches Trakt.
var (
	slugPattern    = regexp.MustCompile(`^[a-z0-9-]+$`)
	numericPattern = regexp.MustCompile(`^[0-9]+$`)
	imdbPattern    = regexp.MustCompile(`^(tt|nm)[0-9]+$`)
)

// errInvalidID is returned by ParseID; callers turn it into a tool error
// naming the field with idArg.
var errInvalidID = errors.New("invalid id")

// ParseID returns s as an ID when it has one of the allowed forms. An IMDb
// id of a form not allowed (a person id where a title is expected) is
// rejected rather than sent to Trakt as a slug. A numeric value is accepted
// when Numeric or Slug is allowed, since digits form a valid slug too.
func ParseID(s string, allow IDKinds) (ID, error) {
	if s == "" || len(s) > maxIDLength {
		return "", errInvalidID
	}

	switch {
	case imdbPattern.MatchString(s):
		if (strings.HasPrefix(s, imdbTitlePrefix) && allow&IMDbTitle != 0) ||
			(strings.HasPrefix(s, imdbPersonPrefix) && allow&IMDbPerson != 0) {
			return ID(s), nil
		}
	case numericPattern.MatchString(s):
		if allow&(Numeric|Slug) != 0 {
			return ID(s), nil
		}
	case slugPattern.MatchString(s):
		if allow&Slug != 0 {
			return ID(s), nil
		}
	}

	return "", errInvalidID
}

// idArg parses the tool argument field, failing with a tool error that names
// the field but never echoes the value.
func idArg(field, s string, allow IDKinds) (ID, error) {
	id, err := ParseID(s, allow)
	if err != nil {
		return "", invalidArgument(field)
	}

	return id, nil
}
