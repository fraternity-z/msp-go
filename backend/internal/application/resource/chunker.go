package resource

import (
	"context"
	"fmt"
	"strings"
	"unicode"
	"unicode/utf8"

	"golang.org/x/text/unicode/norm"
)

const ChunkTokenCountKind = "utf8_byte_upper_estimate"

// Reserve room below the smallest supported provider batch limit for input prefixes.
const MaxDocumentEmbeddingBatchBytes = 110000

// DeterministicChunker uses UTF-8 bytes as a conservative token-budget estimate.
// Offsets index Unicode runes in NFC blocks joined by two newline characters.
// TokenCount is an estimate, not an exact provider tokenizer result.
type DeterministicChunker struct{}

func NewDeterministicChunker() *DeterministicChunker { return &DeterministicChunker{} }

func (c *DeterministicChunker) Chunk(ctx context.Context, document ParsedDocument, policy ChunkPolicy) ([]ChunkDraft, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if policy.MaxTokens < 4 || policy.MaxTokens > 64000 || policy.MaxCharacters < 1 || policy.MaxCharacters > 64000 ||
		policy.OverlapTokens < 0 || policy.OverlapTokens >= policy.MaxTokens || len(document.Blocks) > 50_000 {
		return nil, fmt.Errorf("%w: invalid resource chunk policy", ErrParseFailed)
	}
	chunks := make([]ChunkDraft, 0)
	parents := make(map[string]int)
	offset := 0
	for _, block := range document.Blocks {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		if !utf8.ValidString(block.Text) || strings.ContainsRune(block.Text, '\x00') || !utf8.ValidString(block.SectionPath) ||
			utf8.RuneCountInString(block.SectionPath) > 1000 || block.Page < 0 || block.Page > 200 {
			return nil, fmt.Errorf("%w: invalid resource document block", ErrParseFailed)
		}
		text := strings.TrimSpace(norm.NFC.String(block.Text))
		if text == "" {
			continue
		}
		if offset > 0 {
			offset += 2
		}
		runes := []rune(text)
		if offset+len(runes) > 2_000_000 {
			return nil, fmt.Errorf("%w: resource document character limit exceeded", ErrParseFailed)
		}
		section := norm.NFC.String(block.SectionPath)
		first := len(chunks)
		for start := 0; start < len(runes); {
			if err := ctx.Err(); err != nil {
				return nil, err
			}
			for start < len(runes) && unicode.IsSpace(runes[start]) {
				start++
			}
			if start == len(runes) {
				break
			}
			end, bytesUsed := start, 0
			for end < len(runes) && end-start < policy.MaxCharacters {
				size := utf8.RuneLen(runes[end])
				if bytesUsed+size > policy.MaxTokens {
					break
				}
				bytesUsed += size
				end++
			}
			if end < len(runes) {
				for i := end - 1; i > start+(end-start)/2; i-- {
					if unicode.IsSpace(runes[i]) || strings.ContainsRune(".!?;\u3002\uff01\uff1f\uff1b", runes[i]) {
						end = i + 1
						break
					}
				}
			}
			contentEnd := end
			for contentEnd > start && unicode.IsSpace(runes[contentEnd-1]) {
				contentEnd--
			}
			content := string(runes[start:contentEnd])
			chunk := ChunkDraft{Ordinal: len(chunks), Content: content, Language: document.Language, Page: block.Page,
				SectionPath: section, StartOffset: offset + start, EndOffset: offset + contentEnd, TokenCount: len(content)}
			if parent, found := parents[section]; found && block.Kind != "heading" {
				value := parent
				chunk.ParentIndex = &value
			}
			chunks = append(chunks, chunk)
			if len(chunks) > 100_000 {
				return nil, fmt.Errorf("%w: resource chunk count limit exceeded", ErrParseFailed)
			}
			if end == len(runes) {
				break
			}
			next, overlap := end, 0
			for next > start+(end-start)/2 {
				size := utf8.RuneLen(runes[next-1])
				if overlap+size > policy.OverlapTokens {
					break
				}
				overlap += size
				next--
			}
			start = max(next, start+1)
		}
		if block.Kind == "heading" && section != "" && len(chunks) > first {
			parents[section] = first
		}
		offset += len(runes)
	}
	if len(chunks) == 0 {
		return nil, fmt.Errorf("%w: resource document has no chunks", ErrParseFailed)
	}
	return chunks, nil
}
