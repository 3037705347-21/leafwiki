package markdown

import (
	"fmt"
	"strings"
	"unicode"
	"unicode/utf8"

	yaml "gopkg.in/yaml.v3"
)

// TagsSpanKind describes how the tags value was found in the frontmatter.
type TagsSpanKind int

const (
	// TagsSpanNone means the document has no frontmatter or no top-level
	// tags key; there is nothing to rewrite.
	TagsSpanNone TagsSpanKind = iota
	// TagsSpanUnsupported means a tags key exists but its value cannot be
	// rewritten safely (malformed YAML, non-sequence value, mixed-type list,
	// duplicate keys, …). The caller must leave the file untouched.
	TagsSpanUnsupported
	// TagsSpanBlock is a block sequence ("tags:\n  - a").
	TagsSpanBlock
	// TagsSpanFlow is a flow sequence ("tags: [a, b]").
	TagsSpanFlow
)

// rawLine records the byte span of one line in the original document.
type rawLine struct {
	start      int // offset of the first byte of the line
	contentEnd int // offset just before the trailing \r / \n
	nlLen      int // 2 for CRLF, 1 for LF, 0 when the file ends without one
}

func splitRawLines(raw string) []rawLine {
	lines := make([]rawLine, 0, 8)
	start := 0
	for start <= len(raw) {
		relNL := strings.IndexByte(raw[start:], '\n')
		if relNL < 0 {
			if start < len(raw) {
				lines = append(lines, rawLine{start: start, contentEnd: len(raw), nlLen: 0})
			}
			return lines
		}
		nl := start + relNL
		contentEnd := nl
		if contentEnd > start && raw[contentEnd-1] == '\r' {
			contentEnd--
		}
		lines = append(lines, rawLine{start: start, contentEnd: contentEnd, nlLen: nl + 1 - contentEnd})
		start = nl + 1
	}
	return lines
}

// locateFrontmatter returns the raw line index of the closing delimiter of a
// leading frontmatter block, mirroring splitFrontmatter's detection (BOM,
// "---" lines, looksLikeYAML) but keeping byte offsets into the original,
// unnormalized document.
func locateFrontmatter(lines []rawLine, raw string) (int, bool) {
	if len(lines) == 0 {
		return 0, false
	}
	first := raw[lines[0].start:lines[0].contentEnd]
	first = strings.TrimPrefix(first, "\ufeff")
	if strings.TrimSpace(first) != "---" {
		return 0, false
	}

	looksLikeYAML := false
	for i := 1; i < len(lines); i++ {
		trim := strings.TrimSpace(raw[lines[i].start:lines[i].contentEnd])
		if trim == "---" {
			if !looksLikeYAML {
				return 0, false
			}
			return i, true
		}
		if trim != "" && !strings.HasPrefix(trim, "#") {
			if idx := strings.IndexByte(trim, ':'); idx > 0 {
				key := strings.TrimSpace(trim[:idx])
				hasYAMLSeparator := idx == len(trim)-1 || unicode.IsSpace(rune(trim[idx+1]))
				if hasYAMLSeparator && key != "" && strings.IndexFunc(key, invalidYAMLKeyRune) == -1 {
					looksLikeYAML = true
				}
			}
		}
	}
	return 0, false
}

// colToOffset converts a 1-based yaml.v3 column into a byte offset within the
// given raw line text.
func colToOffset(line string, column int) (int, bool) {
	if column < 1 {
		return 0, false
	}
	off := 0
	for i := 0; i < column-1; i++ {
		if off >= len(line) {
			return 0, false
		}
		_, size := utf8.DecodeRuneInString(line[off:])
		off += size
	}
	return off, true
}

// findTagsPair returns the key and value nodes of the unique top-level tags
// key in the parsed document. duplicated is true when the key appears more
// than once (the caller treats that as unsupported rather than guessing).
func findTagsPair(doc *yaml.Node) (key, value *yaml.Node, duplicated, ok bool) {
	if doc == nil || doc.Kind != yaml.DocumentNode || len(doc.Content) != 1 {
		return nil, nil, false, false
	}
	mapping := doc.Content[0]
	if mapping.Kind != yaml.MappingNode {
		return nil, nil, false, false
	}
	for i := 0; i+1 < len(mapping.Content); i += 2 {
		k, v := mapping.Content[i], mapping.Content[i+1]
		if k.Kind != yaml.ScalarNode || k.Column != 1 {
			continue
		}
		if strings.EqualFold(strings.TrimSpace(k.Value), "tags") {
			if key != nil {
				return nil, nil, true, false
			}
			key, value = k, v
		}
	}
	if key == nil {
		return nil, nil, false, false
	}
	return key, value, false, true
}

func isStringSequence(v *yaml.Node) bool {
	if v == nil || v.Kind != yaml.SequenceNode || len(v.Content) == 0 {
		return false
	}
	for _, item := range v.Content {
		if item.Kind != yaml.ScalarNode || item.Tag != "!!str" {
			return false
		}
	}
	return true
}

// marshalTags renders newTags as YAML string scalars in the requested
// sequence style. Explicit !!str tags keep values like "true" or "1" as
// quoted strings so ExtractTagsFromContent reads them back as strings.
func marshalTags(newTags []string, flow bool) (string, error) {
	seq := &yaml.Node{Kind: yaml.SequenceNode}
	if flow {
		seq.Style = yaml.FlowStyle
	}
	for _, t := range newTags {
		seq.Content = append(seq.Content, &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: t})
	}
	b, err := yaml.Marshal(seq)
	if err != nil {
		return "", err
	}
	return strings.TrimRight(string(b), "\r\n"), nil
}

// flowEndOffset returns the byte offset just past the ']' closing the flow
// sequence beginning at start (which must point at '['), scanning
// quote- and comment-aware so ']' characters inside strings or comments are
// ignored.
func flowEndOffset(raw string, start int) (int, error) {
	depth := 0
	for i := start; i < len(raw); {
		switch raw[i] {
		case '\'':
			i++
			for i < len(raw) {
				if raw[i] == '\'' {
					if i+1 < len(raw) && raw[i+1] == '\'' {
						i += 2
						continue
					}
					i++
					break
				}
				i++
			}
		case '"':
			i++
			for i < len(raw) {
				if raw[i] == '\\' {
					i += 2
					continue
				}
				if raw[i] == '"' {
					i++
					break
				}
				i++
			}
		case '#':
			// A comment runs to the end of the line; a closing bracket can
			// follow on a later line.
			rel := strings.IndexByte(raw[i:], '\n')
			if rel < 0 {
				return 0, fmt.Errorf("unterminated comment in flow tags")
			}
			i += rel + 1
		case '[', '{':
			depth++
			i++
		case ']', '}':
			depth--
			i++
			if depth == 0 {
				return i, nil
			}
		default:
			i++
		}
	}
	return 0, fmt.Errorf("unterminated flow tags sequence")
}

// RewriteFrontmatterTags replaces ONLY the tags sequence value in the
// leading frontmatter with newTags. Every other byte — BOM, newlines,
// comments, other frontmatter keys and the body — is preserved. The returned
// kind reports what was found; on TagsSpanNone/TagsSpanUnsupported the
// input is returned unchanged.
//
// newTags must be non-empty, already normalized (trimmed, lowercased,
// deduped). Block sequences keep their original indentation and newline
// style; flow sequences stay single-flow. Comment lines strictly inside the
// item span of a block sequence are replaced together with the value.
func RewriteFrontmatterTags(raw string, newTags []string) (string, TagsSpanKind, error) {
	if len(newTags) == 0 {
		return raw, TagsSpanNone, fmt.Errorf("RewriteFrontmatterTags requires at least one tag")
	}

	lines := splitRawLines(raw)
	closeIdx, ok := locateFrontmatter(lines, raw)
	if !ok {
		return raw, TagsSpanNone, nil
	}

	yamlStart := lines[1].start
	yamlEnd := lines[closeIdx-1].contentEnd
	yamlText := raw[yamlStart:yamlEnd]

	var doc yaml.Node
	if err := yaml.Unmarshal([]byte(yamlText), &doc); err != nil {
		// Never rewrite frontmatter that only parses via the sanitize fallback.
		return raw, TagsSpanUnsupported, nil
	}

	key, value, duplicated, found := findTagsPair(&doc)
	if duplicated {
		return raw, TagsSpanUnsupported, nil
	}
	if !found {
		return raw, TagsSpanNone, nil
	}
	if !isStringSequence(value) {
		return raw, TagsSpanUnsupported, nil
	}

	// Node lines are 1-based within yamlText, whose line 1 is raw line index 1.
	firstRawLine := lines[value.Line]

	switch value.Style {
	case yaml.FlowStyle:
		colOff, ok := colToOffset(raw[firstRawLine.start:firstRawLine.contentEnd], value.Column)
		if !ok {
			return raw, TagsSpanUnsupported, nil
		}
		bracketStart := firstRawLine.start + colOff
		if bracketStart >= len(raw) || raw[bracketStart] != '[' {
			return raw, TagsSpanUnsupported, nil
		}
		bracketEnd, err := flowEndOffset(raw, bracketStart)
		if err != nil {
			return raw, TagsSpanUnsupported, nil
		}
		replacement, err := marshalTags(newTags, true)
		if err != nil {
			return raw, TagsSpanUnsupported, nil
		}
		return raw[:bracketStart] + replacement + raw[bracketEnd:], TagsSpanFlow, nil

	default: // block sequence
		lastItem := value.Content[len(value.Content)-1]
		// The sequence node's Line/Column point at the first '-' dash.
		firstDashLine := lines[value.Line]
		colOff, ok := colToOffset(raw[firstDashLine.start:firstDashLine.contentEnd], value.Column)
		if !ok {
			return raw, TagsSpanUnsupported, nil
		}
		spanStart := firstDashLine.start + colOff
		// A sequence entry sharing the key line ("tags: - a") is too rare to
		// splice safely; leave it for a regular editor save.
		if value.Line == key.Line || spanStart >= len(raw) || raw[spanStart] != '-' {
			return raw, TagsSpanUnsupported, nil
		}
		indent := raw[firstDashLine.start:spanStart]
		for _, r := range indent {
			if r != ' ' && r != '\t' {
				return raw, TagsSpanUnsupported, nil
			}
		}

		lastItemLine := lines[lastItem.Line]
		spanEnd := lastItemLine.contentEnd

		rendered, err := marshalTags(newTags, false)
		if err != nil {
			return raw, TagsSpanUnsupported, nil
		}
		nl := "\n"
		if lastItemLine.nlLen == 2 || firstDashLine.nlLen == 2 {
			nl = "\r\n"
		}
		var b strings.Builder
		for i, renderedLine := range strings.Split(rendered, "\n") {
			if i > 0 {
				// Subsequent lines start after a newline; the span start for
				// the first line already carries its indent in raw[:spanStart].
				b.WriteString(nl)
				b.WriteString(indent)
			}
			b.WriteString(strings.TrimLeft(renderedLine, " \t"))
		}
		return raw[:spanStart] + b.String() + raw[spanEnd:], TagsSpanBlock, nil
	}
}
