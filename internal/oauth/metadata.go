package oauth

import (
	"encoding/json"
	"net/http"

	"github.com/modelcontextprotocol/go-sdk/auth"
	"github.com/modelcontextprotocol/go-sdk/oauthex"
)

const (
	// bearerMethodHeader is the only way a client presents our access token (RFC 6750 §2.1).
	bearerMethodHeader = "header"
	// corsAnyOrigin lets browser-based MCP clients read public metadata.
	corsAnyOrigin = "*"
)

// asMetadata is the RFC 8414 document, with the fields of spec §6.1.
type asMetadata struct {
	Issuer                                     string   `json:"issuer"`
	AuthorizationEndpoint                      string   `json:"authorization_endpoint"`
	TokenEndpoint                              string   `json:"token_endpoint"`
	RevocationEndpoint                         string   `json:"revocation_endpoint"`
	RegistrationEndpoint                       string   `json:"registration_endpoint,omitempty"`
	ResponseTypesSupported                     []string `json:"response_types_supported"`
	GrantTypesSupported                        []string `json:"grant_types_supported"`
	CodeChallengeMethodsSupported              []string `json:"code_challenge_methods_supported"`
	TokenEndpointAuthMethodsSupported          []string `json:"token_endpoint_auth_methods_supported"`
	RevocationEndpointAuthMethodsSupported     []string `json:"revocation_endpoint_auth_methods_supported"`
	ClientIDMetadataDocumentSupported          bool     `json:"client_id_metadata_document_supported"`
	AuthorizationResponseIssParameterSupported bool     `json:"authorization_response_iss_parameter_supported"`
	ScopesSupported                            []string `json:"scopes_supported"`
}

// pkceMethodS256 is the only PKCE method accepted.
const pkceMethodS256 = "S256"

func (s *Server) asMetadata() asMetadata {
	m := asMetadata{
		Issuer:                            s.issuer,
		AuthorizationEndpoint:             s.issuer + pathAuthorize,
		TokenEndpoint:                     s.issuer + pathToken,
		RevocationEndpoint:                s.issuer + pathRevoke,
		ResponseTypesSupported:            supportedResponseTypes,
		GrantTypesSupported:               supportedGrantTypes,
		CodeChallengeMethodsSupported:     []string{pkceMethodS256},
		TokenEndpointAuthMethodsSupported: []string{authMethodNone},
		ScopesSupported:                   []string{MCPScope},
		// RFC 8414 defaults this to client_secret_basic, which public clients lack.
		RevocationEndpointAuthMethodsSupported:     []string{authMethodNone},
		ClientIDMetadataDocumentSupported:          true,
		AuthorizationResponseIssParameterSupported: true,
	}
	if s.enableDCR {
		m.RegistrationEndpoint = s.issuer + pathRegister
	}

	return m
}

func (s *Server) serveASMetadata(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", mediaTypeJSON)
	w.Header().Set("Cache-Control", cacheControlNoStore)
	w.Header().Set("Access-Control-Allow-Origin", corsAnyOrigin)

	if err := json.NewEncoder(w).Encode(s.asMetadata()); err != nil {
		s.log.DebugContext(r.Context(), "oauth: write metadata", "error", err)
	}
}

// resourceMetadataHandler serves RFC 9728 metadata for <base-url>/mcp.
func (s *Server) resourceMetadataHandler() http.Handler {
	h := auth.ProtectedResourceMetadataHandler(&oauthex.ProtectedResourceMetadata{
		Resource:               s.resource,
		AuthorizationServers:   []string{s.issuer},
		ScopesSupported:        []string{MCPScope},
		BearerMethodsSupported: []string{bearerMethodHeader},
	})

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", cacheControlNoStore)
		h.ServeHTTP(w, r)
	})
}
