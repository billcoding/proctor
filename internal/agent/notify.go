package agent

import "strings"

// normalizeMessageResult keeps command results short and stable for the teacher UI.
// Expected forms: "acked", "reply: …", "dismissed", "timeout", "shown".
func normalizeMessageResult(s string) string {
	s = strings.TrimSpace(s)
	if s == "" {
		return "acked"
	}
	if i := strings.IndexAny(s, "\r\n"); i >= 0 {
		s = strings.TrimSpace(s[:i])
	}
	if s == "" {
		return "acked"
	}
	lower := strings.ToLower(s)
	switch {
	case lower == "acked" || lower == "dismissed" || lower == "timeout" || lower == "shown":
		return lower
	case strings.HasPrefix(lower, "reply:"):
		text := strings.TrimSpace(s[len("reply:"):])
		if text == "" {
			return "acked"
		}
		return "reply: " + text
	default:
		return s
	}
}
