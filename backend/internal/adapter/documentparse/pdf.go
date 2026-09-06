package documentparse

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	resourceapp "mathstudy/backend/internal/application/resource"
)

func (p *Parser) parsePDF(ctx context.Context, data []byte) ([]resourceapp.DocumentBlock, error) {
	if !bytes.HasPrefix(data, []byte("%PDF-")) {
		return nil, parseError("DOCUMENT_INVALID")
	}
	directory, err := os.MkdirTemp("", "mathstudy-document-*")
	if err != nil {
		return nil, &Error{Code: "PARSER_UNAVAILABLE", Retryable: true}
	}
	defer os.RemoveAll(directory)
	filename := filepath.Join(directory, "source.pdf")
	if err := os.WriteFile(filename, data, 0o600); err != nil {
		return nil, &Error{Code: "PARSER_UNAVAILABLE", Retryable: true}
	}
	info, err := runPDFCommand(ctx, p.pdfInfoPath, 64<<10, "-enc", "UTF-8", filename)
	if err != nil {
		return nil, err
	}
	pages, encrypted := 0, ""
	for _, line := range strings.Split(string(info), "\n") {
		key, value, ok := strings.Cut(line, ":")
		if !ok {
			continue
		}
		switch strings.TrimSpace(key) {
		case "Pages":
			pages, _ = strconv.Atoi(strings.TrimSpace(value))
		case "Encrypted":
			encrypted = strings.ToLower(strings.TrimSpace(value))
		}
	}
	if strings.HasPrefix(encrypted, "yes") {
		return nil, parseError("DOCUMENT_ENCRYPTED")
	}
	if pages > MaxPages {
		return nil, parseError("DOCUMENT_PAGE_LIMIT")
	}
	if pages < 1 || !strings.HasPrefix(encrypted, "no") {
		return nil, parseError("DOCUMENT_INVALID")
	}
	text, err := runPDFCommand(ctx, p.pdfToTextPath, MaxCharacters*4+MaxPages, "-enc", "UTF-8", "-layout", "-eol", "unix", "-f", "1", "-l", strconv.Itoa(pages), filename, "-")
	if err != nil {
		return nil, err
	}
	pageTexts := strings.Split(string(text), "\f")
	if len(pageTexts) > pages && strings.TrimSpace(pageTexts[len(pageTexts)-1]) == "" {
		pageTexts = pageTexts[:len(pageTexts)-1]
	}
	if len(pageTexts) > pages {
		return nil, parseError("DOCUMENT_INVALID")
	}
	blocks := make([]resourceapp.DocumentBlock, 0)
	for i, pageText := range pageTexts {
		pageBlocks, err := parseText(ctx, []byte(pageText), false, i+1)
		if err != nil {
			return nil, err
		}
		blocks = append(blocks, pageBlocks...)
	}
	if len(blocks) == 0 {
		return nil, parseError("PDF_NO_TEXT")
	}
	return blocks, nil
}

type boundedCommandOutput struct {
	buffer bytes.Buffer
	limit  int
	cancel context.CancelFunc
	excess bool
}

func (w *boundedCommandOutput) Write(value []byte) (int, error) {
	if len(value) > w.limit-w.buffer.Len() {
		w.excess = true
		w.cancel()
		return 0, io.ErrShortBuffer
	}
	return w.buffer.Write(value)
}

func runPDFCommand(ctx context.Context, executable string, outputLimit int, args ...string) ([]byte, error) {
	commandCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	// Executable paths are deployment configuration; document paths are separate
	// argv entries. No shell or caller-supplied command is used.
	command := exec.CommandContext(commandCtx, executable, args...)
	command.Env = append(os.Environ(), "LC_ALL=C", "LANG=C")
	command.WaitDelay = 2 * time.Second
	output := &boundedCommandOutput{limit: outputLimit, cancel: cancel}
	diagnostics := &boundedCommandOutput{limit: 64 << 10, cancel: cancel}
	command.Stdout, command.Stderr = output, diagnostics
	err := command.Run()
	if ctx.Err() != nil {
		return nil, ctx.Err()
	}
	if output.excess || diagnostics.excess {
		return nil, parseError("DOCUMENT_CHARACTER_LIMIT")
	}
	if err != nil {
		var startError *exec.Error
		var pathError *os.PathError
		if errors.As(err, &startError) || errors.As(err, &pathError) {
			return nil, &Error{Code: "PARSER_UNAVAILABLE", Retryable: true}
		}
		detail := strings.ToLower(diagnostics.buffer.String())
		if strings.Contains(detail, "password") || strings.Contains(detail, "encrypted") {
			return nil, parseError("DOCUMENT_ENCRYPTED")
		}
		return nil, parseError("DOCUMENT_INVALID")
	}
	return output.buffer.Bytes(), nil
}
