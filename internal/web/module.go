package web

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"html/template"
	"net"
	"net/http"
	"strings"
	"time"

	"github.com/jpconstantineau/Glaucus/internal/profile"
	"github.com/jpconstantineau/Glaucus/internal/providers"
	"github.com/jpconstantineau/Glaucus/internal/runtime"
	"github.com/pocketbase/pocketbase/core"
	"github.com/pocketbase/pocketbase/tools/router"
)

const (
	sessionCookieName = "glaucus_session"
	csrfCookieName    = "glaucus_csrf"
)

type Options struct {
	AppName                 string
	Version                 string
	Commit                  string
	BuiltAt                 string
	BindAddress             string
	SessionTTL              time.Duration
	Profile                 profile.ActiveProfile
	ProviderCatalog         providers.Catalog
	EventService            *runtime.EventService
	DefaultOperatorEmail    string
	DefaultOperatorPassword string
}

type Module struct {
	options Options
}

func Register(app core.App, options Options) *Module {
	module := &Module{options: options}

	app.OnServe().BindFunc(func(se *core.ServeEvent) error {
		module.BindRoutes(app, se.Router)
		return se.Next()
	})

	return module
}

func (m *Module) BindRoutes(app core.App, rg *router.Router[*core.RequestEvent]) {
	rg.GET("/", func(e *core.RequestEvent) error {
		if err := m.requireLocalHost(e); err != nil {
			return err
		}
		return e.Redirect(http.StatusTemporaryRedirect, "/dashboard")
	})
	rg.GET("/health", m.publicHealth)
	rg.GET("/login", m.loginPage)
	rg.POST("/login", m.loginSubmit)
	rg.POST("/logout", m.withOperatorAuth(m.logoutSubmit))
	rg.GET("/dashboard", m.withOperatorAuth(m.dashboardShell))
	rg.GET("/dashboard/status", m.withOperatorAuth(m.dashboardStatusPage))
	rg.GET("/health/detailed", m.withOperatorAuth(m.detailedHealth))
	rg.GET("/api/version", m.withOperatorAuth(m.versionInfo))
	rg.GET("/api/dashboard/runs/{runID}/stream", m.withOperatorAuth(m.streamRunEvents))
	rg.GET("/api/dashboard/sessions/{sessionID}/stream", m.withOperatorAuth(m.streamSessionEvents))
	rg.GET("/api/dashboard/status/stream", m.withOperatorAuth(m.streamStatusEvents))

	_ = app
}

func EnsureDefaultOperator(app core.App, email, password string) error {
	const collectionName = "operators"

	existing, err := app.FindFirstRecordByFilter(collectionName, "id != ''")
	if err == nil && existing != nil {
		return nil
	}

	collection, err := app.FindCollectionByNameOrId(collectionName)
	if err != nil {
		return err
	}

	record := core.NewRecord(collection)
	record.SetEmail(email)
	record.SetPassword(password)
	record.SetVerified(true)
	record.Set("display_name", "Local Operator")
	record.Set("profile_slug", "default")

	return app.Save(record)
}

func (m *Module) publicHealth(e *core.RequestEvent) error {
	if err := m.requireLocalHost(e); err != nil {
		return err
	}

	return e.JSON(http.StatusOK, map[string]any{
		"status":  "ok",
		"service": m.options.AppName,
	})
}

func (m *Module) loginPage(e *core.RequestEvent) error {
	if err := m.requireLocalHost(e); err != nil {
		return err
	}

	csrfToken, err := randomToken()
	if err != nil {
		return e.InternalServerError("failed to create login form", err)
	}

	http.SetCookie(e.Response, &http.Cookie{
		Name:     csrfCookieName,
		Value:    csrfToken,
		Path:     "/",
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
	})

	data := struct {
		AppName              string
		CSRF                 string
		DefaultOperatorEmail string
	}{
		AppName:              m.options.AppName,
		CSRF:                 csrfToken,
		DefaultOperatorEmail: m.options.DefaultOperatorEmail,
	}

	return e.HTML(http.StatusOK, loginTemplate(data))
}

func (m *Module) loginSubmit(e *core.RequestEvent) error {
	if err := m.requireLocalHost(e); err != nil {
		return err
	}

	if err := validateCSRFCookie(e); err != nil {
		return e.ForbiddenError("invalid csrf token", err)
	}

	email := strings.TrimSpace(e.Request.FormValue("email"))
	password := e.Request.FormValue("password")
	if email == "" || password == "" {
		return e.BadRequestError("email and password are required", nil)
	}

	record, err := e.App.FindAuthRecordByEmail("operators", email)
	if err != nil || !record.ValidatePassword(password) {
		return e.UnauthorizedError("invalid credentials", err)
	}

	token, err := record.NewStaticAuthToken(m.options.SessionTTL)
	if err != nil {
		return e.InternalServerError("failed to create session", err)
	}

	http.SetCookie(e.Response, &http.Cookie{
		Name:     sessionCookieName,
		Value:    token,
		Path:     "/",
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
		MaxAge:   int(m.options.SessionTTL.Seconds()),
	})

	return e.Redirect(http.StatusSeeOther, "/dashboard")
}

func (m *Module) logoutSubmit(e *core.RequestEvent, _ *core.Record) error {
	if err := validateCSRFCookie(e); err != nil {
		return e.ForbiddenError("invalid csrf token", err)
	}

	http.SetCookie(e.Response, &http.Cookie{
		Name:     sessionCookieName,
		Value:    "",
		Path:     "/",
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
		MaxAge:   -1,
	})

	return e.Redirect(http.StatusSeeOther, "/login")
}

func (m *Module) dashboardShell(e *core.RequestEvent, operator *core.Record) error {
	csrfToken, err := ensureCSRFCookie(e)
	if err != nil {
		return e.InternalServerError("failed to create dashboard session", err)
	}

	data := struct {
		AppName       string
		OperatorEmail string
		ProfileSlug   string
		ProviderCount int
		CSRF          string
	}{
		AppName:       m.options.AppName,
		OperatorEmail: operator.Email(),
		ProfileSlug:   m.options.Profile.Slug,
		ProviderCount: len(m.options.ProviderCatalog.Entries),
		CSRF:          csrfToken,
	}

	return e.HTML(http.StatusOK, dashboardTemplate(data))
}

func (m *Module) dashboardStatusPage(e *core.RequestEvent, operator *core.Record) error {
	data := struct {
		OperatorEmail string
		BindAddress   string
		ProviderCount int
	}{
		OperatorEmail: operator.Email(),
		BindAddress:   m.options.BindAddress,
		ProviderCount: len(m.options.ProviderCatalog.Entries),
	}

	return e.HTML(http.StatusOK, statusTemplate(data))
}

func (m *Module) detailedHealth(e *core.RequestEvent, operator *core.Record) error {
	return e.JSON(http.StatusOK, map[string]any{
		"status":         "ok",
		"service":        m.options.AppName,
		"profile":        m.options.Profile.Slug,
		"bind_address":   m.options.BindAddress,
		"providers":      len(m.options.ProviderCatalog.Entries),
		"operator_email": operator.Email(),
		"version":        m.options.Version,
	})
}

func (m *Module) versionInfo(e *core.RequestEvent, _ *core.Record) error {
	return e.JSON(http.StatusOK, map[string]any{
		"name":     m.options.AppName,
		"version":  m.options.Version,
		"commit":   m.options.Commit,
		"built_at": m.options.BuiltAt,
	})
}

func (m *Module) streamRunEvents(e *core.RequestEvent, _ *core.Record) error {
	if m.options.EventService == nil {
		return e.JSON(http.StatusServiceUnavailable, map[string]any{"error": "event stream unavailable"})
	}

	runID := e.Request.PathValue("runID")
	after := parseAfterSequence(e.Request.URL.Query().Get("after"))
	events, err := m.options.EventService.ListRunEvents(e.Request.Context(), runID, after)
	if err != nil {
		return e.InternalServerError("failed to load run events", err)
	}

	return m.streamEvents(e, events, func() (<-chan runtime.RunEvent, func()) {
		return m.options.EventService.SubscribeRun(runID)
	})
}

func (m *Module) streamSessionEvents(e *core.RequestEvent, _ *core.Record) error {
	if m.options.EventService == nil {
		return e.JSON(http.StatusServiceUnavailable, map[string]any{"error": "event stream unavailable"})
	}

	sessionID := e.Request.PathValue("sessionID")
	after := parseAfterSequence(e.Request.URL.Query().Get("after"))
	events, err := m.options.EventService.ListSessionEvents(e.Request.Context(), sessionID, after)
	if err != nil {
		return e.InternalServerError("failed to load session events", err)
	}

	return m.streamEvents(e, events, func() (<-chan runtime.RunEvent, func()) {
		return m.options.EventService.SubscribeSession(sessionID)
	})
}

func (m *Module) streamStatusEvents(e *core.RequestEvent, _ *core.Record) error {
	if m.options.EventService == nil {
		return e.JSON(http.StatusServiceUnavailable, map[string]any{"error": "event stream unavailable"})
	}

	snapshot, err := m.options.EventService.StatusSnapshot(e.Request.Context())
	if err != nil {
		return e.InternalServerError("failed to load status snapshot", err)
	}

	return m.streamEvents(e, []runtime.RunEvent{snapshot}, func() (<-chan runtime.RunEvent, func()) {
		return m.options.EventService.SubscribeStatus()
	})
}

func (m *Module) streamEvents(
	e *core.RequestEvent,
	backlog []runtime.RunEvent,
	subscribe func() (<-chan runtime.RunEvent, func()),
) error {
	e.Response.Header().Set("Content-Type", "text/event-stream")
	e.Response.Header().Set("Cache-Control", "no-cache")
	e.Response.Header().Set("Connection", "keep-alive")

	flusher, ok := e.Response.(http.Flusher)
	if !ok {
		return e.InternalServerError("streaming unsupported", nil)
	}

	for _, event := range backlog {
		if err := writeSSEEvent(e.Response, event); err != nil {
			return err
		}
	}
	flusher.Flush()

	if e.Request.URL.Query().Get("once") == "1" {
		return nil
	}

	ch, unsubscribe := subscribe()
	defer unsubscribe()

	for {
		select {
		case <-e.Request.Context().Done():
			return nil
		case event := <-ch:
			if err := writeSSEEvent(e.Response, event); err != nil {
				return err
			}
			flusher.Flush()
		}
	}
}

func writeSSEEvent(w http.ResponseWriter, event runtime.RunEvent) error {
	payload, err := json.Marshal(event)
	if err != nil {
		return err
	}
	if _, err := fmt.Fprintf(w, "id: %s\nevent: %s\ndata: %s\n\n", event.ID, event.Type, payload); err != nil {
		return err
	}
	return nil
}

func parseAfterSequence(value string) int {
	var after int
	_, _ = fmt.Sscanf(strings.TrimSpace(value), "%d", &after)
	return after
}

func (m *Module) withOperatorAuth(next func(*core.RequestEvent, *core.Record) error) func(*core.RequestEvent) error {
	return func(e *core.RequestEvent) error {
		if err := m.requireLocalHost(e); err != nil {
			return err
		}

		cookie, err := e.Request.Cookie(sessionCookieName)
		if err != nil || cookie.Value == "" {
			return e.Redirect(http.StatusSeeOther, "/login")
		}

		record, err := e.App.FindAuthRecordByToken(cookie.Value, core.TokenTypeAuth)
		if err != nil {
			return e.Redirect(http.StatusSeeOther, "/login")
		}

		e.Auth = record
		return next(e, record)
	}
}

func (m *Module) requireLocalHost(e *core.RequestEvent) error {
	if isAllowedHost(m.options.BindAddress, e.Request.Host) {
		return nil
	}
	return e.ForbiddenError("invalid host", nil)
}

func isAllowedHost(bindAddress, requestHost string) bool {
	allowed := map[string]struct{}{
		"localhost": {},
		"127.0.0.1": {},
		"::1":       {},
		"[::1]":     {},
	}

	bindHost, bindPort, err := net.SplitHostPort(bindAddress)
	if err == nil {
		allowed[bindAddress] = struct{}{}
		allowed[bindHost] = struct{}{}
		if bindHost == "" {
			allowed["127.0.0.1:"+bindPort] = struct{}{}
			allowed["localhost:"+bindPort] = struct{}{}
		}
	}

	requestHost = strings.TrimSpace(requestHost)
	if requestHost == "" {
		return false
	}
	if _, ok := allowed[requestHost]; ok {
		return true
	}

	host, _, err := net.SplitHostPort(requestHost)
	if err == nil {
		_, ok := allowed[host]
		return ok
	}

	_, ok := allowed[requestHost]
	return ok
}

func validateCSRFCookie(e *core.RequestEvent) error {
	formToken := e.Request.FormValue("csrf")
	cookie, err := e.Request.Cookie(csrfCookieName)
	if err != nil {
		return err
	}
	if formToken == "" || formToken != cookie.Value {
		return fmt.Errorf("csrf token mismatch")
	}
	return nil
}

func ensureCSRFCookie(e *core.RequestEvent) (string, error) {
	if cookie, err := e.Request.Cookie(csrfCookieName); err == nil && cookie.Value != "" {
		return cookie.Value, nil
	}

	token, err := randomToken()
	if err != nil {
		return "", err
	}

	http.SetCookie(e.Response, &http.Cookie{
		Name:     csrfCookieName,
		Value:    token,
		Path:     "/",
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
	})

	return token, nil
}

func randomToken() (string, error) {
	buf := make([]byte, 16)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	return hex.EncodeToString(buf), nil
}

var loginPageTmpl = template.Must(template.New("login").Parse(`<!doctype html>
<html lang="en">
<head>
  <meta charset="utf-8">
  <meta name="viewport" content="width=device-width, initial-scale=1">
  <title>{{.AppName}} Login</title>
  <style>
    body { font-family: Georgia, serif; background: linear-gradient(135deg, #f3efe4, #dbe7f0); color: #1f2933; margin: 0; }
    main { max-width: 28rem; margin: 4rem auto; background: rgba(255,255,255,.92); padding: 2rem; border-radius: 1rem; box-shadow: 0 20px 60px rgba(15, 23, 42, .15); }
    label, input { display: block; width: 100%; }
    input { margin: .4rem 0 1rem; padding: .8rem; border: 1px solid #c7d2da; border-radius: .6rem; box-sizing: border-box; }
    button { background: #1f5c4a; color: #fff; border: 0; border-radius: .6rem; padding: .8rem 1rem; width: 100%; }
    .hint { color: #52606d; font-size: .95rem; }
  </style>
</head>
<body>
  <main>
    <h1>{{.AppName}}</h1>
    <p class="hint">Sign in with the local operator account. Default bootstrap email: <strong>{{.DefaultOperatorEmail}}</strong></p>
    <form method="post" action="/login">
      <input type="hidden" name="csrf" value="{{.CSRF}}">
      <label for="email">Email</label>
      <input id="email" name="email" type="email" required>
      <label for="password">Password</label>
      <input id="password" name="password" type="password" required>
      <button type="submit">Open Dashboard</button>
    </form>
  </main>
</body>
</html>`))

var dashboardPageTmpl = template.Must(template.New("dashboard").Parse(`<!doctype html>
<html lang="en">
<head>
  <meta charset="utf-8">
  <meta name="viewport" content="width=device-width, initial-scale=1">
  <title>{{.AppName}} Dashboard</title>
  <style>
    :root { --ink: #102a43; --muted: #627d98; --card: rgba(255,255,255,.88); --accent: #0f766e; --bg-a: #f6efe6; --bg-b: #d9e7ef; }
    body { margin: 0; font-family: "Trebuchet MS", sans-serif; color: var(--ink); background: radial-gradient(circle at top left, var(--bg-a), var(--bg-b)); }
    header, main { max-width: 64rem; margin: 0 auto; padding: 1.5rem; }
    header { display: flex; justify-content: space-between; align-items: center; }
    .brand { font-size: 1.8rem; font-weight: 700; letter-spacing: .04em; }
    .card-grid { display: grid; grid-template-columns: repeat(auto-fit, minmax(14rem, 1fr)); gap: 1rem; }
    .card, .panel { background: var(--card); border-radius: 1rem; padding: 1.25rem; box-shadow: 0 18px 40px rgba(16, 42, 67, .12); }
    .label { color: var(--muted); text-transform: uppercase; font-size: .78rem; letter-spacing: .08em; }
    .value { font-size: 1.7rem; margin-top: .4rem; }
    .panel { margin-top: 1rem; }
    button { background: var(--accent); color: #fff; border: 0; border-radius: .6rem; padding: .7rem 1rem; }
    a { color: var(--accent); }
  </style>
</head>
<body>
  <header>
    <div>
      <div class="brand">{{.AppName}}</div>
      <div>Signed in as {{.OperatorEmail}}</div>
    </div>
    <form method="post" action="/logout">
      <input type="hidden" name="csrf" value="{{.CSRF}}">
      <button type="submit">Log Out</button>
    </form>
  </header>
  <main>
    <section class="card-grid">
      <article class="card">
        <div class="label">Profile</div>
        <div class="value">{{.ProfileSlug}}</div>
      </article>
      <article class="card">
        <div class="label">Providers</div>
        <div class="value">{{.ProviderCount}}</div>
      </article>
      <article class="card">
        <div class="label">Health</div>
        <div class="value">OK</div>
      </article>
    </section>
    <section class="panel">
      <h2>Status</h2>
      <p>Open the operator status page at <a href="/dashboard/status">/dashboard/status</a>.</p>
      <p>Machine-readable endpoints: <a href="/health">/health</a>, <a href="/health/detailed">/health/detailed</a>, <a href="/api/version">/api/version</a>.</p>
    </section>
  </main>
</body>
</html>`))

var statusPageTmpl = template.Must(template.New("status").Parse(`<!doctype html>
<html lang="en">
<head>
  <meta charset="utf-8">
  <meta name="viewport" content="width=device-width, initial-scale=1">
  <title>Glaucus Status</title>
  <style>
    body { font-family: "Trebuchet MS", sans-serif; background: #f4f7f9; color: #102a43; margin: 0; padding: 2rem; }
    .panel { max-width: 42rem; margin: 0 auto; background: #fff; border-radius: 1rem; padding: 1.5rem; box-shadow: 0 12px 30px rgba(16, 42, 67, .10); }
  </style>
</head>
<body>
  <section class="panel">
    <h1>Status</h1>
    <p>Operator: {{.OperatorEmail}}</p>
    <p>Bind address: {{.BindAddress}}</p>
    <p>Loaded provider entries: {{.ProviderCount}}</p>
  </section>
</body>
</html>`))

func loginTemplate(data any) string {
	var sb strings.Builder
	_ = loginPageTmpl.Execute(&sb, data)
	return sb.String()
}

func dashboardTemplate(data any) string {
	var sb strings.Builder
	_ = dashboardPageTmpl.Execute(&sb, data)
	return sb.String()
}

func statusTemplate(data any) string {
	var sb strings.Builder
	_ = statusPageTmpl.Execute(&sb, data)
	return sb.String()
}
