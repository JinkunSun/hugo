// Copyright 2019 The Hugo Authors. All rights reserved.
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
// http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

// Package helpers implements general utility functions that work with
// and on content.  The helper functions defined here lay down the
// foundation of how Hugo works with files and filepaths, and perform
// string operations on content.
package helpers

import (
	"bytes"
	"html/template"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/gohugoio/hugo/common/hexec"
	"github.com/gohugoio/hugo/common/loggers"

	"github.com/spf13/afero"

	"github.com/gohugoio/hugo/markup/converter"
	"github.com/gohugoio/hugo/markup/converter/hooks"

	"github.com/gohugoio/hugo/markup"

	"github.com/gohugoio/hugo/config"
)

var (
	openingPTag        = []byte("<p>")
	closingPTag        = []byte("</p>")
	paragraphIndicator = []byte("<p")
	closingIndicator   = []byte("</")
)

// ContentSpec provides functionality to render markdown content.
type ContentSpec struct {
	Converters          markup.ConverterProvider
	anchorNameSanitizer converter.AnchorNameSanitizer
	getRenderer         func(t hooks.RendererType, id any) any

	Cfg config.AllProvider
}

// NewContentSpec returns a ContentSpec initialized
// with the appropriate fields from the given config.Provider.
func NewContentSpec(cfg config.AllProvider, logger loggers.Logger, contentFs afero.Fs, ex *hexec.Exec) (*ContentSpec, error) {
	spec := &ContentSpec{
		Cfg: cfg,
	}

	converterProvider, err := markup.NewConverterProvider(converter.ProviderConfig{
		Conf:      cfg,
		ContentFs: contentFs,
		Logger:    logger,
		Exec:      ex,
	})
	if err != nil {
		return nil, err
	}

	spec.Converters = converterProvider
	p := converterProvider.Get("markdown")
	conv, err := p.New(converter.DocumentContext{})
	if err != nil {
		return nil, err
	}
	if as, ok := conv.(converter.AnchorNameSanitizer); ok {
		spec.anchorNameSanitizer = as
	} else {
		// Use Goldmark's sanitizer
		p := converterProvider.Get("goldmark")
		conv, err := p.New(converter.DocumentContext{})
		if err != nil {
			return nil, err
		}
		spec.anchorNameSanitizer = conv.(converter.AnchorNameSanitizer)
	}

	return spec, nil
}

// stripEmptyNav strips out empty <nav> tags from content.
func stripEmptyNav(in []byte) []byte {
	return bytes.Replace(in, []byte("<nav>\n</nav>\n\n"), []byte(``), -1)
}

// BytesToHTML converts bytes to type template.HTML.
func BytesToHTML(b []byte) template.HTML {
	return template.HTML(string(b))
}

// ExtractTOC extracts Table of Contents from content.
func ExtractTOC(content []byte) (newcontent []byte, toc []byte) {
	if !bytes.Contains(content, []byte("<nav>")) {
		return content, nil
	}
	origContent := make([]byte, len(content))
	copy(origContent, content)
	first := []byte(`<nav>
<ul>`)

	last := []byte(`</ul>
</nav>`)

	replacement := []byte(`<nav id="TableOfContents">
<ul>`)

	startOfTOC := bytes.Index(content, first)

	peekEnd := len(content)
	if peekEnd > 70+startOfTOC {
		peekEnd = 70 + startOfTOC
	}

	if startOfTOC < 0 {
		return stripEmptyNav(content), toc
	}
	// Need to peek ahead to see if this nav element is actually the right one.
	correctNav := bytes.Index(content[startOfTOC:peekEnd], []byte(`<li><a href="#`))
	if correctNav < 0 { // no match found
		return content, toc
	}
	lengthOfTOC := bytes.Index(content[startOfTOC:], last) + len(last)
	endOfTOC := startOfTOC + lengthOfTOC

	newcontent = append(content[:startOfTOC], content[endOfTOC:]...)
	toc = append(replacement, origContent[startOfTOC+len(first):endOfTOC]...)
	return
}

func (c *ContentSpec) SanitizeAnchorName(s string) string {
	return c.anchorNameSanitizer.SanitizeAnchorName(s)
}

func (c *ContentSpec) ResolveMarkup(in string) string {
	if c == nil {
		panic("nil ContentSpec")
	}
	in = strings.ToLower(in)
	switch in {
	case "md", "markdown", "mdown":
		return "markdown"
	case "html", "htm":
		return "html"
	default:
		if conv := c.Converters.Get(in); conv != nil {
			return conv.Name()
		}
	}
	return ""
}

// TotalWords counts instance of one or more consecutive white space
// characters, as defined by unicode.IsSpace, in s.
// This is a cheaper way of word counting than the obvious len(strings.Fields(s)).
func TotalWords(s string) int {
	n := 0
	inWord := false
	for _, r := range s {
		wasInWord := inWord
		inWord = !unicode.IsSpace(r)
		if inWord && !wasInWord {
			n++
		}
	}
	return n
}

// TruncateWordsByRune truncates words by runes.
func (c *ContentSpec) TruncateWordsByRune(in []string) (string, bool) {
	words := make([]string, len(in))
	copy(words, in)

	count := 0
	for index, word := range words {
		if count >= c.Cfg.SummaryLength() {
			return strings.Join(words[:index], " "), true
		}
		runeCount := utf8.RuneCountInString(word)
		if len(word) == runeCount {
			count++
		} else if count+runeCount < c.Cfg.SummaryLength() {
			count += runeCount
		} else {
			for ri := range word {
				if count >= c.Cfg.SummaryLength() {
					truncatedWords := append(words[:index], word[:ri])
					return strings.Join(truncatedWords, " "), true
				}
				count++
			}
		}
	}

	return strings.Join(words, " "), false
}

// TruncateWordsToWholeSentence takes content and truncates to whole sentence
// limited by max number of words. It also returns whether it is truncated.
func (c *ContentSpec) TruncateWordsToWholeSentence(s string) (string, bool) {
	var (
		wordCount     = 0
		lastWordIndex = -1
	)

	for i, r := range s {
		if unicode.IsSpace(r) {
			wordCount++
			lastWordIndex = i

			if wordCount >= c.Cfg.SummaryLength() {
				break
			}

		}
	}

	if lastWordIndex == -1 {
		return s, false
	}

	endIndex := -1

	for j, r := range s[lastWordIndex:] {
		if isEndOfSentence(r) {
			endIndex = j + lastWordIndex + utf8.RuneLen(r)
			break
		}
	}

	if endIndex == -1 {
		return s, false
	}

	return strings.TrimSpace(s[:endIndex]), endIndex < len(s)
}

// TrimShortHTML removes the <p>/</p> tags from HTML input in the situation
// where said tags are the only <p> tags in the input and enclose the content
// of the input (whitespace excluded).
func (c *ContentSpec) TrimShortHTML(input []byte) []byte {
	if bytes.Count(input, openingPTag) == 1 {
		input = bytes.TrimSpace(input)
		if bytes.HasPrefix(input, openingPTag) && bytes.HasSuffix(input, closingPTag) {
			input = bytes.TrimPrefix(input, openingPTag)
			input = bytes.TrimSuffix(input, closingPTag)
			input = bytes.TrimSpace(input)
		}
	}

	return input
}

func isEndOfSentence(r rune) bool {
	return r == '.' || r == '?' || r == '!' || r == '"' || r == '\n'
}

// Kept only for benchmark.
func (c *ContentSpec) truncateWordsToWholeSentenceOld(content string) (string, bool) {
	words := strings.Fields(content)

	if c.Cfg.SummaryLength() >= len(words) {
		return strings.Join(words, " "), false
	}

	for counter, word := range words[c.Cfg.SummaryLength():] {
		if strings.HasSuffix(word, ".") ||
			strings.HasSuffix(word, "?") ||
			strings.HasSuffix(word, ".\"") ||
			strings.HasSuffix(word, "!") {
			upper := c.Cfg.SummaryLength() + counter + 1
			return strings.Join(words[:upper], " "), (upper < len(words))
		}
	}

	return strings.Join(words[:c.Cfg.SummaryLength()], " "), true
}
