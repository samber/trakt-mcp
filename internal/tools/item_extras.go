package tools

import (
	"context"

	"github.com/samber/trakt-mcp/internal/trakt"
)

const (
	// fieldEmbedMarkdown and fieldIncludeGuestStars name the type-specific
	// arguments in tool errors.
	fieldEmbedMarkdown     = "embed_markdown"
	fieldIncludeGuestStars = "include_guest_stars"
)

// ListCommentsArgs are the arguments of list_comments.
type ListCommentsArgs struct {
	ItemRef
	CommentOpts
}

// ListPeopleArgs are the arguments of list_people.
type ListPeopleArgs struct {
	ItemRef
	IncludeGuestStars *bool `json:"include_guest_stars,omitempty" jsonschema:"type show only: also return guest stars (default false)"`
}

// ListVideosArgs are the arguments of list_videos.
type ListVideosArgs struct {
	ItemRef
	EmbedMarkdown *bool `json:"embed_markdown,omitempty" jsonschema:"type movie or show only: also return each video as a ready markdown link (default true)"`
}

// ListItemListsArgs are the arguments of list_item_lists.
type ListItemListsArgs struct {
	ItemRef
	PageArgs
	ListType string `json:"list_type,omitempty" jsonschema:"all, personal, official, watchlist or favorites (default all)"`
	Sort     string `json:"sort,omitempty"      jsonschema:"popular, likes, comments, items, added or updated (default popular)"`
}

func registerItemExtraTools(s *Server) {
	Register(s, Spec{
		Name: "list_comments", Title: "List comments", Access: Read,
		Description: "List the comments Trakt users wrote about a movie, TV show, TV show season or TV show episode, with likes, " +
			"replies, rating, review flag and author. Takes sort, limit and page. " + itemTypesNote + commentNote,
	}, listComments)
	Register(s, Spec{
		Name: "list_people", Title: "List people", Access: Read,
		Description: "Get the cast (name, character) and crew grouped by department of a movie, TV show, TV show season or " +
			"TV show episode. Long credit lists are cut. include_guest_stars applies to type show only. " + itemTypesNote,
	}, listPeople)
	Register(s, Spec{
		Name: "list_videos", Title: "List videos", Access: Read,
		Description: "List the trailers and other videos of a movie, TV show, TV show season or TV show episode with title, " +
			"type, site, url, publication date and a ready markdown link. embed_markdown applies to type movie and show only. " + itemTypesNote,
	}, listVideos)
	Register(s, Spec{
		Name: "list_item_lists", Title: "List lists with an item", Access: Read,
		Description: "List the Trakt lists that contain a movie, TV show, TV show season or TV show episode, by list type " +
			"and sort. Takes limit and page. " + itemTypesNote + untrustedNote,
	}, listItemLists)
}

func listComments(ctx context.Context, c *trakt.UserClient, in ListCommentsArgs) (Listing[Comment], error) {
	if err := in.check(typeMovie, typeShow, typeSeason, typeEpisode); err != nil {
		return Listing[Comment]{}, err
	}

	switch in.Type {
	case typeMovie:
		return fetchMovieComments(ctx, c, MovieCommentsArgs{MovieRef: in.movieRef(), CommentOpts: in.CommentOpts})
	case typeShow:
		return fetchShowComments(ctx, c, ShowCommentsArgs{ShowRef: in.showRef(), CommentOpts: in.CommentOpts})
	case typeSeason:
		return fetchSeasonComments(ctx, c, SeasonCommentsArgs{SeasonRef: in.seasonRef(), CommentOpts: in.CommentOpts})
	default:
		return fetchEpisodeComments(ctx, c, EpisodeCommentsArgs{EpisodeRef: in.episodeRef(), CommentOpts: in.CommentOpts})
	}
}

func listPeople(ctx context.Context, c *trakt.UserClient, in ListPeopleArgs) (People, error) {
	// Only a TV show's cast list has guest stars to include; refuse the
	// argument elsewhere rather than ignore it.
	if err := in.check(typeMovie, typeShow, typeSeason, typeEpisode); err != nil {
		return People{}, err
	}

	if in.IncludeGuestStars != nil && in.Type != typeShow {
		return People{}, invalidArgument(fieldIncludeGuestStars)
	}

	return itemFetchers[People]{
		movie: fetchMoviePeople,
		show: func(ctx context.Context, c *trakt.UserClient, r ShowRef) (People, error) {
			return fetchShowPeople(ctx, c, ShowPeopleArgs{ShowRef: r, IncludeGuestStars: in.IncludeGuestStars != nil && *in.IncludeGuestStars})
		},
		season:  fetchSeasonPeople,
		episode: fetchEpisodePeople,
	}.fetch(ctx, c, in.ItemRef)
}

func listVideos(ctx context.Context, c *trakt.UserClient, in ListVideosArgs) (Listing[Video], error) {
	// Season and episode videos always carry the markdown link: refuse the
	// argument rather than ignore it.
	if err := in.check(typeMovie, typeShow, typeSeason, typeEpisode); err != nil {
		return Listing[Video]{}, err
	}

	if in.EmbedMarkdown != nil && in.Type != typeMovie && in.Type != typeShow {
		return Listing[Video]{}, invalidArgument(fieldEmbedMarkdown)
	}

	return itemFetchers[Listing[Video]]{
		movie: func(ctx context.Context, c *trakt.UserClient, r MovieRef) (Listing[Video], error) {
			return fetchMovieVideos(ctx, c, MovieVideosArgs{MovieRef: r, EmbedMarkdown: in.EmbedMarkdown})
		},
		show: func(ctx context.Context, c *trakt.UserClient, r ShowRef) (Listing[Video], error) {
			return fetchShowVideos(ctx, c, VideosArgs{ShowRef: r, EmbedMarkdown: in.EmbedMarkdown})
		},
		season:  fetchSeasonVideos,
		episode: fetchEpisodeVideos,
	}.fetch(ctx, c, in.ItemRef)
}

func listItemLists(ctx context.Context, c *trakt.UserClient, in ListItemListsArgs) (Listing[TraktList], error) {
	if err := in.check(typeMovie, typeShow, typeSeason, typeEpisode); err != nil {
		return Listing[TraktList]{}, err
	}

	switch in.Type {
	case typeMovie:
		return fetchMovieLists(ctx, c, MovieListsArgs{PageArgs: in.PageArgs, MovieRef: in.movieRef(), ListType: in.ListType, Sort: in.Sort})
	case typeShow:
		return fetchShowLists(ctx, c, ShowListsArgs{PageArgs: in.PageArgs, ShowRef: in.showRef(), ListType: in.ListType, Sort: in.Sort})
	case typeSeason:
		return fetchSeasonLists(ctx, c, SeasonListsArgs{PageArgs: in.PageArgs, SeasonRef: in.seasonRef(), ListType: in.ListType, Sort: in.Sort})
	default:
		return fetchEpisodeLists(ctx, c, EpisodeListsArgs{PageArgs: in.PageArgs, EpisodeRef: in.episodeRef(), ListType: in.ListType, Sort: in.Sort})
	}
}
