package ingest

import "strings"

// decodeShiftJIS converts Shift_JIS bytes to UTF-8 without a dependency: the
// Aozora Bunko text downloads are still distributed in that encoding, and a
// reading app that cannot open them would miss its main source of books.
//
// Only the mapping needed for Japanese text is implemented: ASCII, half-width
// katakana, and the two-byte planes through the JIS X 0208 table below.
func decodeShiftJIS(raw []byte) string {
	var out strings.Builder
	for i := 0; i < len(raw); i++ {
		b := raw[i]
		switch {
		case b < 0x80:
			out.WriteByte(b)
		case b >= 0xa1 && b <= 0xdf:
			out.WriteRune(halfWidthKatakana[b-0xa1])
		case (b >= 0x81 && b <= 0x9f) || (b >= 0xe0 && b <= 0xfc):
			if i+1 >= len(raw) {
				out.WriteRune('�')
				continue
			}
			second := raw[i+1]
			i++
			if r := shiftJISRune(b, second); r != 0 {
				out.WriteRune(r)
			} else {
				out.WriteRune('�')
			}
		default:
			out.WriteRune('�')
		}
	}
	return out.String()
}

var halfWidthKatakana = []rune("｡｢｣､･ｦｧｨｩｪｫｬｭｮｯｰｱｲｳｴｵｶｷｸｹｺｻｼｽｾｿﾀﾁﾂﾃﾄﾅﾆﾇﾈﾉﾊﾋﾌﾍﾎﾏﾐﾑﾒﾓﾔﾕﾖﾗﾘﾙﾚﾛﾜﾝﾞﾟ")

// shiftJISRune maps a two-byte sequence through the JIS X 0208 table.
func shiftJISRune(first, second byte) rune {
	if second < 0x40 || second == 0x7f || second > 0xfc {
		return 0
	}
	leadOffset := byte(0x81)
	if first >= 0xa0 {
		leadOffset = 0xc1
	}
	trailOffset := byte(0x40)
	if second >= 0x7f {
		trailOffset = 0x41
	}
	pointer := int(first-leadOffset)*188 + int(second-trailOffset)
	if pointer < 0 || pointer >= len(jisX0208) {
		return 0
	}
	return jisX0208[pointer]
}
