package store

import "strings"

const (
	footerPrefix = "**Task**:"
	noTaskLabel  = "ninguna"
)

// withTaskFooter makes the last line of content the `**Task**` footer for the
// task. key is "" for no task. rewritten is true only when an existing footer
// named another task; a missing footer is added without being reported.
func withTaskFooter(content, key, title string) (out string, rewritten bool) {
	body, footer, found := splitFooter(content)
	want := footerPrefix + " " + noTaskLabel
	if key != "" {
		want = footerPrefix + " " + key + " — " + title
	}
	if found && footerMatches(footer, key) {
		return content, false
	}
	if body != "" {
		body += "\n\n"
	}
	return body + want, found
}

func splitFooter(content string) (body, footer string, found bool) {
	trimmed := strings.TrimRight(content, " \t\r\n")
	i := strings.LastIndex(trimmed, "\n") + 1
	last := strings.TrimSpace(trimmed[i:])
	if !strings.HasPrefix(last, footerPrefix) {
		return trimmed, "", false
	}
	return strings.TrimRight(trimmed[:i], " \t\r\n"), strings.TrimSpace(strings.TrimPrefix(last, footerPrefix)), true
}

func footerMatches(footer, key string) bool {
	if key == "" {
		return strings.HasPrefix(strings.ToLower(footer), noTaskLabel)
	}
	got, _, _ := strings.Cut(footer, " — ")
	return strings.EqualFold(strings.TrimSpace(got), key)
}
