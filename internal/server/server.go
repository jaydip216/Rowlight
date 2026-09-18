package server

import (
	"context"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"io/fs"
	"log"
	"net"
	"net/http"
	"strings"
	"time"

	"github.com/jaydip216/db0/internal/store"
)

type Server struct {
	token   string
	store   *store.Store
	handler http.Handler
}

func New(token string, web fs.FS) *Server {
	s := &Server{token: token, store: store.New()}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/health", s.health)
	mux.HandleFunc("POST /api/connections", s.createConnection)
	mux.HandleFunc("DELETE /api/connections/{id}", s.deleteConnection)
	mux.HandleFunc("GET /api/connections/{id}/schemas", s.schemas)
	mux.HandleFunc("GET /api/connections/{id}/schemas/{schema}/tables", s.tables)
	mux.HandleFunc("GET /api/connections/{id}/schemas/{schema}/tables/{table}/columns", s.columns)
	mux.HandleFunc("POST /api/connections/{id}/queries", s.query)
	mux.HandleFunc("DELETE /api/queries/{id}", s.cancelQuery)
	mux.Handle("/", spaHandler(web))
	s.handler = s.securityHeaders(s.hostCheck(s.auth(mux)))
	return s
}

func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) { s.handler.ServeHTTP(w, r) }

func (s *Server) Close() { s.store.CloseAll() }

func (s *Server) auth(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.HasPrefix(r.URL.Path, "/api/") {
			next.ServeHTTP(w, r)
			return
		}
		got := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
		if subtle.ConstantTimeCompare([]byte(got), []byte(s.token)) != 1 {
			writeError(w, http.StatusUnauthorized, "invalid local session token")
			return
		}
		next.ServeHTTP(w, r)
	})
}

func (s *Server) hostCheck(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		host, _, err := net.SplitHostPort(r.Host)
		if err != nil {
			host = r.Host
		}
		if host != "127.0.0.1" && host != "localhost" && host != "[::1]" && host != "::1" {
			writeError(w, http.StatusForbidden, "invalid host")
			return
		}
		if origin := r.Header.Get("Origin"); origin != "" {
			o, err := neturl(origin)
			if err != nil || !strings.EqualFold(o, r.Host) {
				writeError(w, http.StatusForbidden, "invalid origin")
				return
			}
		}
		next.ServeHTTP(w, r)
	})
}

func neturl(origin string) (string, error) {
	for _, prefix := range []string{"http://", "https://"} {
		if strings.HasPrefix(origin, prefix) {
			return strings.TrimSuffix(strings.TrimPrefix(origin, prefix), "/"), nil
		}
	}
	return "", errors.New("invalid origin")
}

func (s *Server) securityHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Security-Policy", "default-src 'self'; script-src 'self'; style-src 'self' 'unsafe-inline'; img-src 'self' data:; connect-src 'self'; object-src 'none'; base-uri 'none'; frame-ancestors 'none'")
		w.Header().Set("Referrer-Policy", "no-referrer")
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("X-Frame-Options", "DENY")
		next.ServeHTTP(w, r)
	})
}

func (s *Server) health(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

func (s *Server) createConnection(w http.ResponseWriter, r *http.Request) {
	var req store.ConnectionRequest
	if err := decodeJSON(w, r, &req); err != nil {
		return
	}
	ctx, cancel := contextWithTimeout(r, 10*time.Second)
	defer cancel()
	c, err := s.store.Connect(ctx, req)
	if err != nil {
		writeError(w, http.StatusBadGateway, err.Error())
		return
	}
	writeJSON(w, http.StatusCreated, c)
}

func (s *Server) deleteConnection(w http.ResponseWriter, r *http.Request) {
	if !s.store.Close(r.PathValue("id")) {
		writeError(w, http.StatusNotFound, "connection not found")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) schemas(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := contextWithTimeout(r, 15*time.Second)
	defer cancel()
	items, err := s.store.Schemas(ctx, r.PathValue("id"))
	if err != nil {
		writeStoreError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"schemas": items})
}

func (s *Server) tables(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := contextWithTimeout(r, 15*time.Second)
	defer cancel()
	items, err := s.store.Tables(ctx, r.PathValue("id"), r.PathValue("schema"))
	if err != nil {
		writeStoreError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"tables": items})
}

func (s *Server) columns(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := contextWithTimeout(r, 15*time.Second)
	defer cancel()
	items, err := s.store.Columns(ctx, r.PathValue("id"), r.PathValue("schema"), r.PathValue("table"))
	if err != nil {
		writeStoreError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"columns": items})
}

func (s *Server) query(w http.ResponseWriter, r *http.Request) {
	var req store.QueryRequest
	if err := decodeJSON(w, r, &req); err != nil {
		return
	}
	w.Header().Set("Content-Type", "application/x-ndjson")
	w.Header().Set("Cache-Control", "no-store")
	flusher, ok := w.(http.Flusher)
	if !ok {
		writeError(w, http.StatusInternalServerError, "streaming unavailable")
		return
	}
	enc := json.NewEncoder(w)
	write := func(v any) error {
		if err := enc.Encode(v); err != nil {
			return err
		}
		flusher.Flush()
		return nil
	}
	if err := s.store.StreamQuery(r.Context(), r.PathValue("id"), req, write); err != nil {
		_ = write(map[string]any{"type": "error", "error": err.Error()})
	}
}

func (s *Server) cancelQuery(w http.ResponseWriter, r *http.Request) {
	if !s.store.Cancel(r.PathValue("id")) {
		writeError(w, http.StatusNotFound, "active query not found")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func spaHandler(web fs.FS) http.Handler {
	files := http.FileServer(http.FS(web))
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet && r.Method != http.MethodHead {
			http.NotFound(w, r)
			return
		}
		if r.URL.Path == "/" {
			index, err := fs.ReadFile(web, "index.html")
			if err != nil {
				http.Error(w, "frontend unavailable", http.StatusInternalServerError)
				return
			}
			w.Header().Set("Content-Type", "text/html; charset=utf-8")
			w.Header().Set("Cache-Control", "no-store")
			_, _ = w.Write(index)
			return
		}
		files.ServeHTTP(w, r)
	})
}

func decodeJSON(w http.ResponseWriter, r *http.Request, dst any) error {
	r.Body = http.MaxBytesReader(w, r.Body, 1<<20)
	dec := json.NewDecoder(r.Body)
	dec.DisallowUnknownFields()
	if err := dec.Decode(dst); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request: "+err.Error())
		return err
	}
	return nil
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	if err := json.NewEncoder(w).Encode(v); err != nil {
		log.Printf("write response: %v", err)
	}
}

func writeError(w http.ResponseWriter, status int, message string) {
	writeJSON(w, status, map[string]string{"error": message})
}

func writeStoreError(w http.ResponseWriter, err error) {
	if errors.Is(err, store.ErrNotFound) {
		writeError(w, http.StatusNotFound, err.Error())
		return
	}
	writeError(w, http.StatusBadGateway, err.Error())
}

func contextWithTimeout(r *http.Request, d time.Duration) (context.Context, context.CancelFunc) {
	return context.WithTimeout(r.Context(), d)
}
