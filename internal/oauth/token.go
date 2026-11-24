package oauth

import (
	"context"
	"errors"
	"log/slog"
	"mime"
	"net/http"

	"github.com/ory/fosite"
)

const (
	// mediaTypeForm is the only body type the token and revocation endpoints accept (RFC 6749 §4.1.3).
	mediaTypeForm = "application/x-www-form-urlencoded"
	// grantRefreshToken is the grant type whose reuse is logged.
	grantRefreshToken = "refresh_token"
	// grantAuthorizationCode is the other grant type served; both are the
	// kinds of the oauth_grants metric.
	grantAuthorizationCode = "authorization_code"
	// paramGrantType names the grant in a token request.
	paramGrantType = "grant_type"
)

// errNotForm rejects token and revocation bodies that are not form-encoded.
var errNotForm = fosite.ErrInvalidRequest.WithHint("The request body must be application/x-www-form-urlencoded.")

// token serves POST /oauth/token: fosite handles the code and refresh grants.
func (s *Server) token(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	// fosite sets no-store on success only; error bodies get it too.
	w.Header().Set("Cache-Control", cacheControlNoStore)

	fallback := fosite.NewAccessRequest(new(fosite.DefaultSession))
	if !isForm(r) {
		s.provider.WriteAccessError(ctx, w, fallback, errNotForm)
		return
	}

	r.Body = http.MaxBytesReader(w, r.Body, maxFormBody)

	ar, err := s.provider.NewAccessRequest(ctx, r, new(fosite.DefaultSession))
	if err != nil {
		s.logTokenError(ctx, r, err)

		var req fosite.AccessRequester = fallback
		if ar != nil {
			req = ar
		}

		s.provider.WriteAccessError(ctx, w, req, err)

		return
	}

	resp, err := s.provider.NewAccessResponse(ctx, ar)
	if err != nil {
		s.logTokenError(ctx, r, err)
		s.provider.WriteAccessError(ctx, w, ar, err)

		return
	}

	grant := r.PostForm.Get(paramGrantType)
	s.log.InfoContext(ctx, "oauth: token issued",
		slog.String("grant_type", grant), slog.String("client_id", ar.GetClient().GetID()))
	// fosite only issues these two grants; the check keeps the metric's
	// label set bounded even so.
	if grant == grantAuthorizationCode || grant == grantRefreshToken {
		s.metrics.Grant(grant)
	}

	s.provider.WriteAccessResponse(ctx, w, ar, resp)
}

// logTokenError logs a rejected token request without the request body,
// which carries codes, verifiers and refresh tokens.
func (s *Server) logTokenError(ctx context.Context, r *http.Request, err error) {
	rfc := fosite.ErrorToRFC6749Error(err)

	grant := r.PostForm.Get(paramGrantType)
	if grant == grantRefreshToken && errors.Is(err, fosite.ErrInactiveToken) {
		s.log.InfoContext(ctx, "oauth: refresh token reuse detected, token family revoked",
			slog.String("client_id", r.PostForm.Get(paramClientID)))

		return
	}

	s.log.InfoContext(ctx, "oauth: token request rejected",
		slog.String("grant_type", grant), slog.String("error", rfc.ErrorField), slog.String("hint", rfc.HintField))
}

// revoke serves POST /oauth/revoke (RFC 7009). It revokes only our tokens:
// the user's Trakt grant stays, so their other clients keep working (spec §7).
func (s *Server) revoke(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	if !isForm(r) {
		s.provider.WriteRevocationResponse(ctx, w, errNotForm)
		return
	}

	r.Body = http.MaxBytesReader(w, r.Body, maxFormBody)

	err := s.provider.NewRevocationRequest(ctx, r)
	if err != nil {
		rfc := fosite.ErrorToRFC6749Error(err)
		s.log.InfoContext(ctx, "oauth: revocation request rejected",
			slog.String("error", rfc.ErrorField), slog.String("hint", rfc.HintField))
	}

	s.provider.WriteRevocationResponse(ctx, w, err)
}

func isForm(r *http.Request) bool {
	mt, _, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
	return err == nil && mt == mediaTypeForm
}
