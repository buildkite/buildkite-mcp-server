package sanitize

import (
	"regexp"
	"strings"
	"sync"

	"github.com/microcosm-cc/bluemonday"
)

// Sanitize applies a 5-stage sanitization pipeline to the input string:
// 1. FilterInvisibleCharacters — strip zero-width chars, BiDi controls, Unicode tags
// 2. FilterControlCharacters — strip raw control chars preserving \t \n \r
// 3. FilterCodeFenceMetadata — strip suspicious code fence info strings
// 4. FilterHTMLTags — bluemonday allowlist-based HTML sanitization
// 5. FilterLLMDelimiters — neutralize prompt role/delimiter markers
func Sanitize(s string) string {
	s = FilterInvisibleCharacters(s)
	s = FilterControlCharacters(s)
	s = FilterCodeFenceMetadata(s)
	s = FilterHTMLTags(s)
	s = FilterLLMDelimiters(s)
	return s
}

// invisibleRe matches zero-width characters, BiDi controls, Unicode tag characters,
// and other invisible modifiers that can be used for prompt injection.
var invisibleRe = regexp.MustCompile("[\u200B-\u200F\u2028-\u202F\u2060-\u2069\u206A-\u206F\uFEFF\uFFF9-\uFFFB" +
	"\U000E0001-\U000E007F" + // Unicode tag characters (U+E0001–U+E007F)
	"\U0001D173-\U0001D17A" + // Musical symbol invisible chars
	"]")

// FilterInvisibleCharacters strips zero-width characters, BiDi controls,
// Unicode tag characters, and hidden modifiers.
func FilterInvisibleCharacters(s string) string {
	return invisibleRe.ReplaceAllString(s, "")
}

// FilterControlCharacters strips raw control characters (0x00-0x08, 0x0B, 0x0C,
// 0x0E-0x1F, 0x7F) while preserving tab (\t, 0x09), newline (\n, 0x0A),
// and carriage return (\r, 0x0D).
func FilterControlCharacters(s string) string {
	return strings.Map(func(r rune) rune {
		// Preserve tab, newline, carriage return
		if r == '\t' || r == '\n' || r == '\r' {
			return r
		}
		// Strip C0 control characters and DEL
		if r < 0x20 || r == 0x7F {
			return -1
		}
		return r
	}, s)
}

// codeFenceRe matches fenced code block opening lines with suspicious info strings
// that could be used for prompt injection. Matches ``` or ~~~ followed by content
// containing suspicious patterns.
var codeFenceRe = regexp.MustCompile("(?m)^(```|~~~)[ \t]*([^\n]*?)[ \t]*$")

// suspiciousInfoStringRe matches info strings that look like prompt injection attempts:
// contains special delimiters, instructions, role markers, or overly long strings.
var suspiciousInfoStringRe = regexp.MustCompile(`(?i)(system|user|assistant|human|<\||\[INST|<<SYS|ignore|forget|override|instruction|prompt)`)

// FilterCodeFenceMetadata strips suspicious info strings from markdown fenced code
// blocks to prevent prompt injection via code fence metadata.
func FilterCodeFenceMetadata(s string) string {
	return codeFenceRe.ReplaceAllStringFunc(s, func(match string) string {
		submatch := codeFenceRe.FindStringSubmatch(match)
		if len(submatch) < 3 {
			return match
		}
		fence := submatch[1]
		infoString := submatch[2]

		// Allow common, safe language identifiers
		if isSafeInfoString(infoString) {
			return match
		}

		// Strip suspicious info strings
		if suspiciousInfoStringRe.MatchString(infoString) || len(infoString) > 50 {
			return fence
		}

		return match
	})
}

// safeInfoStrings contains language names commonly used in code fences.
var safeInfoStrings = map[string]bool{
	"": true, "bash": true, "sh": true, "zsh": true, "shell": true,
	"python": true, "py": true, "javascript": true, "js": true, "typescript": true, "ts": true,
	"go": true, "golang": true, "rust": true, "ruby": true, "rb": true,
	"java": true, "kotlin": true, "scala": true, "swift": true,
	"c": true, "cpp": true, "c++": true, "csharp": true, "cs": true,
	"html": true, "css": true, "scss": true, "sass": true, "less": true,
	"xml": true, "json": true, "yaml": true, "yml": true, "toml": true,
	"sql": true, "graphql": true, "gql": true,
	"markdown": true, "md": true, "text": true, "txt": true, "plaintext": true,
	"diff": true, "dockerfile": true, "docker": true, "makefile": true,
	"lua": true, "perl": true, "php": true, "r": true, "elixir": true, "erlang": true,
	"haskell": true, "clojure": true, "lisp": true, "ocaml": true, "fsharp": true,
	"powershell": true, "ps1": true, "bat": true, "cmd": true,
	"terraform": true, "hcl": true, "nix": true, "protobuf": true, "proto": true,
	"csv": true, "tsv": true, "ini": true, "conf": true, "cfg": true, "env": true,
	"log": true, "console": true, "output": true,
}

func isSafeInfoString(s string) bool {
	return safeInfoStrings[strings.ToLower(strings.TrimSpace(s))]
}

// htmlPolicy is a lazily-initialized bluemonday policy for Buildkite annotation HTML.
var (
	htmlPolicy     *bluemonday.Policy
	htmlPolicyOnce sync.Once
)

func getHTMLPolicy() *bluemonday.Policy {
	htmlPolicyOnce.Do(func() {
		p := bluemonday.NewPolicy()

		// Block-level elements
		p.AllowElements("p", "div", "br", "hr", "pre", "blockquote",
			"h1", "h2", "h3", "h4", "h5", "h6",
			"ul", "ol", "li", "dl", "dt", "dd",
			"details", "summary")

		// Inline elements
		p.AllowElements("b", "strong", "i", "em", "u", "s", "del", "ins",
			"sub", "sup", "small", "mark", "abbr", "cite", "q",
			"code", "kbd", "samp", "var", "tt")

		// Tables
		p.AllowElements("table", "thead", "tbody", "tfoot", "tr", "th", "td",
			"caption", "colgroup", "col")
		p.AllowAttrs("colspan", "rowspan", "align", "valign").OnElements("th", "td")

		// Spans with class (Buildkite uses CSS utility classes)
		p.AllowAttrs("class").OnElements("span", "div", "p", "td", "th", "tr", "table",
			"pre", "code", "li", "ul", "ol", "h1", "h2", "h3", "h4", "h5", "h6")

		// Images
		p.AllowImages()
		p.AllowAttrs("alt", "title").OnElements("img")

		// Links
		p.AllowAttrs("href", "title", "rel").OnElements("a")
		p.AllowRelativeURLs(true)
		p.RequireNoFollowOnLinks(false)

		// Data attributes commonly used by Buildkite
		p.AllowDataAttributes()

		htmlPolicy = p
	})
	return htmlPolicy
}

// htmlTagRe detects strings that likely contain HTML tags (opening tags like <div, <p>,
// <script>, etc.). This avoids running bluemonday on non-HTML strings like Link headers
// which use angle bracket syntax for URLs.
var htmlTagRe = regexp.MustCompile(`<[a-zA-Z][a-zA-Z0-9]*[\s/>]`)

// FilterHTMLTags applies bluemonday allowlist-based HTML sanitization.
// Allows standard block/inline elements, tables, code blocks, spans with class,
// images, and links. Strips script, iframe, style, event handlers, etc.
// Only runs on strings that appear to contain HTML tags.
func FilterHTMLTags(s string) string {
	if !htmlTagRe.MatchString(s) {
		return s
	}
	return getHTMLPolicy().Sanitize(s)
}

// llmDelimiterPatterns matches common LLM prompt format markers that could be
// used to break out of data context into prompt instructions.
var llmDelimiterPatterns = []*regexp.Regexp{
	// Llama/Mistral-style
	regexp.MustCompile(`(?i)\[INST\]`),
	regexp.MustCompile(`(?i)\[/INST\]`),
	regexp.MustCompile(`(?i)<<SYS>>`),
	regexp.MustCompile(`(?i)<</SYS>>`),
	// ChatML-style
	regexp.MustCompile(`<\|im_start\|>`),
	regexp.MustCompile(`<\|im_end\|>`),
	regexp.MustCompile(`<\|endoftext\|>`),
	// Anthropic-style (these appear as plain text in data)
	regexp.MustCompile(`(?m)^\s*\nHuman:\s`),
	regexp.MustCompile(`(?m)^\s*\nAssistant:\s`),
	// XML-style role tags that could be injected
	regexp.MustCompile(`(?i)</?(system|user|assistant|human|tool_result|function_call|function_result)>`),
}

// llmDelimiterReplacements maps each pattern to its safe replacement.
var llmDelimiterReplacements = []string{
	"[_INST_]",
	"[_/INST_]",
	"<<_SYS_>>",
	"<<_/SYS_>>",
	"<|_im_start_|>",
	"<|_im_end_|>",
	"<|_endoftext_|>",
	"\nHuman: ", // Remove leading newline that makes it look like a turn
	"\nAssistant: ",
	"", // XML role tags are stripped by the replacement function below
}

// FilterLLMDelimiters detects and neutralizes model prompt format markers
// that attempt to break out of data context into prompt instructions.
func FilterLLMDelimiters(s string) string {
	for i, pattern := range llmDelimiterPatterns {
		if i < len(llmDelimiterReplacements)-1 {
			s = pattern.ReplaceAllString(s, llmDelimiterReplacements[i])
		} else {
			// For XML role tags, wrap the tag name in underscores
			s = pattern.ReplaceAllStringFunc(s, func(match string) string {
				return strings.ReplaceAll(strings.ReplaceAll(match, "<", "<_"), ">", "_>")
			})
		}
	}
	return s
}
