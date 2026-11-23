package tools

import (
	"context"
	"encoding/xml"
	"os"
	"regexp"
	"strings"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/samber/trakt-mcp/internal/limits"
)

const (
	// siteFaviconPath is the website icon the MCP icon must match.
	siteFaviconPath = "../../site/public/favicon.svg"
	// iconMaxBytes keeps the data URI small: it is sent in every initialize response.
	iconMaxBytes = 2048
	// iconViewBox is the 32-unit grid the mark is drawn on, so it stays crisp at 16 and 32 px.
	iconViewBox = "0 0 32 32"
)

// iconPalette is the only set of fills the mark may use: navy, amber and white.
// It keeps the mark flat and distinct from Trakt's own red branding.
var iconPalette = map[string]bool{"#1d2b53": true, "#f6a823": true, "#ffffff": true}

// hexColor matches every hex color literal in the SVG source.
var hexColor = regexp.MustCompile(`#[0-9a-fA-F]{3,8}\b`)

// normalizeSpace collapses every whitespace run to one space, so formatting differences do not count.
func normalizeSpace(s string) string {
	return strings.Join(strings.Fields(s), " ")
}

func TestIconSVGIsValidXML(t *testing.T) {
	t.Parallel()

	var root struct {
		XMLName xml.Name
		ViewBox string `xml:"viewBox,attr"`
	}
	if err := xml.Unmarshal([]byte(iconSVG), &root); err != nil {
		t.Fatalf("iconSVG is not valid XML: %v", err)
	}

	if root.XMLName.Local != "svg" || root.ViewBox != iconViewBox {
		t.Fatalf("root = %s viewBox=%q, want svg viewBox=%q", root.XMLName.Local, root.ViewBox, iconViewBox)
	}
}

func TestIconSVGMatchesFavicon(t *testing.T) {
	t.Parallel()

	b, err := os.ReadFile(siteFaviconPath)
	if err != nil {
		t.Fatalf("read favicon: %v", err)
	}

	if normalizeSpace(string(b)) != normalizeSpace(iconSVG) {
		t.Fatalf("iconSVG differs from %s", siteFaviconPath)
	}
}

func TestIconSVGUnder2KB(t *testing.T) {
	t.Parallel()

	if n := len(iconSVG); n > iconMaxBytes {
		t.Fatalf("iconSVG is %d bytes, want at most %d", n, iconMaxBytes)
	}
}

func TestIconSVGUsesBrandPalette(t *testing.T) {
	t.Parallel()

	for _, c := range hexColor.FindAllString(iconSVG, -1) {
		if !iconPalette[strings.ToLower(c)] {
			t.Errorf("iconSVG uses %s, outside the brand palette", c)
		}
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
