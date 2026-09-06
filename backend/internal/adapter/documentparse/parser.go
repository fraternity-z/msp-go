package documentparse

import (
	"bytes"
	"context"
	"errors"
	"io"
	"mime"
	"path"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"golang.org/x/text/unicode/norm"

	resourceapp "mathstudy/backend/internal/application/resource"
)

const (
	MaxBytes      = 50 << 20
	MaxPages      = 200
	MaxCharacters = 2_000_000
	MaxBlocks     = 50_000
	ParseTimeout  = 120 * time.Second
	MaxXMLBytes   = 16 << 20
)

type Config struct {
	PDFInfoPath   string
	PDFToTextPath string
}

type Parser struct {
	pdfInfoPath   string
	pdfToTextPath string
}

// Error exposes a stable failure reason without source text or process output.
type Error struct {
	Code      string
	Retryable bool
}

func (e *Error) Error() string { return "resource document parsing: " + e.Code }

func (e *Error) IngestionFailure() (string, bool) { return e.Code, e.Retryable }

func (e *Error) Is(target error) bool {
	return target == resourceapp.ErrParseFailed || target == resourceapp.ErrObjectUnsupported && e.Code == "OBJECT_UNSUPPORTED"
}

func New(config Config) (*Parser, error) {
	info, text := strings.TrimSpace(config.PDFInfoPath), strings.TrimSpace(config.PDFToTextPath)
	if strings.ContainsAny(info+text, "\x00\r\n") {
		return nil, errors.New("invalid PDF executable configuration")
	}
	if info == "" {
		info = "pdfinfo"
	}
	if text == "" {
		text = "pdftotext"
	}
	return &Parser{pdfInfoPath: info, pdfToTextPath: text}, nil
}

func (p *Parser) Parse(ctx context.Context, input resourceapp.ParseInput) (resourceapp.ParsedDocument, error) {
	if err := ctx.Err(); err != nil {
		return resourceapp.ParsedDocument{}, err
	}
	if p == nil || input.Reader == nil {
		return resourceapp.ParsedDocument{}, parseError("DOCUMENT_INVALID")
	}
	ctx, cancel := context.WithTimeout(ctx, ParseTimeout)
	defer cancel()
	contentType, _, err := mime.ParseMediaType(input.Metadata.MIMEType)
	if err != nil {
		return resourceapp.ParsedDocument{}, parseError("OBJECT_UNSUPPORTED")
	}
	contentType = strings.ToLower(contentType)
	if contentType != "application/pdf" && contentType != "application/vnd.openxmlformats-officedocument.wordprocessingml.document" && contentType != "text/plain" && contentType != "text/markdown" {
		return resourceapp.ParsedDocument{}, parseError("OBJECT_UNSUPPORTED")
	}
	if input.Metadata.ByteSize < 0 || input.Metadata.ByteSize > MaxBytes {
		return resourceapp.ParsedDocument{}, parseError("OBJECT_TOO_LARGE")
	}
	data, err := io.ReadAll(io.LimitReader(contextReader{ctx: ctx, reader: input.Reader}, MaxBytes+1))
	if err != nil {
		if ctx.Err() != nil {
			return resourceapp.ParsedDocument{}, ctx.Err()
		}
		return resourceapp.ParsedDocument{}, &Error{Code: "OBJECT_READ_FAILED", Retryable: true}
	}
	if len(data) > MaxBytes {
		return resourceapp.ParsedDocument{}, parseError("OBJECT_TOO_LARGE")
	}
	if len(data) == 0 {
		return resourceapp.ParsedDocument{}, parseError("DOCUMENT_EMPTY")
	}
	var blocks []resourceapp.DocumentBlock
	switch contentType {
	case "application/pdf":
		blocks, err = p.parsePDF(ctx, data)
	case "application/vnd.openxmlformats-officedocument.wordprocessingml.document":
		blocks, err = parseDOCX(ctx, data)
	default:
		blocks, err = parseText(ctx, data, contentType == "text/markdown", 0)
	}
	if ctx.Err() != nil {
		return resourceapp.ParsedDocument{}, ctx.Err()
	}
	if err != nil {
		return resourceapp.ParsedDocument{}, err
	}
	return normalizedDocument(ctx, blocks, input.Metadata.Filename)
}

type contextReader struct {
	ctx    context.Context
	reader io.Reader
}

func (r contextReader) Read(p []byte) (int, error) {
	if err := r.ctx.Err(); err != nil {
		return 0, err
	}
	return r.reader.Read(p)
}

func parseError(code string) error { return &Error{Code: code} }

func normalizeText(value string) (string, error) {
	if !utf8.ValidString(value) || strings.IndexFunc(value, func(r rune) bool { return unicode.IsControl(r) && r != '\t' && r != '\n' && r != '\r' && r != '\f' }) >= 0 {
		return "", parseError("DOCUMENT_INVALID")
	}
	value = strings.TrimPrefix(value, "\ufeff")
	value = strings.ReplaceAll(strings.ReplaceAll(value, "\r\n", "\n"), "\r", "\n")
	value = strings.ReplaceAll(value, "\f", "\n\n")
	lines := strings.Split(norm.NFC.String(value), "\n")
	for i := range lines {
		lines[i] = strings.TrimRight(lines[i], " \t")
	}
	return strings.TrimSpace(strings.Join(lines, "\n")), nil
}

func normalizedDocument(ctx context.Context, blocks []resourceapp.DocumentBlock, filename string) (resourceapp.ParsedDocument, error) {
	if len(blocks) > MaxBlocks {
		return resourceapp.ParsedDocument{}, parseError("DOCUMENT_BLOCK_LIMIT")
	}
	document := resourceapp.ParsedDocument{Blocks: make([]resourceapp.DocumentBlock, 0, len(blocks))}
	characters, han, latin := 0, 0, 0
	for _, block := range blocks {
		if err := ctx.Err(); err != nil {
			return resourceapp.ParsedDocument{}, err
		}
		text, err := normalizeText(block.Text)
		if err != nil {
			return resourceapp.ParsedDocument{}, err
		}
		if text == "" {
			continue
		}
		section, err := normalizeText(block.SectionPath)
		if err != nil || utf8.RuneCountInString(section) > 1000 || block.Page < 0 || block.Page > MaxPages {
			return resourceapp.ParsedDocument{}, parseError("DOCUMENT_INVALID")
		}
		block.Text, block.SectionPath = text, section
		if block.Kind == "" {
			block.Kind = "paragraph"
		}
		characters += utf8.RuneCountInString(text)
		if len(document.Blocks) > 0 {
			characters += 2
		}
		if characters > MaxCharacters {
			return resourceapp.ParsedDocument{}, parseError("DOCUMENT_CHARACTER_LIMIT")
		}
		for _, r := range text {
			if unicode.Is(unicode.Han, r) {
				han++
			} else if unicode.Is(unicode.Latin, r) {
				latin++
			}
		}
		if document.Title == "" && block.Kind == "heading" {
			document.Title = boundedText(text, 200)
		}
		document.Blocks = append(document.Blocks, block)
	}
	if len(document.Blocks) == 0 {
		return resourceapp.ParsedDocument{}, parseError("DOCUMENT_EMPTY")
	}
	if document.Title == "" {
		filename = strings.ReplaceAll(filename, "\\", "/")
		document.Title = boundedText(strings.TrimSuffix(path.Base(filename), path.Ext(filename)), 200)
		if document.Title == "." || document.Title == "" {
			document.Title = boundedText(document.Blocks[0].Text, 200)
		}
	}
	document.Language = "und"
	if han > 0 && han >= latin/3 {
		document.Language = "zh"
	} else if latin > 0 {
		document.Language = "en"
	}
	return document, nil
}

func boundedText(value string, size int) string {
	runes := []rune(value)
	if len(runes) > size {
		runes = runes[:size]
	}
	return string(runes)
}

func parseText(ctx context.Context, data []byte, markdown bool, page int) ([]resourceapp.DocumentBlock, error) {
	text, err := normalizeText(string(bytes.TrimPrefix(data, []byte{0xef, 0xbb, 0xbf})))
	if err != nil {
		return nil, err
	}
	if utf8.RuneCountInString(text) > MaxCharacters {
		return nil, parseError("DOCUMENT_CHARACTER_LIMIT")
	}
	blocks := make([]resourceapp.DocumentBlock, 0)
	var paragraph []string
	var headings [6]string
	section, fence := "", ""
	flush := func() {
		if len(paragraph) > 0 {
			blocks = append(blocks, resourceapp.DocumentBlock{Kind: "paragraph", Text: strings.Join(paragraph, "\n"), Page: page, SectionPath: section})
			paragraph = nil
		}
	}
	for _, line := range strings.Split(text, "\n") {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		trimmed := strings.TrimSpace(line)
		if markdown && (strings.HasPrefix(trimmed, "```") || strings.HasPrefix(trimmed, "~~~")) {
			if fence == "" {
				fence = trimmed[:3]
			} else if strings.HasPrefix(trimmed, fence) {
				fence = ""
			}
			paragraph = append(paragraph, line)
			continue
		}
		if markdown && fence == "" {
			level := len(trimmed) - len(strings.TrimLeft(trimmed, "#"))
			if level > 0 && level <= 6 && len(trimmed) > level && trimmed[level] == ' ' {
				flush()
				heading := strings.TrimSpace(trimmed[level:])
				withoutClosing := strings.TrimRight(heading, "#")
				if len(withoutClosing) < len(heading) && strings.HasSuffix(withoutClosing, " ") {
					heading = strings.TrimSpace(withoutClosing)
				}
				if heading != "" {
					headings[level-1] = heading
					for i := level; i < len(headings); i++ {
						headings[i] = ""
					}
					section = headingPath(headings[:])
					blocks = append(blocks, resourceapp.DocumentBlock{Kind: "heading", Text: heading, Page: page, SectionPath: section})
				}
				continue
			}
		}
		if trimmed == "" && fence == "" {
			flush()
		} else {
			paragraph = append(paragraph, line)
		}
		if len(blocks) > MaxBlocks {
			return nil, parseError("DOCUMENT_BLOCK_LIMIT")
		}
	}
	flush()
	if len(blocks) > MaxBlocks {
		return nil, parseError("DOCUMENT_BLOCK_LIMIT")
	}
	return blocks, nil
}

func headingPath(headings []string) string {
	parts := make([]string, 0, len(headings))
	for _, heading := range headings {
		if heading != "" {
			parts = append(parts, heading)
		}
	}
	return boundedText(strings.Join(parts, " / "), 1000)
}
