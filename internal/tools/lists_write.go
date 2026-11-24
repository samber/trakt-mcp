package tools

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"unicode/utf8"

	"github.com/samber/trakt-mcp/internal/trakt"
)

// Argument names of the list write tools, as they appear in tool errors.
const (
	fieldName        = "name"
	fieldDescription = "description"
	fieldPrivacy     = "privacy"
	// fieldChanges names the "nothing to update" error of update_list.
	fieldChanges = "changes"
)

const (
	// maxListNameRunes bounds a list name. UNVERIFIED: not documented by
	// Trakt (the filtered spec states no limit); this reuses the title bound so a mistaken call stays small.
	maxListNameRunes = maxTitleRunes
	// maxListDescriptionRunes bounds a list description. UNVERIFIED: not
	// documented by Trakt (the filtered spec states no limit); a generous bound that keeps a mistaken
	// call small without cutting a realistic description.
	maxListDescriptionRunes = 1000
)

// listPrivacies are the visibility values of a custom list; link means anyone with the share link.
var listPrivacies = []string{"private", "link", "friends", "public"}

// listSortBys are the default sorts of a list. UNVERIFIED for custom lists:
// the filtered spec leaves sort_by free text, so this is the set the list
// items path takes.
var listSortBys = listItemSorts

// CreateListArgs are the arguments of create_list.
type CreateListArgs struct {
	Name           string `json:"name"                      jsonschema:"list name, at most 200 characters"`
	Description    string `json:"description,omitempty"     jsonschema:"list description, at most 1000 characters"`
	Privacy        string `json:"privacy,omitempty"         jsonschema:"private, link, friends or public (Trakt's default is private)"`
	DisplayNumbers *bool  `json:"display_numbers,omitempty" jsonschema:"show the rank of each item"`
	AllowComments  *bool  `json:"allow_comments,omitempty"  jsonschema:"let people comment on the list"`
	SortBy         string `json:"sort_by,omitempty"         jsonschema:"rank, added, title, released, runtime, popularity, percentage or votes"`
	SortHow        string `json:"sort_how,omitempty"        jsonschema:"asc or desc"`
}

// UpdateListArgs are the arguments of update_list. A field left out stays as
// it is; an empty description clears it.
type UpdateListArgs struct {
	ListID         string  `json:"list_id"                   jsonschema:"slug or numeric Trakt id of one of the user's own lists"`
	Name           *string `json:"name,omitempty"            jsonschema:"new name, at most 200 characters"`
	Description    *string `json:"description,omitempty"     jsonschema:"new description, at most 1000 characters; empty clears it"`
	Privacy        string  `json:"privacy,omitempty"         jsonschema:"private, link, friends or public"`
	DisplayNumbers *bool   `json:"display_numbers,omitempty" jsonschema:"show the rank of each item"`
	AllowComments  *bool   `json:"allow_comments,omitempty"  jsonschema:"let people comment on the list"`
	SortBy         string  `json:"sort_by,omitempty"         jsonschema:"rank, added, title, released, runtime, popularity, percentage or votes"`
	SortHow        string  `json:"sort_how,omitempty"        jsonschema:"asc or desc"`
}

// ListRefArgs name one of the connected user's own lists.
type ListRefArgs struct {
	ListID string `json:"list_id" jsonschema:"slug or numeric Trakt id of one of the user's own lists"`
}

// ListItemsEditArgs are the arguments of add_list_items and remove_list_items.
type ListItemsEditArgs struct {
	ListRefArgs
	ItemsArgs
}

// DeleteListResult confirms a deleted list.
type DeleteListResult struct {
	Deleted bool `json:"deleted"`
}

func (DeleteListResult) resultCount() int { return 1 }

// createListBody and updateListBody are the Trakt request bodies. Pointers
// and omitempty keep an argument the caller left out off the wire.
type createListBody struct {
	Name           string `json:"name"`
	Description    string `json:"description,omitempty"`
	Privacy        string `json:"privacy,omitempty"`
	DisplayNumbers *bool  `json:"display_numbers,omitempty"`
	AllowComments  *bool  `json:"allow_comments,omitempty"`
	SortBy         string `json:"sort_by,omitempty"`
	SortHow        string `json:"sort_how,omitempty"`
}

type updateListBody struct {
	Name           *string `json:"name,omitempty"`
	Description    *string `json:"description,omitempty"`
	Privacy        string  `json:"privacy,omitempty"`
	DisplayNumbers *bool   `json:"display_numbers,omitempty"`
	AllowComments  *bool   `json:"allow_comments,omitempty"`
	SortBy         string  `json:"sort_by,omitempty"`
	SortHow        string  `json:"sort_how,omitempty"`
}

func registerListWriteTools(s *Server) {
	Register(s, Spec{
		Name: "create_list", Title: "Create list", Access: Write,
		Description: "Create a custom list on the user's Trakt account, with an optional description, privacy (private, link, friends or public), item ranks, comments and default sort. " +
			"Returns the new list, whose id the other list tools take. Trakt caps the number of lists per account." + untrustedNote,
	}, createList)
	Register(s, Spec{
		Name: "update_list", Title: "Update list", Access: Write,
		Description: "Change the name, description, privacy, item ranks, comments or default sort of one of the user's own custom lists. " +
			"Only the arguments given change. Returns the updated list." + untrustedNote,
	}, updateList)
	Register(s, Spec{
		Name: "add_list_items", Title: "Add list items", Access: Write,
		Description: "Add up to 50 movies, TV shows, seasons or episodes to one of the user's own custom lists. " +
			"Returns how many were added or already there, and which items Trakt did not find. People cannot be added with this tool.",
	}, addListItems)
	Register(s, Spec{
		Name: "remove_list_items", Title: "Remove list items", Access: Destroy,
		Description: "Remove up to 50 movies, TV shows, seasons or episodes from one of the user's own custom lists; the entries, and any notes on them, are deleted from the list. " +
			"Returns how many were removed and which items Trakt did not find.",
	}, removeListItems)
	Register(s, Spec{
		Name: "delete_list", Title: "Delete list", Access: Destroy,
		Description: "Delete one of the user's own custom lists. This is irreversible: Trakt removes the list and all its items.",
	}, deleteList)
}

// path validates the id of one of the user's own lists.
func (a ListRefArgs) path() (string, error) {
	id, err := idArg(fieldListID, a.ListID, Slug|Numeric)

	return string(id), err
}

// boundedText trims s and checks it holds 1..max runes of valid UTF-8 (min 0
// when empty is allowed).
func boundedText(field, s string, limit int, allowEmpty bool) (string, error) {
	s = strings.TrimSpace(s)

	if !utf8.ValidString(s) || utf8.RuneCountInString(s) > limit || (s == "" && !allowEmpty) {
		return "", invalidArgument(field)
	}

	return s, nil
}

// listOptions validates the enum arguments shared by create and update; an
// empty value stays empty so it is left off the request.
func listOptions(privacy, sortBy, sortHow string) (p, by, how string, err error) {
	if p, err = oneOf(fieldPrivacy, privacy, "", listPrivacies); err != nil {
		return "", "", "", err
	}

	if by, err = oneOf(fieldSortBy, sortBy, "", listSortBys); err != nil {
		return "", "", "", err
	}

	if how, err = oneOf(fieldSortHow, sortHow, "", sortHows); err != nil {
		return "", "", "", err
	}

	return p, by, how, nil
}

// sendJSON marshals body and makes the call with it.
func sendJSON(body any, call func(io.Reader) (*http.Response, error)) (reply, error) {
	data, err := json.Marshal(body)
	if err != nil {
		return reply{}, fmt.Errorf("encode list body: %w", err)
	}

	return readReply(func() (*http.Response, error) { return call(bytes.NewReader(data)) })
}

func createList(ctx context.Context, c *trakt.UserClient, in CreateListArgs) (ListDetail, error) {
	name, err := boundedText(fieldName, in.Name, maxListNameRunes, false)
	if err != nil {
		return ListDetail{}, err
	}

	desc, err := boundedText(fieldDescription, in.Description, maxListDescriptionRunes, true)
	if err != nil {
		return ListDetail{}, err
	}

	privacy, sortBy, sortHow, err := listOptions(in.Privacy, in.SortBy, in.SortHow)
	if err != nil {
		return ListDetail{}, err
	}

	body := createListBody{
		Name: name, Description: desc, Privacy: privacy, DisplayNumbers: in.DisplayNumbers,
		AllowComments: in.AllowComments, SortBy: sortBy, SortHow: sortHow,
	}

	r, err := sendJSON(body, func(b io.Reader) (*http.Response, error) {
		return c.API().PostUsersListsCreateWithBody(ctx, userMe, jsonContentType, b)
	})
	if err != nil {
		return ListDetail{}, err
	}

	return parseListDetail(r.body)
}

func updateList(ctx context.Context, c *trakt.UserClient, in UpdateListArgs) (ListDetail, error) {
	listID, err := ListRefArgs{ListID: in.ListID}.path()
	if err != nil {
		return ListDetail{}, err
	}

	body := updateListBody{DisplayNumbers: in.DisplayNumbers, AllowComments: in.AllowComments}

	if in.Name != nil {
		name, err := boundedText(fieldName, *in.Name, maxListNameRunes, false)
		if err != nil {
			return ListDetail{}, err
		}

		body.Name = &name
	}

	if in.Description != nil {
		desc, err := boundedText(fieldDescription, *in.Description, maxListDescriptionRunes, true)
		if err != nil {
			return ListDetail{}, err
		}

		body.Description = &desc
	}

	if body.Privacy, body.SortBy, body.SortHow, err = listOptions(in.Privacy, in.SortBy, in.SortHow); err != nil {
		return ListDetail{}, err
	}

	if body == (updateListBody{}) {
		return ListDetail{}, invalidArgument(fieldChanges)
	}

	r, err := sendJSON(body, func(b io.Reader) (*http.Response, error) {
		return c.API().PutUsersListsListUpdateWithBody(ctx, userMe, listID, jsonContentType, b)
	})
	if err != nil {
		return ListDetail{}, err
	}

	return parseListDetail(r.body)
}

func addListItems(ctx context.Context, c *trakt.UserClient, in ListItemsEditArgs) (WriteResult, error) {
	listID, items, err := listItemsInput(in)
	if err != nil {
		return WriteResult{}, err
	}

	return writeItems(items, func(body io.Reader) (*http.Response, error) {
		return c.API().PostUsersListsListAddWithBody(ctx, userMe, listID, jsonContentType, body)
	})
}

func removeListItems(ctx context.Context, c *trakt.UserClient, in ListItemsEditArgs) (WriteResult, error) {
	listID, items, err := listItemsInput(in)
	if err != nil {
		return WriteResult{}, err
	}

	return writeItems(items, func(body io.Reader) (*http.Response, error) {
		return c.API().PostUsersListsListRemoveWithBody(ctx, userMe, listID, jsonContentType, body)
	})
}

// listItemsInput validates the list id and items of an add or remove call.
func listItemsInput(in ListItemsEditArgs) (string, []Item, error) {
	listID, err := in.path()
	if err != nil {
		return "", nil, err
	}

	items, err := parseItems(in.Items)

	return listID, items, err
}

func deleteList(ctx context.Context, c *trakt.UserClient, in ListRefArgs) (DeleteListResult, error) {
	listID, err := in.path()
	if err != nil {
		return DeleteListResult{}, err
	}

	if _, err := readReply(func() (*http.Response, error) {
		return c.API().DeleteUsersListsListDelete(ctx, userMe, listID)
	}); err != nil {
		return DeleteListResult{}, err
	}

	return DeleteListResult{Deleted: true}, nil
}
