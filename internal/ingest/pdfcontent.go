package ingest

import (
	"math"
	"strings"
)

// The content stream interpreter. It tracks only what decides where text goes:
// the text matrix, the font, and the kerning inside a TJ array. Everything else
// a PDF can express - colour, clipping, graphics - has no bearing on reading.
type textState struct {
	font       *pdfFont
	fontSize   float64
	leading    float64
	charSpace  float64
	wordSpace  float64
	horizontal float64
	x, y       float64
	lineX      float64
	lineY      float64
}

func (f *pdfFile) pageText(page pdfDict) string {
	fonts := map[pdfName]*pdfFont{}
	if resources := f.dict(page["Resources"]); resources != nil {
		for name, value := range f.dict(resources["Font"]) {
			if dict := f.dict(value); dict != nil {
				fonts[name] = f.font(dict)
			}
		}
	}

	var content []byte
	switch value := f.resolve(page["Contents"]).(type) {
	case *pdfStream:
		if data, err := value.decoded(f); err == nil {
			content = data
		}
	case pdfArray:
		for _, item := range value {
			if stream, ok := f.resolve(item).(*pdfStream); ok {
				if data, err := stream.decoded(f); err == nil {
					content = append(content, data...)
					content = append(content, '\n')
				}
			}
		}
	}
	if len(content) == 0 {
		return ""
	}
	return runContent(content, fonts)
}

func runContent(content []byte, fonts map[pdfName]*pdfFont) string {
	lexer := newPDFLexer(content)
	var operands []any
	var out strings.Builder
	state := textState{horizontal: 1}
	lastY := math.NaN()
	lastX := math.NaN()
	pendingSpace := false
	written := func() rune {
		if out.Len() == 0 {
			return '\n'
		}
		runes := []rune(out.String())
		return runes[len(runes)-1]
	}
	space := func() {
		if last := written(); last != ' ' && last != '\n' && last != '\u3000' {
			out.WriteString(" ")
		}
	}

	number := func(index int) float64 {
		if index < 0 || index >= len(operands) {
			return 0
		}
		if value, ok := operands[index].(float64); ok {
			return value
		}
		return 0
	}
	// newline decides where a line ends: a vertical move of more than a third of
	// the font size is a new line, and a bigger gap is a paragraph break.
	newline := func() {
		if math.IsNaN(lastY) {
			return
		}
		gap := math.Abs(state.y - lastY)
		switch {
		case gap > math.Max(state.fontSize, state.leading)*1.6:
			out.WriteString("\n\n")
		case gap > math.Max(state.fontSize, 1)*0.35:
			out.WriteString("\n")
		}
	}
	show := func(raw pdfString) {
		if state.font == nil {
			return
		}
		text, advance := state.font.decode(raw)
		if text == "" {
			return
		}
		if !math.IsNaN(lastY) && math.Abs(state.y-lastY) > math.Max(state.fontSize, 1)*0.35 {
			newline()
		} else if pendingSpace || (!math.IsNaN(lastX) && state.x-lastX > state.fontSize*0.28) {
			// A kerning gap, or a horizontal jump inside a line, is usually a
			// space the writer did not encode - but Japanese is set without
			// spaces and justified by exactly these gaps, so a gap between two
			// Japanese characters is not one.
			if !isCJKRune(written()) && !isCJKRune([]rune(text)[0]) {
				space()
			}
		}
		pendingSpace = false
		out.WriteString(text)
		lastY = state.y
		// The pen has moved on by the glyphs just drawn, plus the spacing the
		// content stream asked for; only a gap beyond that is a real space.
		state.x += (advance/1000*state.fontSize + float64(len([]rune(text)))*state.charSpace +
			float64(strings.Count(text, " "))*state.wordSpace) * state.horizontal
		lastX = state.x
	}

	for lexer.pos < len(lexer.data) {
		before := lexer.pos
		value := lexer.value()
		if lexer.pos == before {
			lexer.pos++
			continue
		}
		operator, isOperator := value.(pdfOperator)
		if !isOperator {
			operands = append(operands, value)
			if len(operands) > 64 {
				operands = operands[len(operands)-64:]
			}
			continue
		}
		switch operator {
		case "BT":
			state.x, state.y, state.lineX, state.lineY = 0, 0, 0, 0
		case "ET":
			lastX = math.NaN()
		case "Tf":
			if len(operands) >= 2 {
				if name, ok := operands[len(operands)-2].(pdfName); ok {
					state.font = fonts[name]
				}
				state.fontSize = number(len(operands) - 1)
			}
		case "TL":
			state.leading = number(len(operands) - 1)
		case "Tc":
			state.charSpace = number(len(operands) - 1)
		case "Tw":
			state.wordSpace = number(len(operands) - 1)
		case "Tz":
			state.horizontal = number(len(operands)-1) / 100
		case "Td":
			state.lineX += number(len(operands) - 2)
			state.lineY += number(len(operands) - 1)
			state.x, state.y = state.lineX, state.lineY
		case "TD":
			state.leading = -number(len(operands) - 1)
			state.lineX += number(len(operands) - 2)
			state.lineY += number(len(operands) - 1)
			state.x, state.y = state.lineX, state.lineY
		case "Tm":
			state.lineX = number(len(operands) - 2)
			state.lineY = number(len(operands) - 1)
			state.x, state.y = state.lineX, state.lineY
		case "T*":
			state.lineY -= state.leading
			state.x, state.y = state.lineX, state.lineY
		case "Tj":
			if len(operands) > 0 {
				if raw, ok := operands[len(operands)-1].(pdfString); ok {
					show(raw)
				}
			}
		case "'":
			state.lineY -= state.leading
			state.x, state.y = state.lineX, state.lineY
			if len(operands) > 0 {
				if raw, ok := operands[len(operands)-1].(pdfString); ok {
					show(raw)
				}
			}
		case "\"":
			state.wordSpace = number(len(operands) - 3)
			state.charSpace = number(len(operands) - 2)
			state.lineY -= state.leading
			state.x, state.y = state.lineX, state.lineY
			if len(operands) > 0 {
				if raw, ok := operands[len(operands)-1].(pdfString); ok {
					show(raw)
				}
			}
		case "TJ":
			if len(operands) == 0 {
				break
			}
			array, ok := operands[len(operands)-1].(pdfArray)
			if !ok {
				break
			}
			for _, item := range array {
				switch element := item.(type) {
				case pdfString:
					show(element)
				case float64:
					// Kerning in thousandths of an em. A large negative value is
					// how most writers encode a space between words.
					if element < -180 {
						pendingSpace = true
					}
					state.x -= element / 1000 * state.fontSize * state.horizontal
				}
			}
		}
		operands = operands[:0]
	}
	return out.String()
}
