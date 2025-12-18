package main

import (
	"bufio"
	"fmt"
	"net/http"
	"sort"
	"strconv"
	"strings"
)

// fetchAllMetrics fetches all available metric names from the endpoint
func fetchAllMetrics(url string) ([]string, error) {
	resp, err := http.Get(url)
	if err != nil {
		return nil, fmt.Errorf("failed to fetch metrics: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("unexpected status code: %d", resp.StatusCode)
	}

	metrics := make(map[string]bool)
	scanner := bufio.NewScanner(resp.Body)
	for scanner.Scan() {
		line := scanner.Text()

		// Skip comments and empty lines
		if strings.HasPrefix(line, "#") || len(strings.TrimSpace(line)) == 0 {
			continue
		}

		// Extract metric name
		name, _, ok := parseMetricLine(line)
		if ok {
			metrics[name] = true
		}
	}

	// Convert map to sorted slice
	result := make([]string, 0, len(metrics))
	for name := range metrics {
		result = append(result, name)
	}
	sort.Strings(result)

	return result, nil
}

// fetchAllMetricSeries fetches all series for a specific metric from the Prometheus endpoint
func fetchAllMetricSeries(url, metricName string) ([]MetricSample, error) {
	resp, err := http.Get(url)
	if err != nil {
		return nil, fmt.Errorf("failed to fetch metrics: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("unexpected status code: %d", resp.StatusCode)
	}

	var samples []MetricSample
	scanner := bufio.NewScanner(resp.Body)
	for scanner.Scan() {
		line := scanner.Text()

		// Skip comments and empty lines
		if strings.HasPrefix(line, "#") || len(strings.TrimSpace(line)) == 0 {
			continue
		}

		// Parse metric line
		parts := strings.Fields(line)
		if len(parts) < 2 {
			continue
		}

		fullName := parts[0]
		baseName := fullName

		// Extract base name if labels present
		if idx := strings.Index(fullName, "{"); idx != -1 {
			baseName = fullName[:idx]
		}

		// Check if this is the metric we're looking for
		if baseName != metricName {
			continue
		}

		// Parse value
		valueStr := parts[1]
		val, err := strconv.ParseFloat(valueStr, 64)
		if err != nil {
			continue
		}

		// If no labels, add empty labels
		if !strings.Contains(fullName, "{") {
			fullName = fullName + "{}"
		}

		samples = append(samples, MetricSample{
			FullName: fullName,
			Value:    val,
		})
	}

	if len(samples) == 0 {
		return nil, fmt.Errorf("metric %q not found", metricName)
	}

	return samples, nil
}

// parseMetricLine parses a single Prometheus metric line
func parseMetricLine(line string) (name string, value float64, ok bool) {
	// Handle metric with labels: metric_name{label="value"} 123.45
	// Handle metric without labels: metric_name 123.45
	// Handle optional timestamp at the end: metric_name{label="value"} 123.45 1627847261

	parts := strings.Fields(line)
	if len(parts) < 2 {
		return "", 0, false
	}

	// second field is the value (sometimes timestamp follows, but we ignore it)
	valueStr := parts[1]

	// Check if second to last might be the value (if timestamp is present)
	val, err := strconv.ParseFloat(valueStr, 64)
	if err != nil {
		return "", 0, false
	}

	// Extract metric name (everything before the space and value)
	name = parts[0]
	// If there are labels, extract just the base name for matching
	if before, _, ok0 := strings.Cut(name, "{"); ok0 {
		return before, val, true
	}

	return name, val, true
}
