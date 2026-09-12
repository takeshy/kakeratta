package ingest

import (
	"bytes"
	"compress/zlib"
	"encoding/hex"
	"io"
	"regexp"
	"strconv"
)

// A small PDF object model. Only what text extraction needs is implemented:
// dictionaries, arrays, names, numbers, strings, references and streams.
type (
	pdfName   string
	pdfRef    struct{ number, generation int }
	pdfDict   map[pdfName]any
	pdfArray  []any
	pdfString []byte
	pdfStream struct {
		dict pdfDict
		raw  []byte
	}
)

// pdfFile is the set of objects in a document, addressed by number.
//
// The cross-reference table is deliberately not parsed. A real-world PDF may
// have a broken xref, an incremental update, or a cross-reference stream; every
// one of those still has the objects themselves in the file, so they are found
// by scanning. Text extraction cares about the objects, not the index.
type pdfFile struct {
	objects map[int][]byte
	parsed  map[int]any
}

var objectPattern = regexp.MustCompile(`(?s)(\d+)\s+(\d+)\s+obj\b`)

func newPDFFile(raw []byte) *pdfFile {
	file := &pdfFile{objects: map[int][]byte{}, parsed: map[int]any{}}
	for _, match := range objectPattern.FindAllSubmatchIndex(raw, -1) {
		number, err := strconv.Atoi(string(raw[match[2]:match[3]]))
		if err != nil {
			continue
		}
		body := raw[match[1]:]
		if end := bytes.Index(body, []byte("endobj")); end >= 0 {
			body = body[:end]
		}
		file.objects[number] = body
	}
	file.expandObjectStreams()
	return file
}

// expandObjectStreams pulls the objects that PDF 1.5+ packs inside compressed
// streams into the same map, so the rest of the code never has to care which
// kind of file it is reading.
func (f *pdfFile) expandObjectStreams() {
	for number := range f.objects {
		value := f.object(number)
		stream, ok := value.(*pdfStream)
		if !ok || stream.dict.name("Type") != "ObjStm" {
			continue
		}
		data, err := stream.decoded(f)
		if err != nil {
			continue
		}
		count := f.int(stream.dict["N"])
		first := f.int(stream.dict["First"])
		if count <= 0 || first <= 0 || first > len(data) {
			continue
		}
		header := newPDFLexer(data[:first])
		for i := 0; i < count; i++ {
			id, ok1 := header.number()
			offset, ok2 := header.number()
			if !ok1 || !ok2 {
				break
			}
			start := first + int(offset)
			if start < 0 || start >= len(data) {
				continue
			}
			if _, exists := f.objects[int(id)]; exists {
				continue
			}
			f.objects[int(id)] = data[start:]
		}
	}
}

// object parses an object by number, caching the result.
func (f *pdfFile) object(number int) any {
	if value, ok := f.parsed[number]; ok {
		return value
	}
	body, ok := f.objects[number]
	if !ok {
		return nil
	}
	f.parsed[number] = nil // guard against a reference cycle while parsing
	lexer := newPDFLexer(body)
	value := lexer.value()
	if dict, ok := value.(pdfDict); ok {
		if raw, found := lexer.streamData(); found {
			value = &pdfStream{dict: dict, raw: raw}
		}
	}
	f.parsed[number] = value
	return value
}

// resolve follows references until a real value is reached.
func (f *pdfFile) resolve(value any) any {
	for i := 0; i < 32; i++ {
		ref, ok := value.(pdfRef)
		if !ok {
			return value
		}
		value = f.object(ref.number)
	}
	return value
}

func (f *pdfFile) dict(value any) pdfDict {
	switch resolved := f.resolve(value).(type) {
	case pdfDict:
		return resolved
	case *pdfStream:
		return resolved.dict
	}
	return nil
}

func (f *pdfFile) array(value any) pdfArray {
	if resolved, ok := f.resolve(value).(pdfArray); ok {
		return resolved
	}
	return nil
}

func (f *pdfFile) int(value any) int {
	if number, ok := f.resolve(value).(float64); ok {
		return int(number)
	}
	return 0
}

func (d pdfDict) name(key pdfName) pdfName {
	if value, ok := d[key].(pdfName); ok {
		return value
	}
	return ""
}

// decoded applies the stream filters. Only the ones that carry text are
// supported; an image filter simply yields no text.
func (s *pdfStream) decoded(f *pdfFile) ([]byte, error) {
	data := s.raw
	if length := f.int(s.dict["Length"]); length > 0 && length <= len(data) {
		data = data[:length]
	}
	var filters []pdfName
	switch filter := f.resolve(s.dict["Filter"]).(type) {
	case pdfName:
		filters = append(filters, filter)
	case pdfArray:
		for _, item := range filter {
			if name, ok := f.resolve(item).(pdfName); ok {
				filters = append(filters, name)
			}
		}
	}
	for _, filter := range filters {
		var err error
		switch filter {
		case "FlateDecode", "Fl":
			data, err = inflate(data)
		case "ASCIIHexDecode", "AHx":
			data = decodeASCIIHex(data)
		case "ASCII85Decode", "A85":
			data = decodeASCII85(data)
		default:
			// An unsupported filter (an image codec, say) has no text in it.
			return nil, io.EOF
		}
		if err != nil {
			return nil, err
		}
	}
	if parms := f.dict(s.dict["DecodeParms"]); parms != nil {
		if predictor := f.int(parms["Predictor"]); predictor >= 10 {
			columns := f.int(parms["Columns"])
			if columns <= 0 {
				columns = 1
			}
			colors := f.int(parms["Colors"])
			if colors <= 0 {
				colors = 1
			}
			bits := f.int(parms["BitsPerComponent"])
			if bits <= 0 {
				bits = 8
			}
			data = undoPNGPredictor(data, columns*colors*bits/8)
		}
	}
	return data, nil
}

func inflate(data []byte) ([]byte, error) {
	// Some writers leave junk before the zlib header; skip to it if needed.
	reader, err := zlib.NewReader(bytes.NewReader(data))
	if err != nil {
		if index := bytes.IndexByte(data, 0x78); index > 0 {
			reader, err = zlib.NewReader(bytes.NewReader(data[index:]))
		}
		if err != nil {
			return nil, err
		}
	}
	defer reader.Close()
	// A truncated stream still yields the bytes read so far, which is enough to
	// keep extracting text from a slightly damaged file.
	out, readErr := io.ReadAll(reader)
	if readErr != nil && len(out) == 0 {
		return nil, readErr
	}
	return out, nil
}

func decodeASCIIHex(data []byte) []byte {
	var cleaned []byte
	for _, b := range data {
		if b == '>' {
			break
		}
		if isHexDigit(b) {
			cleaned = append(cleaned, b)
		}
	}
	if len(cleaned)%2 == 1 {
		cleaned = append(cleaned, '0')
	}
	out := make([]byte, hex.DecodedLen(len(cleaned)))
	if _, err := hex.Decode(out, cleaned); err != nil {
		return nil
	}
	return out
}

func decodeASCII85(data []byte) []byte {
	var out []byte
	var group [5]byte
	count := 0
	for i := 0; i < len(data); i++ {
		b := data[i]
		switch {
		case b == '~':
			i = len(data)
		case b == 'z' && count == 0:
			out = append(out, 0, 0, 0, 0)
		case b >= '!' && b <= 'u':
			group[count] = b - '!'
			count++
			if count == 5 {
				value := uint32(0)
				for _, digit := range group {
					value = value*85 + uint32(digit)
				}
				out = append(out, byte(value>>24), byte(value>>16), byte(value>>8), byte(value))
				count = 0
			}
		}
	}
	if count > 0 {
		for i := count; i < 5; i++ {
			group[i] = 84
		}
		value := uint32(0)
		for _, digit := range group {
			value = value*85 + uint32(digit)
		}
		full := []byte{byte(value >> 24), byte(value >> 16), byte(value >> 8), byte(value)}
		out = append(out, full[:count-1]...)
	}
	return out
}

func undoPNGPredictor(data []byte, rowLength int) []byte {
	if rowLength <= 0 {
		return data
	}
	var out []byte
	previous := make([]byte, rowLength)
	for offset := 0; offset+1 <= len(data); offset += rowLength + 1 {
		tag := data[offset]
		end := offset + 1 + rowLength
		if end > len(data) {
			end = len(data)
		}
		row := make([]byte, rowLength)
		copy(row, data[offset+1:end])
		switch tag {
		case 1:
			for i := 1; i < rowLength; i++ {
				row[i] += row[i-1]
			}
		case 2:
			for i := 0; i < rowLength; i++ {
				row[i] += previous[i]
			}
		case 3:
			for i := 0; i < rowLength; i++ {
				left := byte(0)
				if i > 0 {
					left = row[i-1]
				}
				row[i] += byte((int(left) + int(previous[i])) / 2)
			}
		case 4:
			for i := 0; i < rowLength; i++ {
				left, upperLeft := byte(0), byte(0)
				if i > 0 {
					left, upperLeft = row[i-1], previous[i-1]
				}
				row[i] += paeth(left, previous[i], upperLeft)
			}
		}
		out = append(out, row...)
		previous = row
	}
	return out
}

func paeth(a, b, c byte) byte {
	p := int(a) + int(b) - int(c)
	pa, pb, pc := abs(p-int(a)), abs(p-int(b)), abs(p-int(c))
	switch {
	case pa <= pb && pa <= pc:
		return a
	case pb <= pc:
		return b
	default:
		return c
	}
}

func abs(value int) int {
	if value < 0 {
		return -value
	}
	return value
}

func isHexDigit(b byte) bool {
	return (b >= '0' && b <= '9') || (b >= 'a' && b <= 'f') || (b >= 'A' && b <= 'F')
}
