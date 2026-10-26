package transcript

import "strings"

// tok is a shell word, or a control or redirection operator when op is set.
// dyn marks a word that holds an expansion ($VAR, $(..), backticks) we cannot resolve.
type tok struct {
	text string
	op   bool
	dyn  bool
}

func isDigits(s string) bool {
	if s == "" {
		return false
	}
	for _, r := range s {
		if r < '0' || r > '9' {
			return false
		}
	}
	return true
}

// shellTokens splits a command into words and operators. Quoted text never yields operators.
func shellTokens(s string) []tok {
	var out []tok
	var cur strings.Builder
	has, dyn, quoted := false, false, false
	flush := func() {
		if has {
			out = append(out, tok{text: cur.String(), dyn: dyn})
		}
		cur.Reset()
		has, dyn, quoted = false, false, false
	}
	op := func(text string) {
		flush()
		out = append(out, tok{text: text, op: true})
	}
	// A glued file descriptor (`2>f`) is part of the operator, not a word.
	dropFD := func() {
		if has && !quoted && isDigits(cur.String()) {
			cur.Reset()
			has = false
		}
	}
	var quote byte
	for i := 0; i < len(s); i++ {
		c := s[i]
		next := byte(0)
		if i+1 < len(s) {
			next = s[i+1]
		}
		switch {
		case quote != 0:
			switch {
			case c == quote:
				quote = 0
			case c == '\\' && quote == '"' && i+1 < len(s):
				i++
				cur.WriteByte(s[i])
			default:
				if quote == '"' && (c == '$' || c == '`') {
					dyn = true
				}
				cur.WriteByte(c)
			}
		case c == '\'' || c == '"':
			quote, has, quoted = c, true, true
		case c == '\\' && i+1 < len(s):
			i++
			cur.WriteByte(s[i])
			has = true
		case c == '$' || c == '`':
			dyn = true
			cur.WriteByte(c)
			has = true
		case c == ' ' || c == '\t' || c == '\r':
			flush()
		case c == '\n' || c == ';':
			op(";")
		case c == '#' && !has:
			for i < len(s) && s[i] != '\n' {
				i++
			}
			i--
		case c == '(' || c == ')':
			op(string(c))
		case c == '&' && next == '>':
			i++
			if i+1 < len(s) && s[i+1] == '>' {
				i++
				op("&>>")
			} else {
				op("&>")
			}
		case c == '&' || c == '|':
			if next == c {
				i++
				op(string([]byte{c, c}))
			} else {
				op(string(c))
			}
		case c == '>':
			dropFD()
			switch next {
			case '>':
				i++
				op(">>")
			case '|':
				i++
				op(">|")
			case '&':
				i++
				op(">&")
			default:
				op(">")
			}
		case c == '<':
			dropFD()
			n := 1
			for n < 3 && i+n < len(s) && s[i+n] == '<' {
				n++
			}
			i += n - 1
			op(strings.Repeat("<", n))
		default:
			cur.WriteByte(c)
			has = true
		}
	}
	flush()
	return out
}

type pendingHeredoc struct{ delim string }

// stripHeredocBodies drops heredoc payloads: a `>` inside one is text, not a redirect.
func stripHeredocBodies(s string) string {
	var out strings.Builder
	var pending []pendingHeredoc
	var quote byte
	for i := 0; i < len(s); i++ {
		c := s[i]
		if quote != 0 {
			out.WriteByte(c)
			if c == '\\' && quote == '"' && i+1 < len(s) {
				i++
				out.WriteByte(s[i])
			} else if c == quote {
				quote = 0
			}
			continue
		}
		switch {
		case c == '\'' || c == '"':
			quote = c
			out.WriteByte(c)
		case c == '\\' && i+1 < len(s):
			out.WriteByte(c)
			i++
			out.WriteByte(s[i])
		case c == '\n':
			out.WriteByte(c)
			for _, h := range pending {
				i = skipHeredocBody(s, i+1, h.delim) - 1
			}
			pending = nil
		case strings.HasPrefix(s[i:], "<<") && !strings.HasPrefix(s[i:], "<<<"):
			end, delim := parseHeredocStart(s, i)
			if delim == "" {
				out.WriteByte(c)
				continue
			}
			pending = append(pending, pendingHeredoc{delim})
			out.WriteString(s[i:end])
			i = end - 1
		default:
			out.WriteByte(c)
		}
	}
	return out.String()
}

func isWordStart(c byte) bool {
	return c == '_' || (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z')
}

func isWordChar(c byte) bool { return isWordStart(c) || (c >= '0' && c <= '9') }

// parseHeredocStart reads `<<[-] DELIM` at s[i:]; delim is empty when it is not a heredoc (`1 << 2`).
func parseHeredocStart(s string, i int) (end int, delim string) {
	j := i + 2
	if j < len(s) && s[j] == '-' {
		j++
	}
	for j < len(s) && (s[j] == ' ' || s[j] == '\t') {
		j++
	}
	if j >= len(s) {
		return 0, ""
	}
	if s[j] == '\'' || s[j] == '"' {
		close := strings.IndexByte(s[j+1:], s[j])
		if close <= 0 {
			return 0, ""
		}
		return j + close + 2, s[j+1 : j+1+close]
	}
	if s[j] == '\\' {
		j++
	}
	start := j
	if j >= len(s) || !isWordStart(s[j]) {
		return 0, ""
	}
	for j < len(s) && isWordChar(s[j]) {
		j++
	}
	return j, s[start:j]
}

// skipHeredocBody returns the index just past the delimiter line starting the search at from.
func skipHeredocBody(s string, from int, delim string) int {
	for from < len(s) {
		nl := strings.IndexByte(s[from:], '\n')
		line, next := s[from:], len(s)
		if nl >= 0 {
			line, next = s[from:from+nl], from+nl+1
		}
		from = next
		if strings.TrimSpace(line) == delim {
			break
		}
	}
	return from
}
