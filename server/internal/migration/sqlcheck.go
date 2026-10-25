package migration

import (
	"fmt"
	"strings"
	"unicode"
)

func checkTransactionControl(sql string) error {
	for i, stmt := range splitStatements(sql) {
		words := leadingWords(stmt, 2)
		if len(words) == 0 {
			continue
		}
		first := words[0]
		second := ""
		if len(words) > 1 {
			second = words[1]
		}
		forbidden := false
		switch first {
		case "BEGIN", "START", "COMMIT", "END", "ABORT":
			forbidden = true
		case "ROLLBACK":
			forbidden = second != "TO"
		case "PREPARE":
			forbidden = second == "TRANSACTION"
		}
		if forbidden {
			return fmt.Errorf("statement ke-%d (%s) tidak diizinkan: migration sudah dijalankan dalam satu transaksi oleh platform", i+1, first)
		}
	}
	return nil
}

func splitStatements(sql string) []string {
	var out []string
	start := 0
	n := len(sql)
	for i := 0; i < n; {
		c := sql[i]
		switch {
		case c == '-' && i+1 < n && sql[i+1] == '-':
			for i < n && sql[i] != '\n' {
				i++
			}
		case c == '/' && i+1 < n && sql[i+1] == '*':
			i = skipBlockComment(sql, i)
		case c == '\'':
			escapes := i > 0 && (sql[i-1] == 'E' || sql[i-1] == 'e') && (i == 1 || !isIdentChar(sql[i-2]))
			i = skipQuoted(sql, i, '\'', escapes)
		case c == '"':
			i = skipQuoted(sql, i, '"', false)
		case c == '$':
			if tag, ok := dollarTag(sql, i); ok {
				end := strings.Index(sql[i+len(tag):], tag)
				if end < 0 {
					i = n
				} else {
					i += len(tag) + end + len(tag)
				}
			} else {
				i++
			}
		case c == ';':
			if strings.TrimSpace(sql[start:i]) != "" {
				out = append(out, sql[start:i])
			}
			i++
			start = i
		default:
			i++
		}
	}
	if strings.TrimSpace(sql[start:]) != "" {
		out = append(out, sql[start:])
	}
	return out
}

func skipBlockComment(s string, i int) int {
	depth := 0
	for i < len(s) {
		switch {
		case strings.HasPrefix(s[i:], "/*"):
			depth++
			i += 2
		case strings.HasPrefix(s[i:], "*/"):
			depth--
			i += 2
			if depth == 0 {
				return i
			}
		default:
			i++
		}
	}
	return i
}

func skipQuoted(s string, i int, q byte, backslashEscapes bool) int {
	i++
	for i < len(s) {
		switch {
		case backslashEscapes && s[i] == '\\':
			i += 2
		case s[i] == q && i+1 < len(s) && s[i+1] == q:
			i += 2
		case s[i] == q:
			return i + 1
		default:
			i++
		}
	}
	return i
}

func dollarTag(s string, i int) (string, bool) {
	if i > 0 && isIdentChar(s[i-1]) {
		return "", false
	}
	j := i + 1
	for j < len(s) && s[j] != '$' {
		if !isIdentChar(s[j]) || (j == i+1 && s[j] >= '0' && s[j] <= '9') {
			return "", false
		}
		j++
	}
	if j >= len(s) {
		return "", false
	}
	return s[i : j+1], true
}

func isIdentChar(c byte) bool {
	return c == '_' || c >= '0' && c <= '9' || c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= 0x80
}

func leadingWords(stmt string, n int) []string {
	var words []string
	i := 0
	for i < len(stmt) && len(words) < n {
		c := stmt[i]
		switch {
		case unicode.IsSpace(rune(c)):
			i++
		case strings.HasPrefix(stmt[i:], "--"):
			for i < len(stmt) && stmt[i] != '\n' {
				i++
			}
		case strings.HasPrefix(stmt[i:], "/*"):
			i = skipBlockComment(stmt, i)
		case isIdentChar(c):
			j := i
			for j < len(stmt) && isIdentChar(stmt[j]) {
				j++
			}
			words = append(words, strings.ToUpper(stmt[i:j]))
			i = j
		default:
			return words
		}
	}
	return words
}
