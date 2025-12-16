# slashmetrics

slashmetrics is a terminal-first Prometheus metric explorer that scrapes an endpoint, plots live data, and lets you pick the series you are interested in. Think Grafana Explore, but in your terminal.

## Demo

![slashmetrics Demo](demo.gif)

## Getting started
- **Requirements:** A Prometheus/compatible `/metrics` endpoint.
- **Build:** run `go build` to produce the `slashmetrics` binary (or `go install github.com/brennerm/slashmetrics@latest` for a global install).
- **Run:** `./slashmetrics --url http://localhost:9100/metrics --metric node_cpu_seconds_total --interval 2s`. If you omit `--metric`, the first metric discovered will be used and you can switch interactively.

## Controls
- `m`: open the metric selector, type `/` to filter, `Enter` to choose, `Esc/q` to cancel.
- `s`: choose which series (label combinations) are visible; use `Space` to toggle, `a` to toggle all, `Enter` to apply.
- `l`: toggle the legend panel that shows colored labels for the checked series.
- `r`: clear the chart and restart the data window.
- `q`/`Ctrl+C`: quit at any time.
