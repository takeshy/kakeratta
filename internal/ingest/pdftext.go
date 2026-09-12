package ingest

import (
	"strconv"
	"strings"
	"unicode/utf16"
)

// pdfFont is what is needed to read a PDF string: how wide a code is, and what
// it means.
//
// The widths matter as much as the characters. Without them the interpreter
// cannot tell how far a glyph advanced the pen, and every individually
// positioned glyph - which is how Japanese typesetters place Latin text - looks
// like a gap wide enough to be a space.
type pdfFont struct {
	twoByte      bool
	toUnicode    map[uint32]string
	differences  map[byte]rune
	simple       bool
	widths       map[uint32]float64
	defaultWidth float64
}

// width is the advance of one code, in thousandths of an em.
func (f *pdfFont) width(code uint32) float64 {
	if width, ok := f.widths[code]; ok {
		return width
	}
	return f.defaultWidth
}

func (f *pdfFile) font(dict pdfDict) *pdfFont {
	font := &pdfFont{simple: true}
	subtype := dict.name("Subtype")
	if subtype == "Type0" {
		// Composite fonts address glyphs with two-byte codes. Identity-H, which
		// is what nearly every CJK PDF uses, has no meaning without ToUnicode.
		font.twoByte = true
		font.simple = false
	}
	if stream, ok := f.resolve(dict["ToUnicode"]).(*pdfStream); ok {
		if data, err := stream.decoded(f); err == nil {
			font.toUnicode = parseCMap(data)
		}
	}
	if encoding := f.dict(dict["Encoding"]); encoding != nil {
		font.differences = parseDifferences(f, encoding["Differences"])
	}
	font.widths = map[uint32]float64{}
	if font.twoByte {
		font.defaultWidth = 1000
		descendants := f.array(dict["DescendantFonts"])
		if len(descendants) > 0 {
			descendant := f.dict(descendants[0])
			if width := f.number(descendant["DW"]); width > 0 {
				font.defaultWidth = width
			}
			f.readCIDWidths(descendant["W"], font.widths)
		}
	} else {
		font.defaultWidth = 500
		first := uint32(f.int(dict["FirstChar"]))
		for index, value := range f.array(dict["Widths"]) {
			if width := f.number(value); width >= 0 {
				font.widths[first+uint32(index)] = width
			}
		}
		if descriptor := f.dict(dict["FontDescriptor"]); descriptor != nil {
			if missing := f.number(descriptor["MissingWidth"]); missing > 0 {
				font.defaultWidth = missing
			}
		}
	}
	return font
}

// readCIDWidths reads the /W array of a composite font, which mixes two shapes:
// "code [w w w]" and "first last w".
func (f *pdfFile) readCIDWidths(value any, into map[uint32]float64) {
	array := f.array(value)
	for i := 0; i < len(array); {
		start := uint32(f.int(array[i]))
		if i+1 >= len(array) {
			return
		}
		switch next := f.resolve(array[i+1]).(type) {
		case pdfArray:
			for offset, item := range next {
				into[start+uint32(offset)] = f.number(item)
			}
			i += 2
		case float64:
			if i+2 >= len(array) {
				return
			}
			end := uint32(next)
			width := f.number(array[i+2])
			if end >= start && end-start < 65536 {
				for code := start; code <= end; code++ {
					into[code] = width
				}
			}
			i += 3
		default:
			i++
		}
	}
}

func (f *pdfFile) number(value any) float64 {
	if number, ok := f.resolve(value).(float64); ok {
		return number
	}
	return 0
}

// decode turns one PDF string into text and reports how far the pen advanced,
// in thousandths of an em, ignoring the spacing the caller controls.
func (f *pdfFont) decode(raw []byte) (string, float64) {
	var out strings.Builder
	advance := 0.0
	step := 1
	if f.twoByte {
		step = 2
	}
	for i := 0; i+step <= len(raw); i += step {
		var code uint32
		if step == 2 {
			code = uint32(raw[i])<<8 | uint32(raw[i+1])
		} else {
			code = uint32(raw[i])
		}
		advance += f.width(code)
		if f.toUnicode != nil {
			if text, ok := f.toUnicode[code]; ok {
				out.WriteString(text)
				continue
			}
		}
		if step == 1 {
			if f.differences != nil {
				if r, ok := f.differences[byte(code)]; ok {
					out.WriteRune(r)
					continue
				}
			}
			if int(code) < len(winAnsi) && winAnsi[code] != 0 {
				out.WriteRune(winAnsi[code])
				continue
			}
		}
		// A code with no mapping is dropped rather than guessed: a wrong
		// character would be indexed and read aloud as if it were the text.
		out.WriteRune('�')
	}
	return out.String(), advance
}

// parseCMap reads the bfchar and bfrange sections of a ToUnicode CMap.
func parseCMap(data []byte) map[uint32]string {
	mapping := map[uint32]string{}
	lexer := newPDFLexer(data)
	var stack []any
	for lexer.pos < len(lexer.data) {
		before := lexer.pos
		value := lexer.value()
		if lexer.pos == before {
			lexer.pos++
			continue
		}
		operator, isOperator := value.(pdfOperator)
		if !isOperator {
			stack = append(stack, value)
			if len(stack) > 600 {
				stack = stack[len(stack)-600:]
			}
			continue
		}
		switch operator {
		case "endbfchar":
			for i := 0; i+1 < len(stack); i += 2 {
				source, ok1 := stack[i].(pdfString)
				target, ok2 := stack[i+1].(pdfString)
				if ok1 && ok2 {
					mapping[codeOf(source)] = utf16BE(target)
				}
			}
		case "endbfrange":
			for i := 0; i+2 < len(stack); i += 3 {
				low, ok1 := stack[i].(pdfString)
				high, ok2 := stack[i+1].(pdfString)
				if !ok1 || !ok2 {
					continue
				}
				start, end := codeOf(low), codeOf(high)
				if end < start || end-start > 65535 {
					continue
				}
				switch destination := stack[i+2].(type) {
				case pdfString:
					text := utf16BE(destination)
					runes := []rune(text)
					for code := start; code <= end; code++ {
						if len(runes) == 0 {
							break
						}
						next := make([]rune, len(runes))
						copy(next, runes)
						next[len(next)-1] += rune(code - start)
						mapping[code] = string(next)
					}
				case pdfArray:
					for offset, item := range destination {
						if text, ok := item.(pdfString); ok && start+uint32(offset) <= end {
							mapping[start+uint32(offset)] = utf16BE(text)
						}
					}
				}
			}
		}
		stack = stack[:0]
	}
	return mapping
}

func parseDifferences(f *pdfFile, value any) map[byte]rune {
	array := f.array(value)
	if array == nil {
		return nil
	}
	differences := map[byte]rune{}
	code := 0
	for _, item := range array {
		switch entry := item.(type) {
		case float64:
			code = int(entry)
		case pdfName:
			if r := glyphRune(string(entry)); r != 0 && code >= 0 && code < 256 {
				differences[byte(code)] = r
			}
			code++
		}
	}
	return differences
}

// glyphRune maps the handful of glyph names that matter for plain text. A full
// Adobe glyph list is not worth carrying: anything else falls back to the
// encoding table or to the replacement character.
func glyphRune(name string) rune {
	if strings.HasPrefix(name, "uni") && len(name) >= 7 {
		if value, err := strconv.ParseUint(name[3:7], 16, 32); err == nil {
			return rune(value)
		}
	}
	if len(name) == 1 {
		return rune(name[0])
	}
	switch name {
	case "space":
		return ' '
	case "period":
		return '.'
	case "comma":
		return ','
	case "hyphen":
		return '-'
	case "quotesingle":
		return '\''
	case "quotedblleft":
		return '“'
	case "quotedblright":
		return '”'
	}
	return 0
}

func codeOf(raw pdfString) uint32 {
	var code uint32
	for _, b := range raw {
		code = code<<8 | uint32(b)
	}
	return code
}

func utf16BE(raw pdfString) string {
	if len(raw)%2 == 1 {
		raw = append(raw, 0)
	}
	units := make([]uint16, 0, len(raw)/2)
	for i := 0; i+1 < len(raw); i += 2 {
		units = append(units, uint16(raw[i])<<8|uint16(raw[i+1]))
	}
	return string(utf16.Decode(units))
}
