package components

import "strconv"

func itoa(n int) string { return strconv.Itoa(n) }

func truncate(s string, max int) string {
	if len(s) <= max {
		return s
	}
	return s[:max] + "…"
}

func tabClass(label, active string) string {
	if label == active {
		return "tab tab-active"
	}
	return "tab"
}
