package text

import (
	"strings"
	"unicode"
)

// stopwords is a compact English stopword set. It deliberately avoids trading
// terms (momentum, value, trend, price, market, ...) so domain queries stay
// precise.
var stopwords = map[string]bool{
	"a": true, "an": true, "the": true, "and": true, "or": true, "but": true,
	"if": true, "then": true, "else": true, "for": true, "nor": true, "so": true,
	"of": true, "to": true, "in": true, "on": true, "at": true, "by": true,
	"with": true, "about": true, "against": true, "between": true, "into": true,
	"through": true, "during": true, "before": true, "after": true, "above": true,
	"below": true, "from": true, "up": true, "down": true, "out": true, "off": true,
	"over": true, "under": true, "again": true, "further": true, "once": true,
	"is": true, "are": true, "was": true, "were": true, "be": true, "been": true,
	"being": true, "have": true, "has": true, "had": true, "having": true,
	"do": true, "does": true, "did": true, "doing": true, "will": true,
	"would": true, "should": true, "could": true, "can": true, "may": true,
	"might": true, "must": true, "shall": true, "i": true, "you": true, "he": true,
	"she": true, "it": true, "we": true, "they": true, "me": true, "him": true,
	"her": true, "us": true, "them": true, "my": true, "your": true, "his": true,
	"its": true, "our": true, "their": true, "this": true, "that": true,
	"these": true, "those": true, "what": true, "which": true, "who": true,
	"whom": true, "as": true, "not": true, "no": true, "too": true,
	"very": true, "more": true, "most": true, "some": true, "any": true,
	"each": true, "both": true, "such": true, "only": true, "own": true,
	"same": true, "than": true, "also": true, "when": true, "where": true,
	"why": true, "how": true, "all": true, "there": true, "here": true,
	"thus": true, "hence": true, "therefore": true, "however": true,
	"although": true, "while": true, "whereas": true, "because": true,
	"since": true, "until": true, "though": true, "per": true, "via": true,
	"etc": true, "eg": true, "ie": true, "fig": true, "figure": true,
	"table": true, "section": true, "chapter": true, "page": true,
}

// IsStopword reports whether a token is a stopword.
func IsStopword(token string) bool {
	return stopwords[token]
}

// Tokenize lowercases s, splits it into alphanumeric tokens, drops stopwords
// and single-character tokens, and returns the resulting slice in order.
// It is used identically for documents and queries.
func Tokenize(s string) []string {
	var out []string
	for _, raw := range tokenizeAll(s) {
		if len(raw) < 2 || IsStopword(raw) {
			continue
		}
		out = append(out, raw)
	}
	return out
}

// tokenizeAll lowercases s and returns every alphanumeric run in order.
func tokenizeAll(s string) []string {
	var tokens []string
	var sb strings.Builder
	flush := func() {
		if sb.Len() > 0 {
			tokens = append(tokens, sb.String())
			sb.Reset()
		}
	}
	for _, r := range s {
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			sb.WriteRune(unicode.ToLower(r))
		} else {
			flush()
		}
	}
	flush()
	return tokens
}
