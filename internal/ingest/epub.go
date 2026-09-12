package ingest

import (
	"archive/zip"
	"encoding/xml"
	"fmt"
	"io"
	"path"
	"strings"

	"github.com/takeshy/dokking/internal/book"
)

// ParseEPUB reads the spine in order and turns each document into a section.
// EPUB already carries the structure this app needs, so it is a first-class
// format: no guessing where a chapter starts.
func ParseEPUB(path string) (*book.Book, error) {
	reader, err := zip.OpenReader(path)
	if err != nil {
		return nil, fmt.Errorf("open epub: %w", err)
	}
	defer reader.Close()
	return parseEPUBArchive(&reader.Reader)
}

func parseEPUBArchive(archive *zip.Reader) (*book.Book, error) {
	opfPath, err := epubRootfile(archive)
	if err != nil {
		return nil, err
	}
	opf, err := readZipFile(archive, opfPath)
	if err != nil {
		return nil, err
	}
	var pkg struct {
		Metadata struct {
			Title   []string `xml:"title"`
			Creator []string `xml:"creator"`
		} `xml:"metadata"`
		Manifest struct {
			Items []struct {
				ID   string `xml:"id,attr"`
				Href string `xml:"href,attr"`
				Type string `xml:"media-type,attr"`
			} `xml:"item"`
		} `xml:"manifest"`
		Spine struct {
			Items []struct {
				IDRef  string `xml:"idref,attr"`
				Linear string `xml:"linear,attr"`
			} `xml:"itemref"`
		} `xml:"spine"`
	}
	if err := xml.Unmarshal(opf, &pkg); err != nil {
		return nil, fmt.Errorf("parse opf: %w", err)
	}
	hrefByID := map[string]string{}
	for _, item := range pkg.Manifest.Items {
		hrefByID[item.ID] = item.Href
	}

	var b builder
	base := path2dir(opfPath)
	for _, ref := range pkg.Spine.Items {
		if strings.EqualFold(ref.Linear, "no") {
			continue
		}
		href, ok := hrefByID[ref.IDRef]
		if !ok {
			continue
		}
		document, err := readZipFile(archive, joinZipPath(base, href))
		if err != nil {
			continue
		}
		title, blocks := htmlBlocks(string(document))
		if len(blocks) == 0 {
			continue
		}
		b.startSection(title, 1)
		for _, block := range blocks {
			b.addBlock(block.kind, block.text, nil, nil, "")
		}
	}
	sections, end := b.finish()
	if len(sections) == 0 {
		return nil, fmt.Errorf("epub has no readable text")
	}
	result := &book.Book{
		Format:     book.FormatEPUB,
		Sections:   sections,
		End:        end,
		ContentEnd: contentEnd(sections, end),
	}
	if len(pkg.Metadata.Title) > 0 {
		result.Title = strings.TrimSpace(pkg.Metadata.Title[0])
	}
	if len(pkg.Metadata.Creator) > 0 {
		result.Author = strings.TrimSpace(pkg.Metadata.Creator[0])
	}
	return result, nil
}

func epubRootfile(archive *zip.Reader) (string, error) {
	container, err := readZipFile(archive, "META-INF/container.xml")
	if err != nil {
		return "", err
	}
	var parsed struct {
		Rootfiles []struct {
			FullPath string `xml:"full-path,attr"`
		} `xml:"rootfiles>rootfile"`
	}
	if err := xml.Unmarshal(container, &parsed); err != nil {
		return "", fmt.Errorf("parse container.xml: %w", err)
	}
	if len(parsed.Rootfiles) == 0 || parsed.Rootfiles[0].FullPath == "" {
		return "", fmt.Errorf("epub container names no package document")
	}
	return parsed.Rootfiles[0].FullPath, nil
}

func readZipFile(archive *zip.Reader, name string) ([]byte, error) {
	for _, file := range archive.File {
		if file.Name == name {
			handle, err := file.Open()
			if err != nil {
				return nil, err
			}
			defer handle.Close()
			return io.ReadAll(handle)
		}
	}
	return nil, fmt.Errorf("%s is missing from the epub", name)
}

func path2dir(p string) string {
	if index := strings.LastIndex(p, "/"); index >= 0 {
		return p[:index]
	}
	return ""
}

func joinZipPath(base, href string) string {
	if index := strings.IndexAny(href, "#?"); index >= 0 {
		href = href[:index]
	}
	if base == "" {
		return path.Clean(href)
	}
	return path.Clean(base + "/" + href)
}

type htmlBlock struct {
	kind book.BlockKind
	text string
}

var blockTags = map[string]book.BlockKind{
	"p": book.BlockParagraph, "div": book.BlockParagraph, "li": book.BlockParagraph,
	"blockquote": book.BlockParagraph, "br": book.BlockParagraph, "tr": book.BlockParagraph,
	"h1": book.BlockHeading, "h2": book.BlockHeading, "h3": book.BlockHeading,
	"h4": book.BlockHeading, "h5": book.BlockHeading, "h6": book.BlockHeading,
}

// htmlBlocks strips markup into blocks. A full HTML parser is not needed here:
// the only questions are where a block ends and what the heading says.
func htmlBlocks(document string) (title string, blocks []htmlBlock) {
	runes := []rune(document)
	var current []rune
	kind := book.BlockParagraph
	flush := func(next book.BlockKind) {
		text := strings.TrimSpace(decodeEntities(collapseSpaces(string(current))))
		current = current[:0]
		if text != "" {
			blocks = append(blocks, htmlBlock{kind: kind, text: text})
			if title == "" && kind == book.BlockHeading {
				title = text
			}
		}
		kind = next
	}
	skip := ""
	for i := 0; i < len(runes); i++ {
		if runes[i] != '<' {
			if skip == "" {
				current = append(current, runes[i])
			}
			continue
		}
		end := findRune(runes, i, '>')
		if end < 0 {
			break
		}
		tag := strings.ToLower(strings.TrimSpace(string(runes[i+1 : end])))
		i = end
		closing := strings.HasPrefix(tag, "/")
		name := strings.TrimPrefix(tag, "/")
		if cut := strings.IndexAny(name, " \t\n/"); cut >= 0 {
			name = name[:cut]
		}
		switch name {
		case "script", "style", "head":
			if closing {
				skip = ""
			} else {
				skip = name
			}
			continue
		}
		if skip != "" {
			continue
		}
		if next, ok := blockTags[name]; ok {
			if closing {
				flush(book.BlockParagraph)
			} else {
				flush(next)
			}
		}
	}
	flush(book.BlockParagraph)
	return title, blocks
}

func collapseSpaces(text string) string {
	var out []rune
	space := false
	for _, r := range text {
		if r == ' ' || r == '\t' || r == '\n' || r == '\r' || r == '　' {
			space = true
			continue
		}
		if space && len(out) > 0 {
			out = append(out, ' ')
		}
		space = false
		out = append(out, r)
	}
	return string(out)
}

var entities = strings.NewReplacer(
	"&nbsp;", " ", "&amp;", "&", "&lt;", "<", "&gt;", ">", "&quot;", `"`,
	"&apos;", "'", "&#39;", "'", "&#8217;", "’", "&#8216;", "‘",
	"&#8220;", "“", "&#8221;", "”", "&mdash;", "—", "&ndash;", "–", "&hellip;", "…",
)

func decodeEntities(text string) string { return entities.Replace(text) }
