package server

import (
	"bytes"
	"encoding/json"
	"io/fs"
	"net/http"
	"net/http/httptest"
	"testing"
	"testing/fstest"

	appstate "github.com/jaydip216/Rowlight/internal/state"
)

func TestLocalAPIAuthorization(t *testing.T) {
	t.Parallel()
	web := fstest.MapFS{"index.html": &fstest.MapFile{Data: []byte("ok")}}
	app := New("secret", fs.FS(web))

	tests := []struct {
		name   string
		host   string
		token  string
		origin string
		status int
	}{
		{name: "authorized", host: "127.0.0.1:7777", token: "secret", origin: "http://127.0.0.1:7777", status: http.StatusOK},
		{name: "missing token", host: "127.0.0.1:7777", status: http.StatusUnauthorized},
		{name: "foreign host", host: "evil.example", token: "secret", status: http.StatusForbidden},
		{name: "foreign origin", host: "127.0.0.1:7777", token: "secret", origin: "https://evil.example", status: http.StatusForbidden},
	}
	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, "http://"+tt.host+"/api/health", nil)
			req.Host = tt.host
			if tt.token != "" {
				req.Header.Set("Authorization", "Bearer "+tt.token)
			}
			if tt.origin != "" {
				req.Header.Set("Origin", tt.origin)
			}
			res := httptest.NewRecorder()
			app.ServeHTTP(res, req)
			if res.Code != tt.status {
				t.Fatalf("status = %d, want %d: %s", res.Code, tt.status, res.Body.String())
			}
		})
	}
}

func TestRootServesIndexWithoutRedirect(t *testing.T) {
	t.Parallel()
	web := fstest.MapFS{"index.html": &fstest.MapFile{Data: []byte("<!doctype html><title>Rowlight</title>")}}
	app := New("secret", fs.FS(web))

	req := httptest.NewRequest(http.MethodGet, "http://127.0.0.1:7777/", nil)
	req.Host = "127.0.0.1:7777"
	res := httptest.NewRecorder()
	app.ServeHTTP(res, req)

	if res.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", res.Code)
	}
	if location := res.Header().Get("Location"); location != "" {
		t.Fatalf("unexpected redirect to %q", location)
	}
	if got := res.Body.String(); got != "<!doctype html><title>Rowlight</title>" {
		t.Fatalf("body = %q", got)
	}
}

func TestProfilesAndHistoryAPI(t *testing.T) {
	t.Parallel()
	web := fstest.MapFS{"index.html": &fstest.MapFile{Data: []byte("ok")}}
	app := New("secret", fs.FS(web))

	profile := []byte(`{"name":"Local","host":"127.0.0.1","port":3306,"user":"reader","database":"","tls":{"mode":"disabled"}}`)
	create := apiRequest(app, http.MethodPost, "/api/profiles", profile)
	if create.Code != http.StatusOK {
		t.Fatalf("create profile status = %d: %s", create.Code, create.Body.String())
	}
	var saved appstate.Profile
	if err := json.Unmarshal(create.Body.Bytes(), &saved); err != nil || saved.ID == "" {
		t.Fatalf("saved profile = %+v, error = %v", saved, err)
	}

	list := apiRequest(app, http.MethodGet, "/api/profiles", nil)
	if list.Code != http.StatusOK || !bytes.Contains(list.Body.Bytes(), []byte(`"name":"Local"`)) {
		t.Fatalf("list profiles status = %d: %s", list.Code, list.Body.String())
	}
	if !bytes.Contains(list.Body.Bytes(), []byte(`"database":""`)) {
		t.Fatalf("list profiles omitted empty database: %s", list.Body.String())
	}

	if err := app.state.AddHistory(appstate.HistoryEntry{SQL: "SELECT 1"}); err != nil {
		t.Fatal(err)
	}
	history := apiRequest(app, http.MethodGet, "/api/history", nil)
	if history.Code != http.StatusOK || !bytes.Contains(history.Body.Bytes(), []byte("SELECT 1")) {
		t.Fatalf("history status = %d: %s", history.Code, history.Body.String())
	}
	cleared := apiRequest(app, http.MethodDelete, "/api/history", nil)
	if cleared.Code != http.StatusNoContent || len(app.state.History()) != 0 {
		t.Fatalf("clear history status = %d", cleared.Code)
	}
}

func TestProfileEngineValidationAndLegacyDefault(t *testing.T) {
	t.Parallel()
	web := fstest.MapFS{"index.html": &fstest.MapFile{Data: []byte("ok")}}
	app := New("secret", fs.FS(web))

	legacy := apiRequest(app, http.MethodPost, "/api/profiles", []byte(`{"name":"Legacy","host":"127.0.0.1","port":3306,"user":"reader","database":"","tls":{"mode":"disabled"}}`))
	if legacy.Code != http.StatusOK || !bytes.Contains(legacy.Body.Bytes(), []byte(`"engine":"mysql"`)) {
		t.Fatalf("legacy profile status = %d: %s", legacy.Code, legacy.Body.String())
	}
	invalid := apiRequest(app, http.MethodPost, "/api/profiles", []byte(`{"name":"Bad","engine":"oracle","host":"127.0.0.1","port":1521,"user":"reader","database":"","tls":{"mode":"disabled"}}`))
	if invalid.Code != http.StatusBadRequest {
		t.Fatalf("invalid engine status = %d: %s", invalid.Code, invalid.Body.String())
	}
}

func TestBrowseAPIValidationAndConnectionLookup(t *testing.T) {
	t.Parallel()
	web := fstest.MapFS{"index.html": &fstest.MapFile{Data: []byte("ok")}}
	app := New("secret", fs.FS(web))

	invalid := apiRequest(app, http.MethodPost, "/api/connections/missing/browse", []byte(`{"schema":"","table":"customers","pageSize":20}`))
	if invalid.Code != http.StatusBadRequest {
		t.Fatalf("invalid browse status = %d: %s", invalid.Code, invalid.Body.String())
	}

	missing := apiRequest(app, http.MethodPost, "/api/connections/missing/browse", []byte(`{"schema":"app","table":"customers","pageSize":20}`))
	if missing.Code != http.StatusNotFound {
		t.Fatalf("missing connection status = %d: %s", missing.Code, missing.Body.String())
	}
}

func apiRequest(app http.Handler, method, path string, body []byte) *httptest.ResponseRecorder {
	req := httptest.NewRequest(method, "http://127.0.0.1:7777"+path, bytes.NewReader(body))
	req.Host = "127.0.0.1:7777"
	req.Header.Set("Authorization", "Bearer secret")
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	res := httptest.NewRecorder()
	app.ServeHTTP(res, req)
	return res
}
