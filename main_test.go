package main

import (
	"math"
	"net/http"
	"net/http/httptest"
	"reflect"
	"testing"
)

func TestParseMetricLine(t *testing.T) {
	tests := []struct {
		name      string
		line      string
		wantName  string
		wantValue float64
		wantOK    bool
	}{
		{
			name:      "basic metric",
			line:      "metric_total 123.45",
			wantName:  "metric_total",
			wantValue: 123.45,
			wantOK:    true,
		},
		{
			name:      "with labels",
			line:      "requests_total{code=\"200\"} 12",
			wantName:  "requests_total",
			wantValue: 12,
			wantOK:    true,
		},
		{
			name:   "invalid line",
			line:   "not_a_metric_line",
			wantOK: false,
		},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			name, value, ok := parseMetricLine(tt.line)
			if ok != tt.wantOK {
				t.Fatalf("expected ok=%v, got %v", tt.wantOK, ok)
			}
			if !tt.wantOK {
				return
			}
			if name != tt.wantName {
				t.Fatalf("expected name %q, got %q", tt.wantName, name)
			}
			if math.Abs(value-tt.wantValue) > 1e-9 {
				t.Fatalf("expected value %v, got %v", tt.wantValue, value)
			}
		})
	}
}

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

func TestAbs(t *testing.T) {
	if got := abs(-3.5); math.Abs(got-3.5) > 1e-9 {
		t.Fatalf("expected 3.5, got %v", got)
	}
	if got := abs(2); got != 2 {
		t.Fatalf("expected 2, got %v", got)
	}
}

func TestFetchAllMetrics(t *testing.T) {
	body := "" +
		"# comment line\n" +
		"metric_a{env=\"prod\"} 10\n" +
		"metric_b 5\n" +
		"metric_a{env=\"prod\"} 12\n" +
		"metric_c 1\n"

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(body))
	}))
	defer server.Close()

	got, err := fetchAllMetrics(server.URL)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	want := []string{"metric_a", "metric_b", "metric_c"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("expected %v, got %v", want, got)
	}
}

func TestFetchAllMetricSeries(t *testing.T) {
	body := "" +
		"test_metric{env=\"prod\"} 1.23\n" +
		"test_metric 2.34\n" +
		"other_metric 5\n"

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(body))
	}))
	defer server.Close()

	samples, err := fetchAllMetricSeries(server.URL, "test_metric")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(samples) != 2 {
		t.Fatalf("expected 2 samples, got %d", len(samples))
	}
	if samples[0].FullName != "test_metric{env=\"prod\"}" {
		t.Fatalf("unexpected first full name: %s", samples[0].FullName)
	}
	if samples[1].FullName != "test_metric{}" {
		t.Fatalf("expected empty labels, got %s", samples[1].FullName)
	}
	if math.Abs(samples[1].Value-2.34) > 1e-9 {
		t.Fatalf("expected second value 2.34, got %v", samples[1].Value)
	}

	emptyServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("other_metric 1\n"))
	}))
	defer emptyServer.Close()

	if _, err := fetchAllMetricSeries(emptyServer.URL, "missing"); err == nil {
		t.Fatalf("expected error when metric is missing")
	}
}

func TestFetchAllMetricsHTTPError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte("boom"))
	}))
	defer server.Close()

	if _, err := fetchAllMetrics(server.URL); err == nil {
		t.Fatalf("expected error when server returns non-200 status")
	}
}

func TestFetchAllMetricSeriesHTTPError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte("boom"))
	}))
	defer server.Close()

	if _, err := fetchAllMetricSeries(server.URL, "any"); err == nil {
		t.Fatalf("expected error when server returns non-200 status")
	}
}

func TestFetchAllMetricSeriesUsesSecondValueOnBadSuffix(t *testing.T) {
	body := "metric_with_bad_suffix 7.89 not_a_number\n"

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(body))
	}))
	defer server.Close()

	samples, err := fetchAllMetricSeries(server.URL, "metric_with_bad_suffix")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(samples) != 1 {
		t.Fatalf("expected 1 sample, got %d", len(samples))
	}
	if math.Abs(samples[0].Value-7.89) > 1e-9 {
		t.Fatalf("expected value 7.89, got %v", samples[0].Value)
	}
}
