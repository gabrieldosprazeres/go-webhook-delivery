package console

import (
	"encoding/json"
	"net/http"
	"strings"

	"github.com/gabrieldosprazeres/go-webhook-delivery/internal/delivery"
	"github.com/gabrieldosprazeres/go-webhook-delivery/internal/endpoint"
	"github.com/gabrieldosprazeres/go-webhook-delivery/internal/event"
	"github.com/gabrieldosprazeres/go-webhook-delivery/internal/insights"
	"github.com/google/uuid"
)

type pageData struct {
	View, Title, Error, Message, CSRF, NextCursor string
	Endpoint                                      *endpoint.Endpoint
	Created                                       *endpoint.Created
	Published                                     *event.PublishResult
	EndpointItems                                 []endpoint.ListItem
	Deliveries                                    []delivery.Summary
	Delivery                                      *delivery.Details
	Metrics                                       *insights.Snapshot
}

func (s *Server) home(w http.ResponseWriter, r *http.Request) {
	if _, err := r.Cookie(s.cookieName()); err == nil {
		http.Redirect(w, r, "/app", http.StatusSeeOther)
		return
	}
	http.Redirect(w, r, "/login", http.StatusSeeOther)
}

func (s *Server) loginPage(w http.ResponseWriter, r *http.Request) {
	token, err := newLoginCSRF()
	if err != nil {
		http.Error(w, "Serviço temporariamente indisponível.", http.StatusServiceUnavailable)
		return
	}
	http.SetCookie(w, &http.Cookie{Name: s.loginCSRFName(), Value: token, Path: "/", Secure: s.deps.SecureCookies,
		HttpOnly: true, SameSite: http.SameSiteStrictMode, MaxAge: 300})
	s.render(w, "login", pageData{View: "login", Title: "Conectar workspace", CSRF: token})
}

func (s *Server) login(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, 8<<10)
	if err := r.ParseForm(); err != nil || !s.validLoginCSRF(r) {
		http.Error(w, "Solicitação inválida.", http.StatusBadRequest)
		return
	}
	session, err := s.deps.Sessions.Login(r.Context(), r.FormValue("api_key"))
	if err != nil {
		s.render(w, "login", pageData{View: "login", Title: "Conectar workspace", Error: "Não foi possível autenticar essa credencial.", CSRF: r.FormValue("csrf_token")})
		return
	}
	s.setSessionCookie(w, session.Token)
	http.Redirect(w, r, "/app", http.StatusSeeOther)
}

func (s *Server) logout(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, 4<<10)
	_ = r.ParseForm()
	session := currentSession(r.Context())
	if !s.validMutation(r, session) {
		http.Error(w, "Solicitação inválida.", http.StatusBadRequest)
		return
	}
	if !s.consumeQuota(w, r, session, uuid.Nil, s.deps.EndpointPolicy) {
		return
	}
	_ = s.deps.Sessions.Logout(r.Context(), session)
	s.clearSessionCookie(w)
	w.Header().Set("Clear-Site-Data", `"cache", "cookies", "storage"`)
	http.Redirect(w, r, "/login", http.StatusSeeOther)
}

func (s *Server) dashboard(w http.ResponseWriter, r *http.Request) {
	session := currentSession(r.Context())
	snapshot, err := s.deps.Insights.Snapshot(r.Context(), session.Principal.WorkspaceID)
	data := pageData{View: "dashboard", Title: "Visão geral", CSRF: s.deps.Sessions.CSRF(session.Token)}
	if err != nil {
		data.Error = "Não foi possível carregar as métricas agora."
	} else {
		data.Metrics = &snapshot
	}
	s.render(w, "app", data)
}

func (s *Server) metrics(w http.ResponseWriter, r *http.Request) {
	session := currentSession(r.Context())
	snapshot, err := s.deps.Insights.Snapshot(r.Context(), session.Principal.WorkspaceID)
	if err != nil {
		http.Error(w, "metrics unavailable", http.StatusServiceUnavailable)
		return
	}
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	_ = json.NewEncoder(w).Encode(snapshot)
}

func (s *Server) endpointList(w http.ResponseWriter, r *http.Request) {
	session := currentSession(r.Context())
	result, err := s.deps.EndpointLister.List(r.Context(), session.Principal.WorkspaceID, 25, r.URL.Query().Get("cursor"))
	data := pageData{View: "endpoints", Title: "Endpoints", CSRF: s.deps.Sessions.CSRF(session.Token)}
	if err != nil {
		data.Error = "Não foi possível carregar os endpoints."
	} else {
		data.EndpointItems, data.NextCursor = result.Items, result.NextCursor
	}
	s.render(w, "app", data)
}

func (s *Server) endpointNew(w http.ResponseWriter, r *http.Request) {
	session := currentSession(r.Context())
	s.render(w, "app", pageData{View: "endpoint-new", Title: "Novo endpoint", CSRF: s.deps.Sessions.CSRF(session.Token)})
}

func (s *Server) endpointCreate(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, 16<<10)
	_ = r.ParseForm()
	session := currentSession(r.Context())
	data := pageData{View: "endpoint-new", Title: "Novo endpoint", CSRF: s.deps.Sessions.CSRF(session.Token)}
	if !s.validMutation(r, session) {
		http.Error(w, "Solicitação inválida.", http.StatusBadRequest)
		return
	}
	if !s.consumeQuota(w, r, session, uuid.Nil, s.deps.EndpointPolicy) {
		return
	}
	eventTypes := splitValues(r.FormValue("event_types"))
	created, err := s.deps.Endpoints.CreateAs(r.Context(), session.Principal.WorkspaceID, "api_key",
		session.Principal.APIKeyID.String(), uuid.NewString(), endpoint.CreateInput{URL: r.FormValue("url"), EventTypes: eventTypes})
	if err != nil {
		data.Error = "O endpoint foi recusado. Confirme HTTPS, DNS público e tipos de evento."
	} else {
		data.View, data.Title, data.Created = "endpoint-created", "Endpoint criado", &created
	}
	s.render(w, "app", data)
}

func (s *Server) endpointDetail(w http.ResponseWriter, r *http.Request) {
	session := currentSession(r.Context())
	id, err := uuid.Parse(r.PathValue("id"))
	if err != nil {
		http.NotFound(w, r)
		return
	}
	value, err := s.deps.Endpoints.Get(r.Context(), session.Principal.WorkspaceID, id)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	s.render(w, "app", pageData{View: "endpoint-detail", Title: "Detalhe do endpoint",
		CSRF: s.deps.Sessions.CSRF(session.Token), Endpoint: &value})
}

func (s *Server) eventNew(w http.ResponseWriter, r *http.Request) {
	session := currentSession(r.Context())
	s.render(w, "app", pageData{View: "event-new", Title: "Publicar evento", CSRF: s.deps.Sessions.CSRF(session.Token)})
}

func (s *Server) eventPublish(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, event.MaxPayloadBytes+(16<<10))
	_ = r.ParseForm()
	session := currentSession(r.Context())
	data := pageData{View: "event-new", Title: "Publicar evento", CSRF: s.deps.Sessions.CSRF(session.Token)}
	if !s.validMutation(r, session) {
		http.Error(w, "Solicitação inválida.", http.StatusBadRequest)
		return
	}
	if !s.consumeQuota(w, r, session, uuid.Nil, s.deps.IngestPolicy) {
		return
	}
	result, err := s.deps.Events.Publish(r.Context(), session.Principal.WorkspaceID,
		r.FormValue("idempotency_key"), []byte(r.FormValue("payload")))
	if err != nil {
		data.Error = "Evento inválido ou chave de idempotência em conflito."
	} else {
		data.View, data.Title, data.Published = "event-published", "Evento aceito", &result
	}
	s.render(w, "app", data)
}

func (s *Server) deliveryList(w http.ResponseWriter, r *http.Request) {
	session := currentSession(r.Context())
	result, err := s.deps.Deliveries.List(r.Context(), session.Principal.WorkspaceID, 25, r.URL.Query().Get("cursor"))
	data := pageData{View: "deliveries", Title: "Entregas", CSRF: s.deps.Sessions.CSRF(session.Token)}
	if err != nil {
		data.Error = "Não foi possível carregar as entregas."
	} else {
		data.Deliveries, data.NextCursor = result.Items, result.NextCursor
	}
	s.render(w, "app", data)
}

func (s *Server) deliveryDetail(w http.ResponseWriter, r *http.Request) {
	session := currentSession(r.Context())
	id, err := uuid.Parse(r.PathValue("id"))
	if err != nil {
		http.NotFound(w, r)
		return
	}
	details, err := s.deps.Deliveries.Get(r.Context(), session.Principal.WorkspaceID, id)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	s.render(w, "app", pageData{View: "delivery-detail", Title: "Detalhe da entrega",
		CSRF: s.deps.Sessions.CSRF(session.Token), Delivery: &details})
}

func (s *Server) deliveryReplay(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, 8<<10)
	_ = r.ParseForm()
	session := currentSession(r.Context())
	if !s.validMutation(r, session) {
		http.Error(w, "Solicitação inválida.", http.StatusBadRequest)
		return
	}
	id, err := uuid.Parse(r.PathValue("id"))
	if err == nil && !s.consumeQuota(w, r, session, id, s.deps.ReplayPolicy) {
		return
	}
	if err == nil {
		_, err = s.deps.Replay.Request(r.Context(), session.Principal.WorkspaceID, id,
			session.Principal.APIKeyID, r.FormValue("idempotency_key"), r.FormValue("reason"), uuid.NewString())
	}
	if err != nil {
		http.Error(w, "Replay recusado para o estado atual.", http.StatusConflict)
		return
	}
	http.Redirect(w, r, "/app/deliveries/"+id.String(), http.StatusSeeOther)
}

func (s *Server) render(w http.ResponseWriter, name string, data pageData) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := s.templates.ExecuteTemplate(w, name, data); err != nil {
		http.Error(w, "Falha ao renderizar página.", http.StatusInternalServerError)
	}
}

func splitValues(raw string) []string {
	parts := strings.FieldsFunc(raw, func(r rune) bool { return r == ',' || r == '\n' })
	result := make([]string, 0, len(parts))
	for _, item := range parts {
		if value := strings.TrimSpace(item); value != "" {
			result = append(result, value)
		}
	}
	return result
}
