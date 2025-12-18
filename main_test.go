package main

import (
	"testing"
)

func TestYLabelFormatter(t *testing.T) {
	formatter := yLabelFormatter()
	tests := []struct {
		name string
		val  float64
		want string
	}{
		{"zero", 0, "0.00"},
		{"small positive", 0.456, "0.46"},
		{"medium", 42.1234, "42.12"},
		{"large", 512.5, "512.5"},
		{"very large", 5120, "5120"},
		{"small negative", -0.3, "-0.30"},
		{"large negative", -123.45, "-123.5"},
	}

	for _, tt := range tests {
		if got := formatter(0, tt.val); got != tt.want {
			t.Fatalf("%s: expected %s, got %s", tt.name, tt.want, got)
		}
	}
}
