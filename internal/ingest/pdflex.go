package ingest

import (
	"bytes"
	"strconv"
)

// pdfLexer reads PDF syntax: the same grammar serves object bodies and the
// content streams that carry the text, so one lexer covers both.
type pdfLexer struct {
	data []byte
	pos  int
}

func newPDFLexer(data []byte) *pdfLexer { return &pdfLexer{data: data} }

func isPDFSpace(b byte) bool {
	return b == 0 || b == '\t' || b == '\n' || b == '\f' || b == '\r' || b == ' '
}

func isPDFDelimiter(b byte) bool {
	switch b {
	case '(', ')', '<', '>', '[', ']', '{', '}', '/', '%':
		return true
	}
	return false
}

func (l *pdfLexer) skipSpace() {
	for l.pos < len(l.data) {
		b := l.data[l.pos]
		if isPDFSpace(b) {
			l.pos++
			continue
		}
		if b == '%' { // comment to end of line
			for l.pos < len(l.data) && l.data[l.pos] != '\n' && l.data[l.pos] != '\r' {
				l.pos++
			}
			continue
		}
		return
	}
}

// token returns the next raw token: an operator, a keyword, or a punctuation
// mark. Composite values are read by value().
func (l *pdfLexer) token() string {
	l.skipSpace()
	if l.pos >= len(l.data) {
		return ""
	}
	start := l.pos
	b := l.data[l.pos]
	if isPDFDelimiter(b) {
		if b == '<' && l.pos+1 < len(l.data) && l.data[l.pos+1] == '<' {
			l.pos += 2
			return "<<"
		}
		if b == '>' && l.pos+1 < len(l.data) && l.data[l.pos+1] == '>' {
			l.pos += 2
			return ">>"
		}
		l.pos++
		return string(b)
	}
	for l.pos < len(l.data) && !isPDFSpace(l.data[l.pos]) && !isPDFDelimiter(l.data[l.pos]) {
		l.pos++
	}
	return string(l.data[start:l.pos])
}

func (l *pdfLexer) peekToken() string {
	saved := l.pos
	token := l.token()
	l.pos = saved
	return token
}

func (l *pdfLexer) number() (float64, bool) {
	token := l.token()
	value, err := strconv.ParseFloat(token, 64)
	return value, err == nil
}

// value reads one complete object.
func (l *pdfLexer) value() any {
	l.skipSpace()
	if l.pos >= len(l.data) {
		return nil
	}
	switch b := l.data[l.pos]; {
	case b == '<' && l.pos+1 < len(l.data) && l.data[l.pos+1] == '<':
		l.pos += 2
		return l.dict()
	case b == '<':
		l.pos++
		return l.hexString()
	case b == '(':
		l.pos++
		return l.literalString()
	case b == '[':
		l.pos++
		return l.array()
	case b == '/':
		l.pos++
		return pdfName(l.nameBody())
	case b == ']' || b == '>' || b == ')' || b == '}':
		l.pos++
		return nil
	}
	token := l.token()
	switch token {
	case "true":
		return true
	case "false":
		return false
	case "null", "":
		return nil
	}
	if value, err := strconv.ParseFloat(token, 64); err == nil {
		// "12 0 R" is a reference; "12 0" inside an array is two numbers.
		saved := l.pos
		second := l.token()
		if generation, err := strconv.Atoi(second); err == nil {
			if l.peekToken() == "R" {
				l.token()
				return pdfRef{number: int(value), generation: generation}
			}
		}
		l.pos = saved
		return value
	}
	return pdfOperator(token)
}

// pdfOperator is a bare keyword: an operator in a content stream.
type pdfOperator string

func (l *pdfLexer) nameBody() string {
	start := l.pos
	for l.pos < len(l.data) && !isPDFSpace(l.data[l.pos]) && !isPDFDelimiter(l.data[l.pos]) {
		l.pos++
	}
	raw := l.data[start:l.pos]
	if !bytes.ContainsRune(raw, '#') {
		return string(raw)
	}
	var out []byte
	for i := 0; i < len(raw); i++ {
		if raw[i] == '#' && i+2 < len(raw) && isHexDigit(raw[i+1]) && isHexDigit(raw[i+2]) {
			value, _ := strconv.ParseUint(string(raw[i+1:i+3]), 16, 8)
			out = append(out, byte(value))
			i += 2
			continue
		}
		out = append(out, raw[i])
	}
	return string(out)
}

func (l *pdfLexer) dict() pdfDict {
	dict := pdfDict{}
	for l.pos < len(l.data) {
		l.skipSpace()
		if l.pos+1 < len(l.data) && l.data[l.pos] == '>' && l.data[l.pos+1] == '>' {
			l.pos += 2
			return dict
		}
		if l.pos >= len(l.data) || l.data[l.pos] != '/' {
			if l.value() == nil && l.pos >= len(l.data) {
				break
			}
			continue
		}
		l.pos++
		key := pdfName(l.nameBody())
		dict[key] = l.value()
	}
	return dict
}

func (l *pdfLexer) array() pdfArray {
	var out pdfArray
	for l.pos < len(l.data) {
		l.skipSpace()
		if l.pos < len(l.data) && l.data[l.pos] == ']' {
			l.pos++
			return out
		}
		before := l.pos
		value := l.value()
		if l.pos == before {
			l.pos++
			continue
		}
		out = append(out, value)
	}
	return out
}

func (l *pdfLexer) literalString() pdfString {
	var out []byte
	depth := 1
	for l.pos < len(l.data) {
		b := l.data[l.pos]
		l.pos++
		switch b {
		case '\\':
			if l.pos >= len(l.data) {
				return out
			}
			escape := l.data[l.pos]
			l.pos++
			switch escape {
			case 'n':
				out = append(out, '\n')
			case 'r':
				out = append(out, '\r')
			case 't':
				out = append(out, '\t')
			case 'b':
				out = append(out, '\b')
			case 'f':
				out = append(out, '\f')
			case '\n':
			case '\r':
				if l.pos < len(l.data) && l.data[l.pos] == '\n' {
					l.pos++
				}
			default:
				if escape >= '0' && escape <= '7' {
					value := int(escape - '0')
					for i := 0; i < 2 && l.pos < len(l.data) && l.data[l.pos] >= '0' && l.data[l.pos] <= '7'; i++ {
						value = value*8 + int(l.data[l.pos]-'0')
						l.pos++
					}
					out = append(out, byte(value))
					continue
				}
				out = append(out, escape)
			}
		case '(':
			depth++
			out = append(out, b)
		case ')':
			depth--
			if depth == 0 {
				return out
			}
			out = append(out, b)
		default:
			out = append(out, b)
		}
	}
	return out
}

func (l *pdfLexer) hexString() pdfString {
	var digits []byte
	for l.pos < len(l.data) {
		b := l.data[l.pos]
		l.pos++
		if b == '>' {
			break
		}
		if isHexDigit(b) {
			digits = append(digits, b)
		}
	}
	if len(digits)%2 == 1 {
		digits = append(digits, '0')
	}
	out := make([]byte, 0, len(digits)/2)
	for i := 0; i+1 < len(digits); i += 2 {
		value, _ := strconv.ParseUint(string(digits[i:i+2]), 16, 8)
		out = append(out, byte(value))
	}
	return out
}

// streamData returns the bytes of a stream that follows the dictionary just
// read, if there is one.
func (l *pdfLexer) streamData() ([]byte, bool) {
	saved := l.pos
	if l.token() != "stream" {
		l.pos = saved
		return nil, false
	}
	if l.pos < len(l.data) && l.data[l.pos] == '\r' {
		l.pos++
	}
	if l.pos < len(l.data) && l.data[l.pos] == '\n' {
		l.pos++
	}
	rest := l.data[l.pos:]
	end := bytes.Index(rest, []byte("endstream"))
	if end < 0 {
		end = len(rest)
	}
	data := rest[:end]
	// The keyword is preceded by the EOL that the writer added, not by data.
	data = bytes.TrimSuffix(data, []byte("\n"))
	data = bytes.TrimSuffix(data, []byte("\r"))
	l.pos += end
	return data, true
}
