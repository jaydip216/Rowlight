package store

import (
	"errors"
	"fmt"
	"strings"
	"unicode"
)

var allowedFirst = map[string]bool{
	"SELECT": true, "SHOW": true, "DESCRIBE": true, "DESC": true, "EXPLAIN": true,
}

var bannedTokens = map[string]bool{
	"INTO": true, "OUTFILE": true, "DUMPFILE": true, "PROCEDURE": true,
	"UPDATE": true, "INSERT": true, "DELETE": true, "REPLACE": true,
	"CREATE": true, "ALTER": true, "DROP": true, "TRUNCATE": true,
	"GRANT": true, "REVOKE": true, "CALL": true, "LOAD": true,
	"LOCK": true, "UNLOCK": true, "SET": true, "USE": true,
	"BEGIN": true, "START": true, "COMMIT": true, "ROLLBACK": true,
	"HANDLER": true, "DO": true, "KILL": true,
	"SHARE": true,
}

// ValidateReadOnlySQL intentionally supports a narrow subset. It is an accident
// prevention layer; database privileges remain the authorization boundary.
func ValidateReadOnlySQL(query string) error {
	return ValidateReadOnlySQLForEngine(EngineMySQL, query)
}

// ValidateReadOnlySQLForEngine applies the conservative statement policy with
// the quoting and comment rules of the selected database engine.
func ValidateReadOnlySQLForEngine(engine, query string) error {
	engine = normalizeEngine(engine)
	tokens, statements, executableComment, err := lexSQLForEngine(engine, query)
	if err != nil {
		return err
	}
	if engine == EngineMySQL && executableComment {
		return errors.New("MySQL executable comments are not allowed in read-only mode")
	}
	if statements != 1 || len(tokens) == 0 {
		return errors.New("enter exactly one SQL statement")
	}
	if !allowedFirst[tokens[0]] {
		return fmt.Errorf("%s statements are not allowed in read-only mode", tokens[0])
	}
	for _, token := range tokens[1:] {
		if bannedTokens[token] {
			return fmt.Errorf("%s is not allowed in read-only mode", token)
		}
	}
	return nil
}

func lexSQL(input string) (tokens []string, statements int, executableComment bool, err error) {
	return lexSQLForEngine(EngineMySQL, input)
}

func lexSQLForEngine(engine, input string) (tokens []string, statements int, executableComment bool, err error) {
	var word strings.Builder
	flush := func() {
		if word.Len() > 0 {
			tokens = append(tokens, strings.ToUpper(word.String()))
			word.Reset()
		}
	}
	hasContent := false
	for i := 0; i < len(input); {
		c := input[i]
		if engine == EnginePostgres && c == '$' {
			if delimiter, ok := postgresDollarDelimiter(input[i:]); ok {
				flush()
				hasContent = true
				end := strings.Index(input[i+len(delimiter):], delimiter)
				if end < 0 {
					return nil, 0, false, errors.New("unterminated PostgreSQL dollar-quoted string")
				}
				i += len(delimiter) + end + len(delimiter)
				continue
			}
		}
		if c == '\'' || c == '"' || (engine == EngineMySQL && c == '`') {
			escapeBackslash := engine == EngineMySQL
			if engine == EnginePostgres && c == '\'' && strings.EqualFold(word.String(), "E") {
				escapeBackslash = true
			}
			flush()
			hasContent = true
			quote := c
			i++
			closed := false
			for i < len(input) {
				if escapeBackslash && input[i] == '\\' && quote != '`' && i+1 < len(input) {
					i += 2
					continue
				}
				if input[i] == quote {
					if i+1 < len(input) && input[i+1] == quote {
						i += 2
						continue
					}
					i++
					closed = true
					break
				}
				i++
			}
			if !closed {
				return nil, 0, false, errors.New("unterminated SQL string or identifier")
			}
			continue
		}
		mysqlDashComment := engine == EngineMySQL && c == '-' && i+2 < len(input) && input[i+1] == '-' && unicode.IsSpace(rune(input[i+2]))
		postgresDashComment := engine == EnginePostgres && c == '-' && i+1 < len(input) && input[i+1] == '-'
		if (engine == EngineMySQL && c == '#') || mysqlDashComment || postgresDashComment {
			flush()
			for i < len(input) && input[i] != '\n' {
				i++
			}
			continue
		}
		if c == '/' && i+1 < len(input) && input[i+1] == '*' {
			flush()
			if i+2 < len(input) && input[i+2] == '!' {
				executableComment = true
			}
			end, ok := blockCommentEnd(engine, input, i)
			if !ok {
				return nil, 0, false, errors.New("unterminated SQL comment")
			}
			i = end
			continue
		}
		if c == ';' {
			flush()
			if hasContent {
				statements++
				hasContent = false
			}
			i++
			continue
		}
		if unicode.IsLetter(rune(c)) || unicode.IsDigit(rune(c)) || c == '_' || c == '$' {
			word.WriteByte(c)
			hasContent = true
		} else {
			flush()
			if !unicode.IsSpace(rune(c)) {
				hasContent = true
			}
		}
		i++
	}
	flush()
	if hasContent {
		statements++
	}
	return tokens, statements, executableComment, nil
}

func postgresDollarDelimiter(input string) (string, bool) {
	if len(input) < 2 || input[0] != '$' {
		return "", false
	}
	for i := 1; i < len(input); i++ {
		switch c := input[i]; {
		case c == '$':
			return input[:i+1], true
		case i == 1 && !(c == '_' || unicode.IsLetter(rune(c))):
			return "", false
		case i > 1 && !(c == '_' || unicode.IsLetter(rune(c)) || unicode.IsDigit(rune(c))):
			return "", false
		}
	}
	return "", false
}

func blockCommentEnd(engine, input string, start int) (int, bool) {
	depth := 1
	for i := start + 2; i < len(input)-1; i++ {
		if engine == EnginePostgres && input[i] == '/' && input[i+1] == '*' {
			depth++
			i++
			continue
		}
		if input[i] == '*' && input[i+1] == '/' {
			depth--
			i++
			if depth == 0 {
				return i + 1, true
			}
		}
	}
	return 0, false
}
