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
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/stdlib"
)

const (
	EngineMySQL    = "mysql"
	EnginePostgres = "postgres"
)

var (
	ErrNotFound          = errors.New("connection not found")
	ErrInvalidConnection = errors.New("invalid connection request")
	ErrInvalidBrowse     = errors.New("invalid browse request")
	ErrBrowseNotFound    = errors.New("table or view not found")
)

type TLSRequest struct {
	Mode          string `json:"mode"`
	ServerName    string `json:"serverName"`
	CAPEM         string `json:"caPem"`
	ClientCertPEM string `json:"clientCertPem"`
	ClientKeyPEM  string `json:"clientKeyPem"`
}

type ConnectionRequest struct {
	Engine   string     `json:"engine"`
	Host     string     `json:"host"`
	Port     int        `json:"port"`
	User     string     `json:"user"`
	Password string     `json:"password"`
	Database string     `json:"database"`
	TLS      TLSRequest `json:"tls"`
}

type Connection struct {
	ID            string `json:"id"`
	Engine        string `json:"engine"`
	ServerVersion string `json:"serverVersion"`
	Database      string `json:"database"`
	db            *sql.DB
	tlsName       string
}

type ConnectionInfo struct {
	Engine        string
	ServerVersion string
	Database      string
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
	SQL           string `json:"sql"`
	MaxRows       int    `json:"maxRows"`
	RecordHistory *bool  `json:"recordHistory,omitempty"`
}

type BrowseSort struct {
	Column    string `json:"column"`
	Direction string `json:"direction"`
}

type BrowseFilter struct {
	Column   string  `json:"column"`
	Operator string  `json:"operator"`
	Value    *string `json:"value,omitempty"`
}

type BrowseRequest struct {
	Schema   string         `json:"schema"`
	Table    string         `json:"table"`
	Offset   int            `json:"offset"`
	PageSize int            `json:"pageSize"`
	Sort     *BrowseSort    `json:"sort,omitempty"`
	Filters  []BrowseFilter `json:"filters,omitempty"`
}

type BrowseColumn struct {
	Name         string `json:"name"`
	DatabaseType string `json:"databaseType"`
	Nullable     bool   `json:"nullable"`
}

type BrowseResult struct {
	Columns  []BrowseColumn `json:"columns"`
	Rows     [][]any        `json:"rows"`
	Offset   int            `json:"offset"`
	PageSize int            `json:"pageSize"`
	HasMore  bool           `json:"hasMore"`
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
		return nil, fmt.Errorf("%w: host and user are required", ErrInvalidConnection)
	}
	req.Engine = normalizeEngine(req.Engine)
	if req.Engine != EngineMySQL && req.Engine != EnginePostgres {
		return nil, fmt.Errorf("%w: engine must be mysql or postgres", ErrInvalidConnection)
	}
	if req.Port == 0 {
		if req.Engine == EnginePostgres {
			req.Port = 5432
		} else {
			req.Port = 3306
		}
	}
	if req.Port < 1 || req.Port > 65535 {
		return nil, fmt.Errorf("%w: port must be between 1 and 65535", ErrInvalidConnection)
	}

	if req.TLS.Mode == "" {
		req.TLS.Mode = "system"
	}
	if err := validateTLSRequest(req.TLS); err != nil {
		return nil, fmt.Errorf("%w: %v", ErrInvalidConnection, err)
	}

	var db *sql.DB
	var tlsName string
	var err error
	if req.Engine == EnginePostgres {
		db, err = openPostgres(req)
	} else {
		db, tlsName, err = s.openMySQL(req)
	}
	if err != nil {
		return nil, err
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
	infoQuery := "SELECT VERSION(), DATABASE()"
	if req.Engine == EnginePostgres {
		infoQuery = "SELECT VERSION(), current_database()"
	}
	if err := db.QueryRowContext(ctx, infoQuery).Scan(&version, &database); err != nil {
		db.Close()
		if tlsName != "" {
			mysql.DeregisterTLSConfig(tlsName)
		}
		return nil, fmt.Errorf("read server information: %w", err)
	}
	c := &Connection{ID: s.nextID(), Engine: req.Engine, ServerVersion: version.String, Database: database.String, db: db, tlsName: tlsName}
	s.mu.Lock()
	s.connections[c.ID] = c
	s.mu.Unlock()
	return c, nil
}

func normalizeEngine(engine string) string {
	switch strings.ToLower(strings.TrimSpace(engine)) {
	case "", "mysql", "mariadb":
		return EngineMySQL
	case "postgres", "postgresql":
		return EnginePostgres
	default:
		return strings.ToLower(strings.TrimSpace(engine))
	}
}

func validateTLSRequest(req TLSRequest) error {
	if req.Mode != "disabled" && req.Mode != "system" && req.Mode != "custom" && req.Mode != "mutual" {
		return errors.New("TLS mode must be disabled, system, custom, or mutual")
	}
	if (req.Mode == "custom" || req.Mode == "mutual") && req.CAPEM == "" {
		return errors.New("custom and mutual TLS require a CA certificate")
	}
	if req.Mode == "mutual" && (req.ClientCertPEM == "" || req.ClientKeyPEM == "") {
		return errors.New("mutual TLS requires a client certificate and key")
	}
	return nil
}

func (s *Store) openMySQL(req ConnectionRequest) (*sql.DB, string, error) {
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

	var tlsName string
	if req.TLS.Mode != "disabled" {
		name := "rowlight-" + s.nextID()
		tlsCfg, err := makeTLSConfig(req.Host, req.TLS)
		if err != nil {
			return nil, "", err
		}
		if err := mysql.RegisterTLSConfig(name, tlsCfg); err != nil {
			return nil, "", fmt.Errorf("register TLS configuration: %w", err)
		}
		cfg.TLSConfig = name
		tlsName = name
	}

	db, err := sql.Open("mysql", cfg.FormatDSN())
	if err != nil {
		if tlsName != "" {
			mysql.DeregisterTLSConfig(tlsName)
		}
		return nil, "", fmt.Errorf("configure connection: %w", err)
	}
	return db, tlsName, nil
}

func openPostgres(req ConnectionRequest) (*sql.DB, error) {
	cfg, err := pgx.ParseConfig("sslmode=disable")
	if err != nil {
		return nil, fmt.Errorf("configure PostgreSQL connection: %w", err)
	}
	cfg.Host = req.Host
	cfg.Port = uint16(req.Port)
	cfg.User = req.User
	cfg.Password = req.Password
	cfg.Database = req.Database
	if cfg.Database == "" {
		cfg.Database = req.User
	}
	cfg.ConnectTimeout = 10 * time.Second
	cfg.Fallbacks = nil
	if req.TLS.Mode != "disabled" {
		tlsCfg, err := makeTLSConfig(req.Host, req.TLS)
		if err != nil {
			return nil, err
		}
		cfg.TLSConfig = tlsCfg
	}
	return stdlib.OpenDB(*cfg), nil
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

func (s *Store) Info(id string) (ConnectionInfo, error) {
	c, err := s.get(id)
	if err != nil {
		return ConnectionInfo{}, err
	}
	return ConnectionInfo{Engine: c.Engine, ServerVersion: c.ServerVersion, Database: c.Database}, nil
}

func (s *Store) Schemas(ctx context.Context, id string) ([]string, error) {
	c, err := s.get(id)
	if err != nil {
		return nil, err
	}
	query := `SELECT SCHEMA_NAME FROM information_schema.SCHEMATA ORDER BY SCHEMA_NAME`
	if c.Engine == EnginePostgres {
		query = `SELECT schema_name FROM information_schema.schemata WHERE schema_name <> 'information_schema' AND schema_name NOT LIKE 'pg\_%' ESCAPE '\' ORDER BY schema_name`
	}
	rows, err := c.db.QueryContext(ctx, query)
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
	query := `SELECT TABLE_NAME, CASE TABLE_TYPE WHEN 'BASE TABLE' THEN 'table' WHEN 'VIEW' THEN 'view' ELSE LOWER(TABLE_TYPE) END FROM information_schema.TABLES WHERE TABLE_SCHEMA = ? ORDER BY TABLE_NAME`
	if c.Engine == EnginePostgres {
		query = `SELECT table_name, CASE table_type WHEN 'BASE TABLE' THEN 'table' WHEN 'VIEW' THEN 'view' WHEN 'FOREIGN' THEN 'foreign table' ELSE LOWER(table_type) END FROM information_schema.tables WHERE table_schema = $1 ORDER BY table_name`
	}
	rows, err := c.db.QueryContext(ctx, query, schema)
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
	query := `SELECT COLUMN_NAME, DATA_TYPE, COLUMN_TYPE, IS_NULLABLE, COLUMN_KEY, COLUMN_DEFAULT, EXTRA FROM information_schema.COLUMNS WHERE TABLE_SCHEMA = ? AND TABLE_NAME = ? ORDER BY ORDINAL_POSITION`
	if c.Engine == EnginePostgres {
		query = `SELECT c.column_name, c.data_type, c.udt_name, c.is_nullable,
			CASE WHEN EXISTS (
				SELECT 1 FROM information_schema.table_constraints tc
				JOIN information_schema.key_column_usage kcu
				  ON tc.constraint_catalog = kcu.constraint_catalog
				 AND tc.constraint_schema = kcu.constraint_schema
				 AND tc.constraint_name = kcu.constraint_name
				WHERE tc.constraint_type = 'PRIMARY KEY'
				  AND tc.table_schema = c.table_schema AND tc.table_name = c.table_name
				  AND kcu.column_name = c.column_name
			) THEN 'PRI' ELSE '' END,
			c.column_default,
			CASE WHEN c.is_identity = 'YES' THEN 'identity' WHEN c.is_generated = 'ALWAYS' THEN 'generated' ELSE '' END
		FROM information_schema.columns c
		WHERE c.table_schema = $1 AND c.table_name = $2
		ORDER BY c.ordinal_position`
	}
	rows, err := c.db.QueryContext(ctx, query, schema, table)
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

func (s *Store) Browse(ctx context.Context, connectionID string, req BrowseRequest) (BrowseResult, error) {
	if err := normalizeBrowseRequest(&req); err != nil {
		return BrowseResult{}, err
	}
	c, err := s.get(connectionID)
	if err != nil {
		return BrowseResult{}, err
	}
	select {
	case s.querySlots <- struct{}{}:
		defer func() { <-s.querySlots }()
	case <-ctx.Done():
		return BrowseResult{}, ctx.Err()
	}

	tx, err := c.db.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return BrowseResult{}, fmt.Errorf("start read-only browse transaction: %w", err)
	}
	defer tx.Rollback()

	columns, err := browseColumns(ctx, tx, c.Engine, req.Schema, req.Table)
	if err != nil {
		return BrowseResult{}, err
	}
	if len(columns) == 0 {
		return BrowseResult{}, fmt.Errorf("%w: %s.%s", ErrBrowseNotFound, req.Schema, req.Table)
	}
	query, args, err := buildBrowseQueryForEngine(c.Engine, req, columns)
	if err != nil {
		return BrowseResult{}, err
	}
	rows, err := tx.QueryContext(ctx, query, args...)
	if err != nil {
		return BrowseResult{}, err
	}
	defer rows.Close()

	columnTypes, err := rows.ColumnTypes()
	if err != nil {
		return BrowseResult{}, err
	}
	result := BrowseResult{
		Columns:  make([]BrowseColumn, len(columnTypes)),
		Rows:     make([][]any, 0, req.PageSize),
		Offset:   req.Offset,
		PageSize: req.PageSize,
	}
	for i, columnType := range columnTypes {
		nullable, known := columnType.Nullable()
		if !known {
			nullable = columns[i].Nullable
		}
		result.Columns[i] = BrowseColumn{Name: columnType.Name(), DatabaseType: columnType.DatabaseTypeName(), Nullable: nullable}
	}

	const maxBytes = 8 << 20
	const maxCellBytes = 1 << 20
	values := make([]any, len(columnTypes))
	pointers := make([]any, len(values))
	for i := range values {
		pointers[i] = &values[i]
	}
	totalBytes := 0
	for rows.Next() {
		if len(result.Rows) >= req.PageSize || totalBytes >= maxBytes {
			result.HasMore = true
			break
		}
		if err := rows.Scan(pointers...); err != nil {
			return BrowseResult{}, err
		}
		row := make([]any, len(values))
		for i, value := range values {
			converted, size, _ := transportValueForType(value, columnTypes[i].DatabaseTypeName(), maxCellBytes)
			row[i] = converted
			totalBytes += size
		}
		result.Rows = append(result.Rows, row)
	}
	if err := rows.Err(); err != nil {
		return BrowseResult{}, err
	}
	return result, nil
}

type browseQuerier interface {
	QueryContext(context.Context, string, ...any) (*sql.Rows, error)
}

func browseColumns(ctx context.Context, queryer browseQuerier, engine, schema, table string) ([]BrowseColumn, error) {
	query := `SELECT COLUMN_NAME, DATA_TYPE, IS_NULLABLE FROM information_schema.COLUMNS WHERE TABLE_SCHEMA = ? AND TABLE_NAME = ? ORDER BY ORDINAL_POSITION`
	if engine == EnginePostgres {
		query = `SELECT column_name, data_type, is_nullable FROM information_schema.columns WHERE table_schema = $1 AND table_name = $2 ORDER BY ordinal_position`
	}
	rows, err := queryer.QueryContext(ctx, query, schema, table)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var columns []BrowseColumn
	for rows.Next() {
		var column BrowseColumn
		var nullable string
		if err := rows.Scan(&column.Name, &column.DatabaseType, &nullable); err != nil {
			return nil, err
		}
		column.Nullable = nullable == "YES"
		columns = append(columns, column)
	}
	return columns, rows.Err()
}

func normalizeBrowseRequest(req *BrowseRequest) error {
	if strings.TrimSpace(req.Schema) == "" || strings.TrimSpace(req.Table) == "" {
		return fmt.Errorf("%w: schema and table are required", ErrInvalidBrowse)
	}
	if req.Offset < 0 {
		return fmt.Errorf("%w: offset cannot be negative", ErrInvalidBrowse)
	}
	if req.PageSize == 0 {
		req.PageSize = 100
	}
	if req.PageSize < 1 || req.PageSize > 200 {
		return fmt.Errorf("%w: pageSize must be between 1 and 200", ErrInvalidBrowse)
	}
	if len(req.Filters) > 20 {
		return fmt.Errorf("%w: at most 20 filters are allowed", ErrInvalidBrowse)
	}
	return nil
}

func buildBrowseQuery(req BrowseRequest, columns []BrowseColumn) (string, []any, error) {
	return buildBrowseQueryForEngine(EngineMySQL, req, columns)
}

func buildBrowseQueryForEngine(engine string, req BrowseRequest, columns []BrowseColumn) (string, []any, error) {
	knownColumns := make(map[string]struct{}, len(columns))
	quotedColumns := make([]string, len(columns))
	for i, column := range columns {
		knownColumns[column.Name] = struct{}{}
		quotedColumns[i] = quoteIdentifierForEngine(engine, column.Name)
	}
	query := "SELECT " + strings.Join(quotedColumns, ", ") + " FROM " + quoteIdentifierForEngine(engine, req.Schema) + "." + quoteIdentifierForEngine(engine, req.Table)
	conditions := make([]string, 0, len(req.Filters))
	args := make([]any, 0, len(req.Filters)+2)
	placeholder := func() string {
		if engine == EnginePostgres {
			return "$" + strconv.Itoa(len(args)+1)
		}
		return "?"
	}
	for _, filter := range req.Filters {
		if _, ok := knownColumns[filter.Column]; !ok {
			return "", nil, fmt.Errorf("%w: unknown filter column %q", ErrInvalidBrowse, filter.Column)
		}
		column := quoteIdentifierForEngine(engine, filter.Column)
		switch filter.Operator {
		case "equals":
			if filter.Value == nil {
				return "", nil, fmt.Errorf("%w: equals requires a value", ErrInvalidBrowse)
			}
			conditions = append(conditions, column+" = "+placeholder())
			args = append(args, *filter.Value)
		case "contains":
			if filter.Value == nil {
				return "", nil, fmt.Errorf("%w: contains requires a value", ErrInvalidBrowse)
			}
			if engine == EnginePostgres {
				conditions = append(conditions, "POSITION("+placeholder()+" IN CAST("+column+" AS TEXT)) > 0")
			} else {
				conditions = append(conditions, "LOCATE("+placeholder()+", "+column+") > 0")
			}
			args = append(args, *filter.Value)
		case "startsWith":
			if filter.Value == nil {
				return "", nil, fmt.Errorf("%w: startsWith requires a value", ErrInvalidBrowse)
			}
			firstPlaceholder := placeholder()
			args = append(args, *filter.Value)
			secondPlaceholder := placeholder()
			if engine == EnginePostgres {
				conditions = append(conditions, "LEFT(CAST("+column+" AS TEXT), CHAR_LENGTH("+firstPlaceholder+")) = "+secondPlaceholder)
			} else {
				conditions = append(conditions, "LEFT("+column+", CHAR_LENGTH("+firstPlaceholder+")) = "+secondPlaceholder)
			}
			args = append(args, *filter.Value)
		case "isNull":
			if filter.Value != nil {
				return "", nil, fmt.Errorf("%w: isNull does not accept a value", ErrInvalidBrowse)
			}
			conditions = append(conditions, column+" IS NULL")
		case "isNotNull":
			if filter.Value != nil {
				return "", nil, fmt.Errorf("%w: isNotNull does not accept a value", ErrInvalidBrowse)
			}
			conditions = append(conditions, column+" IS NOT NULL")
		default:
			return "", nil, fmt.Errorf("%w: unsupported filter operator %q", ErrInvalidBrowse, filter.Operator)
		}
	}
	if len(conditions) > 0 {
		query += " WHERE " + strings.Join(conditions, " AND ")
	}
	if req.Sort != nil {
		if _, ok := knownColumns[req.Sort.Column]; !ok {
			return "", nil, fmt.Errorf("%w: unknown sort column %q", ErrInvalidBrowse, req.Sort.Column)
		}
		direction := strings.ToUpper(req.Sort.Direction)
		if direction != "ASC" && direction != "DESC" {
			return "", nil, fmt.Errorf("%w: sort direction must be asc or desc", ErrInvalidBrowse)
		}
		query += " ORDER BY " + quoteIdentifierForEngine(engine, req.Sort.Column) + " " + direction
	}
	query += " LIMIT " + placeholder()
	args = append(args, req.PageSize+1, req.Offset)
	if engine == EnginePostgres {
		query += " OFFSET $" + strconv.Itoa(len(args))
	} else {
		query += " OFFSET ?"
	}
	return query, args, nil
}

func quoteIdentifier(value string) string {
	return quoteIdentifierForEngine(EngineMySQL, value)
}

func quoteIdentifierForEngine(engine, value string) string {
	if engine == EnginePostgres {
		return `"` + strings.ReplaceAll(value, `"`, `""`) + `"`
	}
	return "`" + strings.ReplaceAll(value, "`", "``") + "`"
}

func (s *Store) StreamQuery(parent context.Context, connectionID string, req QueryRequest, write func(any) error) error {
	c, err := s.get(connectionID)
	if err != nil {
		return err
	}
	if err := ValidateReadOnlySQLForEngine(c.Engine, req.SQL); err != nil {
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
			converted, size, cellTruncated := transportValueForType(value, columnTypes[i].DatabaseTypeName(), maxCellBytes)
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
	return transportValueForType(value, "", max)
}

func transportValueForType(value any, databaseType string, max int) (any, int, bool) {
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
	if isBinaryDatabaseType(databaseType) || !isText(b) {
		return map[string]any{"encoding": "base64", "data": base64.StdEncoding.EncodeToString(b), "truncated": truncated}, len(b), truncated
	}
	if truncated {
		return map[string]any{"encoding": "utf8", "data": string(b), "truncated": true}, len(b), true
	}
	return string(b), len(b), truncated
}

func isBinaryDatabaseType(databaseType string) bool {
	switch strings.ToUpper(strings.TrimSpace(databaseType)) {
	case "BYTEA", "BINARY", "VARBINARY", "TINYBLOB", "BLOB", "MEDIUMBLOB", "LONGBLOB", "BIT":
		return true
	default:
		return false
	}
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
