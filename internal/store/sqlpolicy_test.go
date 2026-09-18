package store

import "testing"

func TestValidateReadOnlySQL(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name  string
		sql   string
		valid bool
	}{
		{name: "select", sql: "SELECT id, name FROM users", valid: true},
		{name: "show", sql: "SHOW TABLES", valid: true},
		{name: "describe", sql: "DESCRIBE `odd;name`", valid: true},
		{name: "semicolon in string", sql: "SELECT ';' AS value;", valid: true},
		{name: "comment", sql: "-- harmless\nSELECT 1", valid: true},
		{name: "multiple", sql: "SELECT 1; SELECT 2", valid: false},
		{name: "write", sql: "UPDATE users SET admin = 1", valid: false},
		{name: "select outfile", sql: "SELECT secret INTO OUTFILE '/tmp/x' FROM users", valid: false},
		{name: "locking read", sql: "SELECT * FROM users FOR UPDATE", valid: false},
		{name: "executable comment", sql: "SELECT 1 /*!50000 INTO OUTFILE '/tmp/x' */", valid: false},
		{name: "unterminated", sql: "SELECT 'oops", valid: false},
		{name: "empty", sql: " -- no query", valid: false},
		{name: "cte deferred", sql: "WITH values AS (SELECT 1) SELECT * FROM values", valid: false},
	}
	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			err := ValidateReadOnlySQL(tt.sql)
			if (err == nil) != tt.valid {
				t.Fatalf("ValidateReadOnlySQL() error = %v, valid want %v", err, tt.valid)
			}
		})
	}
}

func TestTransportValueMarksTruncationAndBinary(t *testing.T) {
	t.Parallel()
	value, size, truncated := transportValue("abcdef", 3)
	if size != 3 || !truncated {
		t.Fatalf("text size = %d, truncated = %v", size, truncated)
	}
	encoded, ok := value.(map[string]any)
	if !ok || encoded["encoding"] != "utf8" || encoded["data"] != "abc" {
		t.Fatalf("truncated text = %#v", value)
	}

	value, _, truncated = transportValue([]byte{0xff, 0x00}, 10)
	encoded, ok = value.(map[string]any)
	if !ok || encoded["encoding"] != "base64" || truncated {
		t.Fatalf("binary value = %#v, truncated = %v", value, truncated)
	}
}
