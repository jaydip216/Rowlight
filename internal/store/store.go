package store

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"database/sql"
	"encoding/base64"
	"errors"
	"fmt"
	"net"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"
	"unicode"

	mysql "github.com/go-sql-driver/mysql"
)

var ErrNotFound = errors.New("connection not found")

type TLSRequest struct {
	Mode          string `json:"mode"`
	ServerName    string `json:"serverName"`
	CAPEM         string `json:"caPem"`
	ClientCertPEM string `json:"clientCertPem"`
	ClientKeyPEM  string `json:"clientKeyPem"`
}

type ConnectionRequest struct {
	Host     string     `json:"host"`
	Port     int        `json:"port"`
	User     string     `json:"user"`
	Password string     `json:"password"`
	Database string     `json:"database"`
	TLS      TLSRequest `json:"tls"`
}

type Connection struct {
	ID            string `json:"id"`
	ServerVersion string `json:"serverVersion"`
	Database      string `json:"database"`
	db            *sql.DB
	tlsName       string
}

type Table struct {
	Name string `json:"name"`
	Type string `json:"type"`
}

type Column struct {
	Name       string  `json:"name"`
	DataType   string  `json:"dataType"`
	ColumnType string  `json:"columnType"`
	Nullable   bool    `json:"nullable"`
	Key        string  `json:"key"`
	Default    *string `json:"default"`
	Extra      string  `json:"extra"`
}

type QueryRequest struct {
	SQL     string `json:"sql"`
	MaxRows int    `json:"maxRows"`
}

type Store struct {
	mu          sync.RWMutex
	connections map[string]*Connection
	queries     map[string]context.CancelFunc
	querySlots  chan struct{}
	sequence    atomic.Uint64
}

func New() *Store {
	return &Store{
		connections: make(map[string]*Connection),
		queries:     make(map[string]context.CancelFunc),
		querySlots:  make(chan struct{}, 2),
	}
}

func (s *Store) Connect(ctx context.Context, req ConnectionRequest) (*Connection, error) {
	if strings.TrimSpace(req.Host) == "" || strings.TrimSpace(req.User) == "" {
		return nil, errors.New("host and user are required")
	}
	if req.Port == 0 {
		req.Port = 3306
	}
	if req.Port < 1 || req.Port > 65535 {
		return nil, errors.New("port must be between 1 and 65535")
	}

	cfg := mysql.NewConfig()
	cfg.Net = "tcp"
	cfg.Addr = net.JoinHostPort(req.Host, strconv.Itoa(req.Port))
	cfg.User = req.User
	cfg.Passwd = req.Password
	cfg.DBName = req.Database
	cfg.MultiStatements = false
	cfg.AllowNativePasswords = true
	cfg.Timeout = 10 * time.Second
	cfg.ReadTimeout = 0
	cfg.WriteTimeout = 0
	cfg.Params = map[string]string{"charset": "utf8mb4"}

	if req.TLS.Mode == "" {
		req.TLS.Mode = "system"
	}
	var tlsName string
	if req.TLS.Mode != "disabled" {
		if req.TLS.Mode != "system" && req.TLS.Mode != "custom" && req.TLS.Mode != "mutual" {
			return nil, errors.New("TLS mode must be disabled, system, custom, or mutual")
		}
		if (req.TLS.Mode == "custom" || req.TLS.Mode == "mutual") && req.TLS.CAPEM == "" {
			return nil, errors.New("custom and mutual TLS require a CA certificate")
		}
		if req.TLS.Mode == "mutual" && (req.TLS.ClientCertPEM == "" || req.TLS.ClientKeyPEM == "") {
			return nil, errors.New("mutual TLS requires a client certificate and key")
		}
		name := "db0-" + s.nextID()
		tlsCfg, err := makeTLSConfig(req.Host, req.TLS)
		if err != nil {
			return nil, err
		}
		if err := mysql.RegisterTLSConfig(name, tlsCfg); err != nil {
			return nil, fmt.Errorf("register TLS configuration: %w", err)
		}
		cfg.TLSConfig = name
		tlsName = name
	}

	db, err := sql.Open("mysql", cfg.FormatDSN())
	if err != nil {
		if tlsName != "" {
			mysql.DeregisterTLSConfig(tlsName)
		}
		return nil, fmt.Errorf("configure connection: %w", err)
	}
	db.SetMaxOpenConns(3)
	db.SetMaxIdleConns(1)
	db.SetConnMaxIdleTime(5 * time.Minute)
	if err := db.PingContext(ctx); err != nil {
		db.Close()
		if tlsName != "" {
			mysql.DeregisterTLSConfig(tlsName)
		}
		return nil, fmt.Errorf("connect: %w", err)
	}

	var version, database sql.NullString
	if err := db.QueryRowContext(ctx, "SELECT VERSION(), DATABASE()").Scan(&version, &database); err != nil {
		db.Close()
		if tlsName != "" {
			mysql.DeregisterTLSConfig(tlsName)
		}
		return nil, fmt.Errorf("read server information: %w", err)
	}
	c := &Connection{ID: s.nextID(), ServerVersion: version.String, Database: database.String, db: db, tlsName: tlsName}
	s.mu.Lock()
	s.connections[c.ID] = c
	s.mu.Unlock()
	return c, nil
}

func makeTLSConfig(host string, req TLSRequest) (*tls.Config, error) {
	serverName := strings.TrimSpace(req.ServerName)
	if serverName == "" {
		serverName = host
	}
	cfg := &tls.Config{MinVersion: tls.VersionTLS12, ServerName: serverName}
	if req.CAPEM != "" {
		roots, err := x509.SystemCertPool()
		if err != nil || roots == nil {
			roots = x509.NewCertPool()
		}
		if !roots.AppendCertsFromPEM([]byte(req.CAPEM)) {
			return nil, errors.New("TLS CA does not contain a valid certificate")
		}
		cfg.RootCAs = roots
	}
	if req.ClientCertPEM != "" || req.ClientKeyPEM != "" {
		if req.ClientCertPEM == "" || req.ClientKeyPEM == "" {
			return nil, errors.New("both TLS client certificate and key are required")
		}
		cert, err := tls.X509KeyPair([]byte(req.ClientCertPEM), []byte(req.ClientKeyPEM))
		if err != nil {
			return nil, fmt.Errorf("load TLS client certificate: %w", err)
		}
		cfg.Certificates = []tls.Certificate{cert}
	}
	return cfg, nil
}

func (s *Store) Close(id string) bool {
	s.mu.Lock()
	c, ok := s.connections[id]
	if ok {
		delete(s.connections, id)
	}
	s.mu.Unlock()
	if ok {
		_ = c.db.Close()
		if c.tlsName != "" {
			mysql.DeregisterTLSConfig(c.tlsName)
		}
	}
	return ok
}

func (s *Store) CloseAll() {
	s.mu.Lock()
	items := s.connections
	s.connections = make(map[string]*Connection)
	for _, cancel := range s.queries {
		cancel()
	}
	s.queries = make(map[string]context.CancelFunc)
	s.mu.Unlock()
	for _, c := range items {
		_ = c.db.Close()
		if c.tlsName != "" {
			mysql.DeregisterTLSConfig(c.tlsName)
		}
	}
}

func (s *Store) get(id string) (*Connection, error) {
	s.mu.RLock()
	c := s.connections[id]
	s.mu.RUnlock()
	if c == nil {
		return nil, ErrNotFound
	}
	return c, nil
}

func (s *Store) Schemas(ctx context.Context, id string) ([]string, error) {
	c, err := s.get(id)
	if err != nil {
		return nil, err
	}
	rows, err := c.db.QueryContext(ctx, `SELECT SCHEMA_NAME FROM information_schema.SCHEMATA ORDER BY SCHEMA_NAME`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var value string
		if err := rows.Scan(&value); err != nil {
			return nil, err
		}
		out = append(out, value)
	}
	return out, rows.Err()
}

func (s *Store) Tables(ctx context.Context, id, schema string) ([]Table, error) {
	c, err := s.get(id)
	if err != nil {
		return nil, err
	}
	rows, err := c.db.QueryContext(ctx, `SELECT TABLE_NAME, CASE TABLE_TYPE WHEN 'BASE TABLE' THEN 'table' WHEN 'VIEW' THEN 'view' ELSE LOWER(TABLE_TYPE) END FROM information_schema.TABLES WHERE TABLE_SCHEMA = ? ORDER BY TABLE_NAME`, schema)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Table
	for rows.Next() {
		var value Table
		if err := rows.Scan(&value.Name, &value.Type); err != nil {
			return nil, err
		}
		out = append(out, value)
	}
	return out, rows.Err()
}

func (s *Store) Columns(ctx context.Context, id, schema, table string) ([]Column, error) {
	c, err := s.get(id)
	if err != nil {
		return nil, err
	}
	rows, err := c.db.QueryContext(ctx, `SELECT COLUMN_NAME, DATA_TYPE, COLUMN_TYPE, IS_NULLABLE, COLUMN_KEY, COLUMN_DEFAULT, EXTRA FROM information_schema.COLUMNS WHERE TABLE_SCHEMA = ? AND TABLE_NAME = ? ORDER BY ORDINAL_POSITION`, schema, table)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Column
	for rows.Next() {
		var value Column
		var nullable string
		var def sql.NullString
		if err := rows.Scan(&value.Name, &value.DataType, &value.ColumnType, &nullable, &value.Key, &def, &value.Extra); err != nil {
			return nil, err
		}
		value.Nullable = nullable == "YES"
		if def.Valid {
			value.Default = &def.String
		}
		out = append(out, value)
	}
	return out, rows.Err()
}

func (s *Store) StreamQuery(parent context.Context, connectionID string, req QueryRequest, write func(any) error) error {
	c, err := s.get(connectionID)
	if err != nil {
		return err
	}
	if err := ValidateReadOnlySQL(req.SQL); err != nil {
		return err
	}
	maxRows := req.MaxRows
	if maxRows <= 0 || maxRows > 1000 {
		maxRows = 1000
	}
	queryID := s.nextID()
	ctx, cancel := context.WithCancel(parent)
	s.mu.Lock()
	s.queries[queryID] = cancel
	s.mu.Unlock()
	defer func() {
		cancel()
		s.mu.Lock()
		delete(s.queries, queryID)
		s.mu.Unlock()
	}()
	if err := write(map[string]any{"type": "started", "queryId": queryID}); err != nil {
		return err
	}
	select {
	case s.querySlots <- struct{}{}:
		defer func() { <-s.querySlots }()
	case <-ctx.Done():
		return ctx.Err()
	}

	started := time.Now()
	conn, err := c.db.Conn(ctx)
	if err != nil {
		return err
	}
	defer conn.Close()
	tx, err := conn.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return fmt.Errorf("start read-only transaction: %w", err)
	}
	defer tx.Rollback()

	rows, err := tx.QueryContext(ctx, req.SQL)
	if err != nil {
		return err
	}
	defer rows.Close()
	columnTypes, err := rows.ColumnTypes()
	if err != nil {
		return err
	}
	columns := make([]map[string]any, len(columnTypes))
	for i, col := range columnTypes {
		nullable, known := col.Nullable()
		columns[i] = map[string]any{"name": col.Name(), "databaseType": col.DatabaseTypeName()}
		if known {
			columns[i]["nullable"] = nullable
		}
	}
	if err := write(map[string]any{"type": "meta", "queryId": queryID, "columns": columns}); err != nil {
		return err
	}

	const maxBytes = 8 << 20
	const maxCellBytes = 1 << 20
	values := make([]any, len(columnTypes))
	pointers := make([]any, len(values))
	for i := range values {
		pointers[i] = &values[i]
	}
	batch := make([][]any, 0, 128)
	count, totalBytes := 0, 0
	truncated := false
	for rows.Next() {
		if count >= maxRows || totalBytes >= maxBytes {
			truncated = true
			break
		}
		if err := rows.Scan(pointers...); err != nil {
			return err
		}
		row := make([]any, len(values))
		for i, value := range values {
			converted, size, cellTruncated := transportValue(value, maxCellBytes)
			row[i] = converted
			totalBytes += size
			truncated = truncated || cellTruncated
		}
		batch = append(batch, row)
		count++
		if len(batch) == cap(batch) || totalBytes >= maxBytes {
			if err := write(map[string]any{"type": "rows", "rows": batch}); err != nil {
				return err
			}
			batch = make([][]any, 0, 128)
		}
	}
	if len(batch) > 0 {
		if err := write(map[string]any{"type": "rows", "rows": batch}); err != nil {
			return err
		}
	}
	if err := rows.Err(); err != nil {
		return err
	}
	return write(map[string]any{"type": "complete", "rowCount": count, "truncated": truncated, "elapsedMs": time.Since(started).Milliseconds()})
}

func transportValue(value any, max int) (any, int, bool) {
	if value == nil {
		return nil, 0, false
	}
	var b []byte
	switch v := value.(type) {
	case []byte:
		b = append([]byte(nil), v...)
	case string:
		b = []byte(v)
	default:
		b = []byte(fmt.Sprint(v))
	}
	truncated := len(b) > max
	if truncated {
		b = b[:max]
	}
	if !isText(b) {
		return map[string]any{"encoding": "base64", "data": base64.StdEncoding.EncodeToString(b), "truncated": truncated}, len(b), truncated
	}
	if truncated {
		return map[string]any{"encoding": "utf8", "data": string(b), "truncated": true}, len(b), true
	}
	return string(b), len(b), truncated
}

func isText(b []byte) bool {
	for _, c := range string(b) {
		if c == unicode.ReplacementChar {
			return false
		}
	}
	return true
}

func (s *Store) Cancel(id string) bool {
	s.mu.RLock()
	cancel := s.queries[id]
	s.mu.RUnlock()
	if cancel == nil {
		return false
	}
	cancel()
	return true
}

func (s *Store) nextID() string {
	return strconv.FormatUint(s.sequence.Add(1), 36)
}
