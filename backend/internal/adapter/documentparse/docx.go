package documentparse

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/xml"
	"io"
	"strconv"
	"strings"

	resourceapp "mathstudy/backend/internal/application/resource"
)

func parseDOCX(ctx context.Context, data []byte) ([]resourceapp.DocumentBlock, error) {
	if bytes.HasPrefix(data, []byte{0xd0, 0xcf, 0x11, 0xe0, 0xa1, 0xb1, 0x1a, 0xe1}) {
		return nil, parseError("DOCUMENT_ENCRYPTED")
	}
	archive, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil || len(archive.File) > 4096 {
		return nil, parseError("DOCUMENT_INVALID")
	}
	files := make(map[string]*zip.File, len(archive.File))
	for _, file := range archive.File {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		if files[file.Name] != nil || file.Flags&1 != 0 {
			return nil, parseError("DOCUMENT_INVALID")
		}
		files[file.Name] = file
	}
	if files["[Content_Types].xml"] == nil || files["word/document.xml"] == nil {
		return nil, parseError("DOCUMENT_INVALID")
	}
	if metadata := files["docProps/app.xml"]; metadata != nil {
		data, err := readDOCXPart(ctx, metadata)
		if err != nil {
			return nil, err
		}
		decoder := xml.NewDecoder(bytes.NewReader(data))
		for {
			token, err := decoder.Token()
			if err == io.EOF {
				break
			}
			if err != nil {
				return nil, parseError("DOCUMENT_INVALID")
			}
			if _, directive := token.(xml.Directive); directive {
				return nil, parseError("DOCUMENT_INVALID")
			}
			if start, ok := token.(xml.StartElement); ok && start.Name.Local == "Pages" {
				var value int
				if err := decoder.DecodeElement(&value, &start); err != nil || value < 0 {
					return nil, parseError("DOCUMENT_INVALID")
				}
				if value > MaxPages {
					return nil, parseError("DOCUMENT_PAGE_LIMIT")
				}
			}
		}
	}
	document, err := readDOCXPart(ctx, files["word/document.xml"])
	if err != nil {
		return nil, err
	}
	return parseWordXML(ctx, document)
}

func readDOCXPart(ctx context.Context, file *zip.File) ([]byte, error) {
	if file.UncompressedSize64 > MaxXMLBytes || file.UncompressedSize64 > max(file.CompressedSize64, 1)*1000 {
		return nil, parseError("DOCUMENT_CHARACTER_LIMIT")
	}
	reader, err := file.Open()
	if err != nil {
		return nil, parseError("DOCUMENT_INVALID")
	}
	defer reader.Close()
	data, err := io.ReadAll(io.LimitReader(contextReader{ctx: ctx, reader: reader}, MaxXMLBytes+1))
	if ctx.Err() != nil {
		return nil, ctx.Err()
	}
	if err != nil {
		return nil, parseError("DOCUMENT_INVALID")
	}
	if len(data) > MaxXMLBytes {
		return nil, parseError("DOCUMENT_CHARACTER_LIMIT")
	}
	return data, nil
}

type wordTableRow struct {
	cells   []string
	current []string
}

func parseWordXML(ctx context.Context, data []byte) ([]resourceapp.DocumentBlock, error) {
	decoder := xml.NewDecoder(bytes.NewReader(data))
	blocks := make([]resourceapp.DocumentBlock, 0)
	var headings [6]string
	var rows []*wordTableRow
	section, foundDocument, depth, pageBreaks := "", false, 0, 0
	for {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		token, err := decoder.Token()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, parseError("DOCUMENT_INVALID")
		}
		switch element := token.(type) {
		case xml.Directive:
			return nil, parseError("DOCUMENT_INVALID")
		case xml.StartElement:
			depth++
			if depth > 256 {
				return nil, parseError("DOCUMENT_INVALID")
			}
			if !wordName(element.Name) {
				continue
			}
			switch element.Name.Local {
			case "document":
				foundDocument = true
			case "tr":
				rows = append(rows, &wordTableRow{})
			case "p":
				text, level, breaks, err := wordParagraph(ctx, decoder)
				depth--
				if err != nil {
					return nil, err
				}
				pageBreaks += breaks
				if pageBreaks >= MaxPages {
					return nil, parseError("DOCUMENT_PAGE_LIMIT")
				}
				if len(rows) > 0 {
					rows[len(rows)-1].current = append(rows[len(rows)-1].current, text)
					continue
				}
				kind := "paragraph"
				if level > 0 && strings.TrimSpace(text) != "" {
					kind = "heading"
					headings[level-1] = strings.TrimSpace(text)
					for i := level; i < len(headings); i++ {
						headings[i] = ""
					}
					section = headingPath(headings[:])
				}
				blocks = append(blocks, resourceapp.DocumentBlock{Kind: kind, Text: text, SectionPath: section})
			}
		case xml.EndElement:
			depth--
			if !wordName(element.Name) || len(rows) == 0 {
				continue
			}
			row := rows[len(rows)-1]
			switch element.Name.Local {
			case "tc":
				row.cells = append(row.cells, strings.Join(row.current, "\n"))
				row.current = nil
			case "tr":
				rows = rows[:len(rows)-1]
				text := strings.Join(row.cells, "\t")
				if len(rows) > 0 {
					rows[len(rows)-1].current = append(rows[len(rows)-1].current, text)
				} else {
					blocks = append(blocks, resourceapp.DocumentBlock{Kind: "table", Text: text, SectionPath: section})
				}
			}
		}
		if len(blocks) > MaxBlocks {
			return nil, parseError("DOCUMENT_BLOCK_LIMIT")
		}
	}
	if !foundDocument || depth != 0 || len(rows) != 0 {
		return nil, parseError("DOCUMENT_INVALID")
	}
	return blocks, nil
}

func wordName(name xml.Name) bool {
	return name.Space == "http://schemas.openxmlformats.org/wordprocessingml/2006/main" || name.Space == "http://purl.oclc.org/ooxml/wordprocessingml/main"
}

func wordParagraph(ctx context.Context, decoder *xml.Decoder) (string, int, int, error) {
	var text strings.Builder
	depth, level, pageBreaks := 1, 0, 0
	for depth > 0 {
		if err := ctx.Err(); err != nil {
			return "", 0, 0, err
		}
		token, err := decoder.Token()
		if err != nil {
			return "", 0, 0, parseError("DOCUMENT_INVALID")
		}
		switch element := token.(type) {
		case xml.Directive:
			return "", 0, 0, parseError("DOCUMENT_INVALID")
		case xml.StartElement:
			depth++
			if depth > 256 {
				return "", 0, 0, parseError("DOCUMENT_INVALID")
			}
			if mathName(element.Name) {
				value, err := wordMath(ctx, decoder, element, depth)
				if err != nil {
					return "", 0, 0, err
				}
				depth--
				text.WriteString(value)
				continue
			}
			if !wordName(element.Name) {
				continue
			}
			switch element.Name.Local {
			case "t":
				var value string
				if err := decoder.DecodeElement(&value, &element); err != nil {
					return "", 0, 0, parseError("DOCUMENT_INVALID")
				}
				depth--
				text.WriteString(value)
			case "tab":
				text.WriteByte('\t')
			case "br", "cr", "lastRenderedPageBreak":
				text.WriteByte('\n')
				if element.Name.Local == "lastRenderedPageBreak" || wordAttribute(element.Attr, "type") == "page" {
					pageBreaks++
				}
			case "pStyle":
				style := strings.ToLower(wordAttribute(element.Attr, "val"))
				if style == "title" {
					level = 1
				} else if strings.HasPrefix(style, "heading") {
					value, _ := strconv.Atoi(strings.TrimSpace(strings.TrimPrefix(style, "heading")))
					if value >= 1 && value <= 6 {
						level = value
					}
				}
			case "outlineLvl":
				value, err := strconv.Atoi(wordAttribute(element.Attr, "val"))
				if err == nil && value >= 0 && value < 6 {
					level = value + 1
				}
			}
		case xml.EndElement:
			depth--
		}
		if text.Len() > MaxCharacters*4 {
			return "", 0, 0, parseError("DOCUMENT_CHARACTER_LIMIT")
		}
	}
	return text.String(), level, pageBreaks, nil
}

func wordAttribute(attributes []xml.Attr, name string) string {
	for _, attribute := range attributes {
		if attribute.Name.Local == name && wordName(attribute.Name) {
			return attribute.Value
		}
	}
	return ""
}
