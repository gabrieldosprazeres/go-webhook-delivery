package console

import (
	"context"
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"net/http"
	"strconv"
	"strings"

	"github.com/gabrieldosprazeres/go-webhook-delivery/internal/consolesession"
	"github.com/gabrieldosprazeres/go-webhook-delivery/internal/ratelimit"
	"github.com/google/uuid"
)

const (
	productionCookie = "__Host-wde_session"
	localCookie      = "wde_session"
	loginCSRFCookie  = "__Host-wde_login_csrf"
	localLoginCSRF   = "wde_login_csrf"
)

type sessionContextKey struct{}

func (s *Server) cookieName() string {
	if s.deps.SecureCookies {
		return productionCookie
	}
	return localCookie
}

func (s *Server) loginCSRFName() string {
	if s.deps.SecureCookies {
		return loginCSRFCookie
	}
	return localLoginCSRF
}

func (s *Server) require(scope string, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		cookie, err := r.Cookie(s.cookieName())
		if err != nil {
			http.Redirect(w, r, "/login", http.StatusSeeOther)
			return
		}
		session, err := s.deps.Sessions.Authenticate(r.Context(), cookie.Value)
		if err != nil {
			s.clearSessionCookie(w)
			http.Redirect(w, r, "/login", http.StatusSeeOther)
			return
		}
		if r.Method == http.MethodGet && strings.HasPrefix(r.URL.Path, "/app") &&
			!s.consumeQuota(w, r, session, uuid.Nil, s.deps.QueryPolicy) {
			return
		}
		if scope != "" {
			if _, allowed := session.Principal.Scopes[scope]; !allowed {
				http.Error(w, "A credencial não possui o escopo necessário.", http.StatusForbidden)
				return
			}
		}
		next.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), sessionContextKey{}, session)))
	})
}

func (s *Server) consumeQuota(w http.ResponseWriter, r *http.Request, session consolesession.Session,
	resourceID uuid.UUID, policy ratelimit.Policy,
) bool {
	if s.deps.Limiter == nil {
		return true
	}
	err := s.deps.Limiter.Consume(r.Context(), session.Principal, resourceID, policy)
	if retryAfter, exceeded := ratelimit.RetryAfter(err); exceeded {
		w.Header().Set("Retry-After", strconv.Itoa(retryAfter))
		http.Error(w, "Muitas solicitações. Tente novamente em instantes.", http.StatusTooManyRequests)
		return false
	}
	if err != nil {
		http.Error(w, "Serviço de quota indisponível.", http.StatusServiceUnavailable)
		return false
	}
	return true
}

func currentSession(ctx context.Context) consolesession.Session {
	return ctx.Value(sessionContextKey{}).(consolesession.Session)
}

func (s *Server) validMutation(r *http.Request, session consolesession.Session) bool {
	if r.Header.Get("Origin") != s.deps.Origin || !strings.HasPrefix(r.Header.Get("Content-Type"), "application/x-www-form-urlencoded") {
		return false
	}
	if site := r.Header.Get("Sec-Fetch-Site"); site != "" && site != "same-origin" && site != "none" {
		return false
	}
	return s.deps.Sessions.ValidCSRF(session.Token, r.FormValue("csrf_token"))
}

func (s *Server) setSessionCookie(w http.ResponseWriter, token string) {
	http.SetCookie(w, &http.Cookie{Name: s.cookieName(), Value: token, Path: "/", Secure: s.deps.SecureCookies,
		HttpOnly: true, SameSite: http.SameSiteStrictMode, MaxAge: 3600})
}

func (s *Server) clearSessionCookie(w http.ResponseWriter) {
	http.SetCookie(w, &http.Cookie{Name: s.cookieName(), Value: "", Path: "/", Secure: s.deps.SecureCookies,
		HttpOnly: true, SameSite: http.SameSiteStrictMode, MaxAge: -1})
}

func newLoginCSRF() (string, error) {
	raw := make([]byte, 32)
	if _, err := rand.Read(raw); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(raw), nil
}

func (s *Server) validLoginCSRF(r *http.Request) bool {
	if r.Header.Get("Origin") != s.deps.Origin || !strings.HasPrefix(r.Header.Get("Content-Type"), "application/x-www-form-urlencoded") {
		return false
	}
	cookie, err := r.Cookie(s.loginCSRFName())
	if err != nil {
		return false
	}
	return subtle.ConstantTimeCompare([]byte(cookie.Value), []byte(r.FormValue("csrf_token"))) == 1
}

func (s *Server) securityHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Security-Policy", "default-src 'none'; script-src 'self'; style-src 'self'; img-src 'self'; connect-src 'self'; object-src 'none'; base-uri 'none'; frame-ancestors 'none'; form-action 'self'")
		w.Header().Set("Permissions-Policy", "camera=(), microphone=(), geolocation=(), payment=()")
		w.Header().Set("Referrer-Policy", "no-referrer")
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("X-Frame-Options", "DENY")
		if s.deps.SecureCookies {
			w.Header().Set("Strict-Transport-Security", "max-age=31536000; includeSubDomains")
		}
		if !strings.HasPrefix(r.URL.Path, "/static/") {
			w.Header().Set("Cache-Control", "no-store, private")
		}
		next.ServeHTTP(w, r)
	})
}
