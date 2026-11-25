package tools

import (
	"context"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// initializeResult connects an in-memory client to a server built with the
// given tool groups and returns the server's initialize result.
func initializeResult(t *testing.T, groups []func(*Server)) *mcp.InitializeResult {
	t.Helper()

	srv := newServer(Deps{Version: "test"}, groups)
	st, ct := mcp.NewInMemoryTransports()

	ctx := context.Background()

	ss, err := srv.mcp.Connect(ctx, st, nil)
	if err != nil {
		t.Fatalf("server Connect: %v", err)
	}

	t.Cleanup(func() {
		if err := ss.Close(); err != nil {
			t.Logf("close server session: %v", err)
		}
	})

	client := mcp.NewClient(&mcp.Implementation{Name: "test-client", Version: "1"}, nil)

	cs, err := client.Connect(ctx, ct, nil)
	if err != nil {
		t.Fatalf("client Connect: %v", err)
	}

	t.Cleanup(func() {
		if err := cs.Close(); err != nil {
			t.Logf("close client session: %v", err)
		}
	})

	return cs.InitializeResult()
}

func TestServerAdvertisesToolsListChanged(t *testing.T) {
	t.Parallel()

	cases := map[string][]func(*Server){
		"catalog":    catalog,
		"zero tools": nil,
	}

	for name, groups := range cases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			caps := initializeResult(t, groups).Capabilities
			if caps == nil {
				t.Fatal("no capabilities advertised")
			}

			if caps.Tools == nil || !caps.Tools.ListChanged {
				t.Errorf("tools capability = %+v, want listChanged true", caps.Tools)
			}

			// A non-nil ServerOptions.Capabilities replaces the SDK default
			// that carries logging; guard against dropping it.
			if caps.Logging == nil { //nolint:staticcheck // SA1019: asserting the deprecated capability is still advertised
				t.Error("logging capability missing")
			}
		})
	}
}
