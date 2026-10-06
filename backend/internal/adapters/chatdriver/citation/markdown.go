// Package citation turns provider citation annotations into portable Markdown.
// Provider adapters identify the cited spans; chat storage and renderers only
// need to understand Markdown links.
package citation

import (
	"net/url"
	"strconv"
	"strings"
	"unicode"
)

// Source is a provider-verified destination for one citation.
type Source struct {
	ID    string
	Title string
	URL   string
}

// Reference replaces the half-open byte range [Start, End) in the raw message.
// An empty range inserts a citation after a provider's cited text block.
type Reference struct {
	Start   int
	End     int
	Sources []Source
}

// Markdown replaces annotations with numbered links. Invalid or unavailable
// destinations stay visible as an unavailable-source label, never a guessed URL.
func Markdown(raw string, references []Reference) string {
	var out strings.Builder
	labels := make(map[string]int)
	end := 0
	for _, ref := range references {
		if ref.Start < end || ref.End < ref.Start || ref.End > len(raw) {
			continue
		}
		out.WriteString(raw[end:ref.Start])
		for i, source := range ref.Sources {
			if i > 0 {
				out.WriteString(", ")
			}
			if !safeWebURL(source.URL) {
				out.WriteString("[Source unavailable]")
				continue
			}
			key := source.ID
			if key == "" {
				key = source.URL
			}
			label := labels[key]
			if label == 0 {
				label = len(labels) + 1
				labels[key] = label
			}
			out.WriteString("[")
			out.WriteString(strconv.Itoa(label))
			out.WriteString("](<")
			out.WriteString(strings.ReplaceAll(source.URL, ">", "%3E"))
			out.WriteString(">)")
		}
		end = ref.End
	}
	out.WriteString(raw[end:])
	return out.String()
}

func safeWebURL(raw string) bool {
	for _, r := range raw {
		if unicode.IsSpace(r) || unicode.IsControl(r) || r == '<' || r == '>' || r == '\\' {
			return false
		}
	}
	u, err := url.Parse(raw)
	return err == nil && (u.Scheme == "https" || u.Scheme == "http") && u.Host != "" && u.User == nil
}
