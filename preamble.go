package main

import "strings"

func formatPreamble(text string) string {
	const maxChars = 4000
	if len(text) > maxChars {
		text = text[:maxChars]
		if idx := strings.LastIndex(text, "\n"); idx > 0 {
			text = text[:idx]
		}
		text += "\n[... truncated]"
	}
	return "## Project Graph\n\n```text\n" + text + "```\n"
}
