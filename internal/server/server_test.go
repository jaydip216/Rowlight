package server

import (
	"io/fs"
	"net/http"
	"net/http/httptest"
	"testing"
	"testing/fstest"
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
	web := fstest.MapFS{"index.html": &fstest.MapFile{Data: []byte("<!doctype html><title>db0</title>")}}
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
	if got := res.Body.String(); got != "<!doctype html><title>db0</title>" {
		t.Fatalf("body = %q", got)
	}
}
