package envy

import (
	"os"
	"strings"
)

// File loads a .env file into the process environment (".env" if name is
// empty). Variables that are already set are not overridden, so the real
// environment always wins over the file.
//
// Syntax:
//
//	# comment
//	KEY=value            # inline comment (after whitespace)
//	export KEY=value
//	KEY="line\nbreak $OTHER ${OTHER}"   escapes and expansion
//	KEY='literal $NOT_EXPANDED'
//	KEY="multi
//	line"
func File(name string) error {
	if name == "" {
		name = ".env"
	}
	m, err := readDotenv(name)
	if err != nil {
		return err
	}
	for k, v := range m {
		if _, ok := os.LookupEnv(k); ok {
			continue
		}
		if err := os.Setenv(k, v); err != nil {
			return err
		}
	}
	return nil
}

// readDotenv parses a .env file without touching the process environment.
func readDotenv(name string) (map[string]string, error) {
	b, err := os.ReadFile(name)
	if err != nil {
		return nil, err
	}
	m, err := parseDotenv(string(b))
	if fe, ok := err.(dotenvError); ok {
		fe.Name = name
		return nil, fe
	}
	return m, err
}

func parseDotenv(src string) (map[string]string, error) {
	src = strings.TrimPrefix(src, "\uFEFF")
	out := make(map[string]string)
	lookup := func(k string) string {
		if v, ok := out[k]; ok {
			return v
		}
		return os.Getenv(k)
	}

	ln := 0
	for src != "" {
		var line string
		line, src = nextLine(src)
		ln++

		line = strings.TrimSpace(line)
		if line == "" || line[0] == '#' {
			continue
		}
		if rest, ok := strings.CutPrefix(line, "export"); ok && rest != "" && (rest[0] == ' ' || rest[0] == '\t') {
			line = strings.TrimSpace(rest)
		}

		key, rest, ok := strings.Cut(line, "=")
		key = strings.TrimSpace(key)
		if !ok {
			return nil, dotenvError{Line: ln, Msg: "expected KEY=VALUE"}
		}
		if !validKey(key) {
			return nil, dotenvError{Line: ln, Msg: "invalid key " + `"` + key + `"`}
		}
		rest = strings.TrimLeft(rest, " \t")

		var val string
		if rest != "" && (rest[0] == '"' || rest[0] == '\'') {
			q := rest[0]
			body := rest[1:]
			start := ln
			for {
				end := closingQuote(body, q)
				if end >= 0 {
					tail := strings.TrimSpace(body[end+1:])
					if tail != "" && tail[0] != '#' {
						return nil, dotenvError{Line: ln, Msg: "unexpected characters after closing quote"}
					}
					body = body[:end]
					break
				}
				if src == "" {
					return nil, dotenvError{Line: start, Msg: "unterminated quoted value"}
				}
				var next string
				next, src = nextLine(src)
				ln++
				body += "\n" + next
			}
			if q == '"' {
				val = expandValue(body, true, lookup)
			} else {
				val = body
			}
		} else {
			if i := inlineComment(rest); i >= 0 {
				rest = rest[:i]
			}
			val = expandValue(strings.TrimSpace(rest), false, lookup)
		}
		out[key] = val
	}
	return out, nil
}

func nextLine(s string) (line, rest string) {
	i := strings.IndexByte(s, '\n')
	if i < 0 {
		return strings.TrimSuffix(s, "\r"), ""
	}
	return strings.TrimSuffix(s[:i], "\r"), s[i+1:]
}

func validKey(k string) bool {
	if k == "" {
		return false
	}
	for i := 0; i < len(k); i++ {
		c := k[i]
		if !(c == '_' || c == '.' || c == '-' || c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9') {
			return false
		}
	}
	return true
}

// closingQuote returns the index of the closing quote q in s, or -1.
// Inside double quotes a backslash escapes the next character.
func closingQuote(s string, q byte) int {
	for i := 0; i < len(s); i++ {
		switch s[i] {
		case '\\':
			if q == '"' {
				i++
			}
		case q:
			return i
		}
	}
	return -1
}

// inlineComment returns the index of a " #" comment in an unquoted value.
func inlineComment(s string) int {
	for i := 1; i < len(s); i++ {
		if s[i] == '#' && (s[i-1] == ' ' || s[i-1] == '\t') {
			return i
		}
	}
	return -1
}

// expandValue expands $VAR / ${VAR} and, if escapes is set, handles
// backslash escapes (\n \r \t \" \\ \$).
func expandValue(s string, escapes bool, lookup func(string) string) string {
	if strings.IndexByte(s, '$') < 0 && (!escapes || strings.IndexByte(s, '\\') < 0) {
		return s
	}
	var b strings.Builder
	b.Grow(len(s))
	for i := 0; i < len(s); i++ {
		c := s[i]
		switch {
		case c == '\\' && escapes && i+1 < len(s):
			i++
			switch s[i] {
			case 'n':
				b.WriteByte('\n')
			case 'r':
				b.WriteByte('\r')
			case 't':
				b.WriteByte('\t')
			case '"', '\\', '$', '\'':
				b.WriteByte(s[i])
			default:
				b.WriteByte('\\')
				b.WriteByte(s[i])
			}
		case c == '$' && i+1 < len(s):
			if s[i+1] == '{' {
				end := strings.IndexByte(s[i+2:], '}')
				if end < 0 {
					b.WriteByte(c)
					continue
				}
				b.WriteString(lookup(s[i+2 : i+2+end]))
				i += 2 + end
				continue
			}
			j := i + 1
			for j < len(s) && isNameChar(s[j]) {
				j++
			}
			if j == i+1 {
				b.WriteByte(c)
				continue
			}
			b.WriteString(lookup(s[i+1 : j]))
			i = j - 1
		default:
			b.WriteByte(c)
		}
	}
	return b.String()
}

func isNameChar(c byte) bool {
	return c == '_' || c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9'
}
