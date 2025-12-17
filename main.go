package main

import (
	"bufio"
	"fmt"
	"io"
	"net/http"
	"os"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/NimbleMarkets/ntcharts/canvas/runes"
	"github.com/NimbleMarkets/ntcharts/linechart/timeserieslinechart"
	"github.com/charmbracelet/bubbles/list"
	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/spf13/cobra"
)

// Styles
var (
	titleStyle = lipgloss.NewStyle().
			Bold(true).
			Foreground(lipgloss.Color("202"))

	borderStyle = lipgloss.NewStyle().
			BorderStyle(lipgloss.RoundedBorder()).
			BorderForeground(lipgloss.Color("202"))

	graphStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("202"))

	axisStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("245"))

	labelStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("15"))

	helpStyle = lipgloss.NewStyle().
			Background(lipgloss.Color("0")).
			Foreground(lipgloss.Color("15"))

	listItemStyle         = lipgloss.NewStyle().PaddingLeft(2)
	listSelectedItemStyle = lipgloss.NewStyle().PaddingLeft(2).Foreground(lipgloss.Color("202"))
	listTitleStyle        = lipgloss.NewStyle().MarginLeft(2).Bold(true).Foreground(lipgloss.Color("202"))
)

const (
	legendBoxWidth   = 35
	legendContentPad = 1
)

var (
	metricFlag   string
	intervalFlag time.Duration
	rootCmd      = &cobra.Command{
		Use:   "slashmetrics <url>",
		Short: "Terminal-based Prometheus metric explorer",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return runApp(args[0])
		},
	}
)

func init() {
	rootCmd.Flags().StringVar(&metricFlag, "metric", "", "The metric to visualize (if empty, a random metric will be chosen)")
	rootCmd.Flags().DurationVar(&intervalFlag, "interval", 2*time.Second, "The interval to poll for new metrics")
}

// MetricSample represents a single metric sample
type MetricSample struct {
	FullName string // Full metric name including labels
	Value    float64
}

// metricItem implements list.Item for the metric list
type metricItem string

func (i metricItem) FilterValue() string { return string(i) }

// metricDelegate is the list item delegate
type metricDelegate struct{}

func (d metricDelegate) Height() int                             { return 1 }
func (d metricDelegate) Spacing() int                            { return 0 }
func (d metricDelegate) Update(_ tea.Msg, _ *list.Model) tea.Cmd { return nil }
func (d metricDelegate) Render(w io.Writer, m list.Model, index int, listItem list.Item) {
	i, ok := listItem.(metricItem)
	if !ok {
		return
	}

	str := fmt.Sprintf("%d. %s", index+1, i)

	fn := listItemStyle.Render
	if index == m.Index() {
		fn = func(s ...string) string {
			return listSelectedItemStyle.Render("> " + strings.Join(s, " "))
		}
	}

	fmt.Fprint(w, fn(str))
}

// TickMsg signals time to fetch new metrics
type TickMsg time.Time

// MetricsMsg contains fetched metrics data
type MetricsMsg struct {
	Samples []MetricSample
	Err     error
}

// MetricsListMsg contains a list of all available metrics
type MetricsListMsg struct {
	Metrics []string
	Err     error
}

// seriesItem represents a data series with a checked state
type seriesItem struct {
	name     string
	checked  bool
	colorIdx int // Color index for this series
}

// Model is the bubbletea model
type Model struct {
	url                string
	metricName         string
	interval           time.Duration
	chart              timeserieslinechart.Model
	lastValues         map[string]float64                         // Map of series name to last value
	dataHistory        map[string][]timeserieslinechart.TimePoint // Store all data points per series
	lastUpdate         time.Time
	err                error
	width              int
	height             int
	selectMode         bool
	metricsList        list.Model
	seriesSelectMode   bool         // Whether in series selection mode
	seriesList         []seriesItem // List of available series
	seriesListScroll   int          // Scroll position in series list
	seriesListSelected int          // Currently selected item in series list
	showLegend         bool         // Whether to show the legend
	termWidth          int
	termHeight         int
	seriesColors       []lipgloss.Color // Colors for different series
	legendViewport     viewport.Model   // Viewport for scrolling legend entries
	yRangeSet          bool             // Whether Y range has been initialized
}

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
		valueStr := parts[len(parts)-1]
		val, err := strconv.ParseFloat(valueStr, 64)
		if err != nil {
			if len(parts) >= 3 {
				valueStr = parts[len(parts)-2]
				val, err = strconv.ParseFloat(valueStr, 64)
				if err != nil {
					continue
				}
			} else {
				continue
			}
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

	parts := strings.Fields(line)
	if len(parts) < 2 {
		return "", 0, false
	}

	// Last field is the value (sometimes timestamp follows, but we ignore it)
	valueStr := parts[len(parts)-1]

	// Check if second to last might be the value (if timestamp is present)
	val, err := strconv.ParseFloat(valueStr, 64)
	if err != nil {
		if len(parts) >= 3 {
			valueStr = parts[len(parts)-2]
			val, err = strconv.ParseFloat(valueStr, 64)
			if err != nil {
				return "", 0, false
			}
		} else {
			return "", 0, false
		}
	}

	// Extract metric name (everything before the space and value)
	name = parts[0]
	// If there are labels, extract just the base name for matching
	if idx := strings.Index(name, "{"); idx != -1 {
		return name[:idx], val, true
	}

	return name, val, true
}

// abs returns the absolute value of a float64
func abs(x float64) float64 {
	if x < 0 {
		return -x
	}
	return x
}

// fetchMetricCmd returns a command that fetches metrics
func fetchMetricCmd(url, metricName string) tea.Cmd {
	return func() tea.Msg {
		samples, err := fetchAllMetricSeries(url, metricName)
		return MetricsMsg{Samples: samples, Err: err}
	}
}

// fetchAllMetricsCmd returns a command that fetches all available metrics
func fetchAllMetricsCmd(url string) tea.Cmd {
	return func() tea.Msg {
		metrics, err := fetchAllMetrics(url)
		return MetricsListMsg{Metrics: metrics, Err: err}
	}
}

// tickCmd returns a command that ticks at the specified interval
func tickCmd(interval time.Duration) tea.Cmd {
	return tea.Tick(interval, func(t time.Time) tea.Msg {
		return TickMsg(t)
	})
}

// yLabelFormatter returns a label formatter that displays at least 2 decimal places for small values
func yLabelFormatter() func(int, float64) string {
	return func(idx int, v float64) string {
		if v == 0 {
			return "0.00"
		}
		absVal := v
		if absVal < 0 {
			absVal = -absVal
		}
		// For small values (< 1), always show 2 decimals
		if absVal < 1 {
			return fmt.Sprintf("%.2f", v)
		}
		// For medium values (< 100), show 2 decimals
		if absVal < 100 {
			return fmt.Sprintf("%.2f", v)
		}
		// For larger values, show fewer decimals
		if absVal < 1000 {
			return fmt.Sprintf("%.1f", v)
		}
		// For very large values, no decimals
		return fmt.Sprintf("%.0f", v)
	}
}

// redrawChart redraws the chart respecting series selection
func (m *Model) redrawChart() {
	// Clear all data from the chart
	m.chart.ClearAllData()
	m.chart.Clear()
	m.chart.DrawXYAxisAndLabel()

	// Rebuild chart with only checked series
	seriesIdx := 0
	// Use seriesList to maintain consistent order and colors
	for _, series := range m.seriesList {
		// Check if this series is checked (visible)
		if !series.checked {
			continue
		}

		// Get data for this series
		data, exists := m.dataHistory[series.name]
		if !exists {
			continue
		}

		// Set style for all datasets (all use named datasets now)
		colorIdx := series.colorIdx % len(m.seriesColors)
		style := lipgloss.NewStyle().Foreground(m.seriesColors[colorIdx])
		m.chart.SetDataSetStyle(series.name, style)
		m.chart.SetDataSetLineStyle(series.name, runes.ThinLineStyle)

		// Re-push all historical data points
		for _, point := range data {
			m.chart.PushDataSet(series.name, point)
		}
		seriesIdx++
	}

	// Draw the rebuilt chart
	m.chart.DrawAll()
}

func (m *Model) rebuildLegend() {
	legendContent := ""

	// Iterate through seriesList to maintain consistent order
	for _, series := range m.seriesList {
		// Only show checked series
		if !series.checked {
			continue
		}

		// Check if this series has data
		if _, exists := m.dataHistory[series.name]; !exists {
			continue
		}

		// Get color for this series
		colorIdx := series.colorIdx % len(m.seriesColors)
		color := m.seriesColors[colorIdx]

		// Create colored indicator
		indicator := lipgloss.NewStyle().Foreground(color).Render("■")

		// Extract only the labels part (between curly braces)
		legendLabel := series.name
		if idx := strings.Index(legendLabel, "{"); idx != -1 {
			legendLabel = legendLabel[idx:]
		} else {
			// If it's just the metric name without labels, show a simple identifier
			legendLabel = "{}"
		}

		// Add legend entry with truncation if too long
		if len(legendLabel) > 30 {
			legendLabel = legendLabel[:27] + "..."
		}
		legendContent += fmt.Sprintf("%s %s\n", indicator, legendLabel)
	}

	m.legendViewport.SetContent(legendContent)
}

func legendInnerDimensions(totalHeight int) (int, int) {
	width := max(legendBoxWidth-2-2*legendContentPad, 1)
	height := max(totalHeight-4, 1)
	return width, height
}

func newLegendViewport(totalHeight int) viewport.Model {
	width, height := legendInnerDimensions(totalHeight)
	viewportModel := viewport.New(width, height)
	viewportModel.MouseWheelEnabled = true
	return viewportModel
}

func (m *Model) updateLegendViewportSize() {
	if !m.showLegend {
		return
	}
	width, height := legendInnerDimensions(m.height)
	m.legendViewport.Width = width
	m.legendViewport.Height = height
}

// NewModel creates a new model
func NewModel(url, metricName string, interval time.Duration) Model {
	// Start with reasonable defaults
	width := 100
	height := 20

	chart := timeserieslinechart.New(width, height,
		timeserieslinechart.WithAxesStyles(axisStyle, labelStyle),
		timeserieslinechart.WithStyle(graphStyle),
		timeserieslinechart.WithLineStyle(runes.ThinLineStyle),
		timeserieslinechart.WithUpdateHandler(timeserieslinechart.SecondUpdateHandler(int(interval.Seconds()))),
		timeserieslinechart.WithXLabelFormatter(timeserieslinechart.HourTimeLabelFormatter()),
		timeserieslinechart.WithYLabelFormatter(yLabelFormatter()),
	)

	l := list.New([]list.Item{}, metricDelegate{}, 50, 20)
	l.Title = "Select a metric:"
	l.SetShowStatusBar(false)
	l.SetFilteringEnabled(true)
	l.Styles.Title = listTitleStyle

	return Model{
		url:         url,
		metricName:  metricName,
		interval:    interval,
		chart:       chart,
		width:       width,
		height:      height,
		selectMode:  false,
		metricsList: l,
		termWidth:   0,
		termHeight:  0,
		lastValues:  make(map[string]float64),
		dataHistory: make(map[string][]timeserieslinechart.TimePoint),
		seriesColors: []lipgloss.Color{
			"202", "46", "226", "201", "51", "208", "99", "171",
			"196", "33", "214", "40", "129", "39", "160", "45",
			"220", "135", "118", "200", "81", "227", "161", "48",
			"57", "190", "213", "38", "154", "124", "27", "141",
		},
		legendViewport: newLegendViewport(height),
		yRangeSet:      false,
	}
}

func (m Model) Init() tea.Cmd {
	m.chart.DrawXYAxisAndLabel()
	// Start by fetching metrics immediately and setting up tick
	return tea.Batch(
		fetchMetricCmd(m.url, m.metricName),
		tickCmd(m.interval),
	)
}

// resizeChart resizes the chart based on terminal dimensions
func (m *Model) resizeChart() {
	if m.termWidth == 0 || m.termHeight == 0 {
		return
	}

	headerFooterHeight := 9
	if m.err != nil {
		headerFooterHeight += 2
	}

	// Calculate chart dimensions
	chartWidth := m.termWidth - 6 // Account for borders and padding

	// If legend is shown, reduce chart width to make room for it
	if m.showLegend {
		chartWidth -= 38 // Legend width (35) + spacing (3)
	}

	chartHeight := m.termHeight - headerFooterHeight

	// Ensure minimum size
	if chartWidth < 40 {
		chartWidth = 40
	}
	if chartHeight < 10 {
		chartHeight = 10
	}

	// Only resize if dimensions changed significantly
	if chartWidth != m.width || chartHeight != m.height {
		m.width = chartWidth
		m.height = chartHeight

		// Use the built-in Resize function
		m.chart.Resize(m.width, m.height)

		m.chart.DrawXYAxisAndLabel()

		// Redraw with existing data
		if len(m.dataHistory) <= 1 {
			m.chart.Draw()
		} else {
			m.chart.DrawAll()
		}
	}

	m.updateLegendViewportSize()
}

func (m Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	var (
		cmd  tea.Cmd
		cmds []tea.Cmd
	)

	// Handle TickMsg and MetricsMsg regardless of mode to keep scraping active
	switch msg := msg.(type) {
	case TickMsg:
		// Fetch new metrics and schedule next tick
		return m, tea.Batch(
			fetchMetricCmd(m.url, m.metricName),
			tickCmd(m.interval),
		)
	case MetricsMsg:
		if msg.Err != nil {
			m.err = msg.Err
			return m, nil
		}

		m.err = nil
		m.lastUpdate = time.Now()

		// Validate that samples belong to the current metric
		// Extract base name from first sample to check
		if len(msg.Samples) > 0 {
			firstSample := msg.Samples[0].FullName
			baseName := firstSample
			if idx := strings.Index(firstSample, "{"); idx != -1 {
				baseName = firstSample[:idx]
			}
			// Ignore messages for the wrong metric (can happen when switching metrics)
			if baseName != m.metricName {
				return m, nil
			}
		}

		// Update series list when new samples arrive
		if len(msg.Samples) > 0 {
			// Check if we need to add new series to the list
			existingSeries := make(map[string]bool)
			for _, s := range m.seriesList {
				existingSeries[s.name] = true
			}

			for _, sample := range msg.Samples {
				displayName := sample.FullName
				if !existingSeries[displayName] {
					// Use the current length of seriesList as the colorIdx to ensure each series gets a unique color
					m.seriesList = append(m.seriesList, seriesItem{
						name:     displayName,
						checked:  true,
						colorIdx: len(m.seriesList),
					})
					existingSeries[displayName] = true
				}
			}
		}

		// Update Y range dynamically if needed (based on first sample)
		if len(msg.Samples) > 0 && !m.yRangeSet {
			// Initial setup - set a reasonable range based on all values
			minVal := msg.Samples[0].Value
			maxVal := msg.Samples[0].Value
			for _, sample := range msg.Samples {
				if sample.Value < minVal {
					minVal = sample.Value
				}
				if sample.Value > maxVal {
					maxVal = sample.Value
				}
			}

			minY := minVal * 0.9
			maxY := maxVal * 1.1

			// Handle edge cases
			if minY == maxY {
				// All values are the same, create a small range around the value
				if minVal == 0 {
					minY = -1
					maxY = 1
				} else {
					// Create a 10% range around the value
					delta := abs(minVal) * 0.1
					minY = minVal - delta
					maxY = maxVal + delta
				}
			}

			m.chart.SetYRange(minY, maxY)
			m.chart.SetViewYRange(minY, maxY)
			m.yRangeSet = true
		}

		// Process each sample and push to appropriate dataset
		for i, sample := range msg.Samples {
			m.lastValues[sample.FullName] = sample.Value

			point := timeserieslinechart.TimePoint{
				Time:  m.lastUpdate,
				Value: sample.Value,
			}

			// Use full name for all series (no special handling)
			displayName := sample.FullName

			// Check if this series is checked (visible) and get its color index
			isChecked := true
			colorIdx := i % len(m.seriesColors) // fallback if not found in seriesList
			if len(m.seriesList) > 0 {
				isChecked = false
				for _, s := range m.seriesList {
					if s.name == displayName {
						isChecked = s.checked
						colorIdx = s.colorIdx
						break
					}
				}
			}

			// Use full name for all series - all use named datasets now
			datasetName := displayName
			m.dataHistory[datasetName] = append(m.dataHistory[datasetName], point)

			// Set style for this dataset
			colorIdx = colorIdx % len(m.seriesColors)
			style := lipgloss.NewStyle().Foreground(m.seriesColors[colorIdx])
			m.chart.SetDataSetStyle(datasetName, style)
			m.chart.SetDataSetLineStyle(datasetName, runes.ThinLineStyle)

			if isChecked {
				m.chart.PushDataSet(datasetName, point)
			}
		}

		// Draw the chart (only if not in series selection mode)
		// Always use DrawAll() since all series now use named datasets
		if !m.seriesSelectMode {
			m.chart.DrawAll()
		}
		return m, nil
	}

	// If in series selection mode, handle series list
	if m.seriesSelectMode {
		switch msg := msg.(type) {
		case tea.KeyMsg:
			switch msg.String() {
			case "ctrl+c":
				return m, tea.Quit
			case "q", "esc":
				// Exit series selection mode without applying changes
				m.seriesSelectMode = false
				return m, nil
			case "enter":
				// Accept selection and exit series selection mode
				m.seriesSelectMode = false
				// Redraw chart with updated series visibility
				m.redrawChart()
				m.rebuildLegend()
				return m, nil
			case " ":
				// Toggle selected item
				if len(m.seriesList) > 0 && m.seriesListSelected < len(m.seriesList) {
					m.seriesList[m.seriesListSelected].checked = !m.seriesList[m.seriesListSelected].checked
				}
				return m, nil
			case "a":
				// Toggle select/unselect all
				allChecked := true
				for _, s := range m.seriesList {
					if !s.checked {
						allChecked = false
						break
					}
				}
				// If all checked, uncheck all; otherwise check all
				for i := range m.seriesList {
					m.seriesList[i].checked = !allChecked
				}
				return m, nil
			case "up":
				if m.seriesListSelected > 0 {
					m.seriesListSelected--
					// Adjust scroll if needed
					if m.seriesListSelected < m.seriesListScroll {
						m.seriesListScroll = m.seriesListSelected
					}
				}
				return m, nil
			case "down":
				if m.seriesListSelected < len(m.seriesList)-1 {
					m.seriesListSelected++
					// Adjust scroll if needed
					maxVisible := m.termHeight - 12
					if maxVisible < 3 {
						maxVisible = 3
					}
					if m.seriesListSelected >= m.seriesListScroll+maxVisible {
						m.seriesListScroll = m.seriesListSelected - maxVisible + 1
					}
				}
				return m, nil
			}
		}
		return m, nil
	}

	// If in select mode, let the list handle most messages
	if m.selectMode {
		switch msg := msg.(type) {
		case tea.KeyMsg:
			switch msg.String() {
			case "enter":
				// Switch to selected metric
				i, ok := m.metricsList.SelectedItem().(metricItem)
				if ok {
					m.metricName = string(i)

					// Recreate chart to clear all dataset configurations
					m.chart = timeserieslinechart.New(m.width, m.height,
						timeserieslinechart.WithAxesStyles(axisStyle, labelStyle),
						timeserieslinechart.WithStyle(graphStyle),
						timeserieslinechart.WithLineStyle(runes.ThinLineStyle),
						timeserieslinechart.WithUpdateHandler(timeserieslinechart.SecondUpdateHandler(int(m.interval.Seconds()))),
						timeserieslinechart.WithXLabelFormatter(timeserieslinechart.HourTimeLabelFormatter()),
						timeserieslinechart.WithYLabelFormatter(yLabelFormatter()),
					)
					m.chart.DrawXYAxisAndLabel()

					m.err = nil
					m.lastValues = make(map[string]float64)
					m.dataHistory = make(map[string][]timeserieslinechart.TimePoint)
					m.lastUpdate = time.Time{}
					m.yRangeSet = false
					m.seriesList = nil
					m.seriesListSelected = 0
					m.seriesListScroll = 0
				}
				m.metricsList.ResetFilter()
				m.rebuildLegend()
				m.selectMode = false
				return m, tea.Batch(
					fetchMetricCmd(m.url, m.metricName),
					tickCmd(m.interval),
				)
			case "ctrl+c":
				// Always allow ctrl+c to quit
				return m, tea.Quit
			case "q", "esc":
				// Only handle q and esc if not actively filtering
				if m.metricsList.FilterState() != list.Filtering {
					// Exit select mode and reset filter
					m.metricsList.ResetFilter()
					m.selectMode = false
					return m, nil
				}
			}
		case MetricsListMsg:
			if msg.Err != nil {
				m.err = msg.Err
				m.selectMode = false
				return m, nil
			}

			// Populate the list with metrics
			items := make([]list.Item, len(msg.Metrics))
			for i, metric := range msg.Metrics {
				items[i] = metricItem(metric)
			}
			m.metricsList.SetItems(items)
			return m, nil
		}

		// Pass all other messages to the list (including filter updates)
		m.metricsList, cmd = m.metricsList.Update(msg)
		return m, cmd
	}

	// Normal mode message handling
	switch msg := msg.(type) {
	case tea.KeyMsg:
		switch msg.String() {
		case "q", "ctrl+c":
			return m, tea.Quit
		case "m":
			// Enter metric select mode - fetch metrics first
			m.selectMode = true
			return m, fetchAllMetricsCmd(m.url)
		case "l":
			// Toggle legend display
			m.showLegend = !m.showLegend
			m.rebuildLegend()
			// Resize chart to accommodate legend
			m.resizeChart()
		case "s":
			// Enter series selection mode
			if len(m.dataHistory) > 0 {
				m.seriesSelectMode = true
				m.seriesListSelected = 0
				m.seriesListScroll = 0
			}
		case "r":
			// Reset the chart
			m.chart.ClearAllData()
			m.chart.Clear()
			m.chart.DrawXYAxisAndLabel()
			return m, nil
		}

	case tea.WindowSizeMsg:
		m.termWidth = msg.Width
		m.termHeight = msg.Height
		if !m.selectMode {
			m.resizeChart()
		} else {
			// Resize the list too
			m.metricsList.SetSize(msg.Width-4, msg.Height-10)
		}
	}

	if m.showLegend {
		m.legendViewport, cmd = m.legendViewport.Update(msg)
		cmds = append(cmds, cmd)
	}

	return m, tea.Batch(cmds...)
}

func (m Model) View() string {
	var sb strings.Builder

	// ASCII art logo
	logo := lipgloss.NewStyle().Foreground(lipgloss.Color("202")).Render(
		"     __            __      _          \n" +
			"    / / __ _  ___ / /_____(_)______   \n" +
			"   / / /  ' \\/ -_) __/ __/ / __(_-<   \n" +
			"  /_/ /_/_/_/\\__/\\__/_/ /_/\\__/___/   \n")

	// Title section with logo and metric info
	titleText := titleStyle.Render(fmt.Sprintf("   Metric: %s", m.metricName))
	subtitleText := helpStyle.Render(fmt.Sprintf("   URL: %s | Interval: %s", m.url, m.interval))

	header := lipgloss.JoinHorizontal(
		lipgloss.Top,
		logo,
		lipgloss.NewStyle().PaddingTop(2).Render(
			lipgloss.JoinVertical(
				lipgloss.Left,
				titleText,
				subtitleText,
			)),
	)

	sb.WriteString(header)
	sb.WriteString("\n")

	// Show select mode if active
	if m.selectMode {
		sb.WriteString(m.metricsList.View())
		sb.WriteString("\n")
		sb.WriteString(helpStyle.Render("Press Enter to select, Esc/q to cancel, / to filter"))
		return sb.String()
	}

	// Show series selection mode if active
	if m.seriesSelectMode {
		sb.WriteString(titleStyle.Render("\nSelect Series to Display:"))
		sb.WriteString("\n\n")

		// Calculate visible range
		maxVisible := m.termHeight - 12
		if maxVisible < 3 {
			maxVisible = 3
		}

		start := m.seriesListScroll
		end := start + maxVisible
		if end > len(m.seriesList) {
			end = len(m.seriesList)
		}

		// Render visible items
		for i := start; i < end; i++ {
			sel := " "
			if i == m.seriesListSelected {
				sel = ">"
			}
			check := " "
			if m.seriesList[i].checked {
				check = "✓"
			}
			line := fmt.Sprintf("%s [%s] %s", sel, check, m.seriesList[i].name)
			if i == m.seriesListSelected {
				sb.WriteString(listSelectedItemStyle.Render(line))
			} else {
				sb.WriteString(listItemStyle.Render(line))
			}
			sb.WriteString("\n")
		}

		sb.WriteString("\n")
		sb.WriteString(helpStyle.Render("Space: Toggle | Enter: Accept | a: Toggle All | Esc/q: Cancel | ↑↓: Navigate"))
		return sb.String()
	}

	// Error display
	if m.err != nil {
		sb.WriteString(lipgloss.NewStyle().Foreground(lipgloss.Color("196")).Render(fmt.Sprintf("⚠️  Error: %v", m.err)))
		sb.WriteString("\n\n")
	}

	// Chart and Legend
	chartView := borderStyle.Render(m.chart.View())

	if m.showLegend && len(m.seriesList) > 0 {
		m.updateLegendViewportSize()
		legendHeader := titleStyle.Render("Legend") + "\n"
		legendView := m.legendViewport.View()

		legend := lipgloss.JoinVertical(
			lipgloss.Left,
			legendHeader,
			legendView,
		)

		legend = lipgloss.NewStyle().
			BorderStyle(lipgloss.RoundedBorder()).
			BorderForeground(lipgloss.Color("202")).
			Padding(1).
			Width(legendBoxWidth).
			Height(m.height).
			Render(legend)

		// Join chart and legend horizontally
		chartAndLegend := lipgloss.JoinHorizontal(lipgloss.Top, chartView, " ", legend)
		chartWithMargin := lipgloss.NewStyle().MarginLeft(2).MarginRight(2).Render(chartAndLegend)
		sb.WriteString(chartWithMargin)
	} else {
		chartWithMargin := lipgloss.NewStyle().MarginLeft(2).MarginRight(2).Render(chartView)
		sb.WriteString(chartWithMargin)
	}

	// Calculate remaining vertical space to push help bar to bottom
	// Count lines: logo (4) + 1 newlines after header + chart (m.height) + chart borders (~2)
	// The title section adds to logo lines (JoinHorizontal keeps max height)
	usedLines := 4 + 1 + m.height + 2 + 0          // +1 for help bar
	remainingLines := m.termHeight - usedLines - 0 // -3 to account for the extra lines
	if remainingLines > 0 {
		sb.WriteString(strings.Repeat("\n", remainingLines))
	}

	// Help
	keyStyle := lipgloss.NewStyle().Background(lipgloss.Color("237")).Foreground(lipgloss.Color("15")).Bold(true)
	valStyle := lipgloss.NewStyle().Background(lipgloss.Color("15")).Foreground(lipgloss.Color("0"))

	helpContent := keyStyle.Render("q") + valStyle.Render("Quit") + "  " +
		keyStyle.Render("m") + valStyle.Render("Metrics") + "  " +
		keyStyle.Render("s") + valStyle.Render("Series") + "  " +
		keyStyle.Render("l") + valStyle.Render("Legend") + "  " +
		keyStyle.Render("r") + valStyle.Render("Reset")
	if m.showLegend && m.legendViewport.TotalLineCount() > m.legendViewport.VisibleLineCount() {
		helpContent += "  " + keyStyle.Render("↑↓") + valStyle.Render("Scroll")
	}

	helpBar := lipgloss.NewStyle().
		Background(lipgloss.Color("15")).
		Foreground(lipgloss.Color("0")).
		Width(m.termWidth).
		Render(helpContent)
	sb.WriteString(helpBar)

	return sb.String()
}

func runApp(url string) error {
	selectedMetric := metricFlag
	if selectedMetric == "" {
		metrics, err := fetchAllMetrics(url)
		if err != nil {
			return fmt.Errorf("error fetching metrics: %w", err)
		}
		if len(metrics) == 0 {
			return fmt.Errorf("no metrics found at the endpoint")
		}
		selectedMetric = metrics[0]
	}

	m := NewModel(url, selectedMetric, intervalFlag)
	p := tea.NewProgram(m, tea.WithAltScreen(), tea.WithMouseAllMotion())

	if len(os.Getenv("DEBUG")) > 0 {
		f, err := tea.LogToFile("debug.log", "debug")
		if err != nil {
			fmt.Println("fatal:", err)
			os.Exit(1)
		}
		defer f.Close()
	}

	if _, err := p.Run(); err != nil {
		return err
	}

	return nil
}

func main() {
	if err := rootCmd.Execute(); err != nil {
		fmt.Printf("Error: %v\n", err)
		os.Exit(1)
	}
}
