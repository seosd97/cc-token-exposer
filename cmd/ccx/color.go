package main

import (
	"math"
	"strings"
)

const (
	ansiYellow = "\x1b[38;5;179m"
	ansiRed    = "\x1b[38;5;167m"
	ansiGray   = "\x1b[38;5;245m"
	ansiReset  = "\x1b[0m"
)

func paint(s, color string, on bool) string {
	if !on || color == "" {
		return s
	}
	return color + s + ansiReset
}

// utilColor returns the alert color for a 0–100 utilization: "" below 60,
// muted yellow from 60, muted red above 85.
func utilColor(u float64) string {
	switch {
	case u > 85:
		return ansiRed
	case u >= 60:
		return ansiYellow
	default:
		return ""
	}
}

// gauge renders a 0–100 utilization as a five-cell bar like "▮▮▯▯▯".
func gauge(u float64) string {
	filled := int(math.Round(u / 20))
	if filled < 0 {
		filled = 0
	}
	if filled > 5 {
		filled = 5
	}
	return strings.Repeat("▮", filled) + strings.Repeat("▯", 5-filled)
}
