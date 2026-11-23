package tools

import (
	"context"
	"os"
	"strings"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/samber/trakt-mcp/internal/limits"
)

// siteFaviconPath is the website icon the MCP icon must match.
const siteFaviconPath = "../../site/public/favicon.svg"

func TestIconMatchesSiteFavicon(t *testing.T) {
	t.Parallel()

	b, err := os.ReadFile(siteFaviconPath)
	if err != nil {
		t.Fatalf("read favicon: %v", err)
	}

	if got := strings.TrimSpace(string(b)); got != iconSVG {
		t.Fatalf("iconSVG differs from %s", siteFaviconPath)
	}
}

func TestServerAdvertisesIcon(t *testing.T) {
	t.Parallel()

	srv := NewServer(Deps{People: &fakeFactory{people: newFakePeople()}, Store: &fakeToucher{}, Limiter: limits.New(10, 10), Version: "test"})
	st, ct := mcp.NewInMemoryTransports()
	ctx := context.Background()

	ss, err := srv.Connect(ctx, st, nil)
	if err != nil {
		t.Fatalf("server connect: %v", err)
	}

	t.Cleanup(func() {
		if err := ss.Close(); err != nil {
			t.Logf("close: %v", err)
		}
	})

	cs, err := mcp.NewClient(&mcp.Implementation{Name: "c", Version: "1"}, nil).Connect(ctx, ct, nil)
	if err != nil {
		t.Fatalf("client connect: %v", err)
	}

	t.Cleanup(func() {
		if err := cs.Close(); err != nil {
			t.Logf("close: %v", err)
		}
	})

	info := cs.InitializeResult().ServerInfo
	if len(info.Icons) != 1 || !strings.HasPrefix(info.Icons[0].Source, "data:image/svg+xml;base64,") {
		t.Fatalf("server icons = %+v", info.Icons)
	}
}
