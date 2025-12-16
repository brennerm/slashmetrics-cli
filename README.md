# slashmetrics

slashmetrics is a terminal-first Prometheus metric explorer that scrapes an endpoint, plots live data, and lets you pick the series you are interested in. Think Grafana Explore, but in your terminal.

## Demo

![slashmetrics Demo](demo.gif)

## Getting started
- **Requirements:** A Prometheus/compatible `/metrics` endpoint.
- **Build:** run `go build` to produce the `slashmetrics` binary (or `go install github.com/brennerm/slashmetrics@latest` for a global install).
- **Run:** `./slashmetrics http://localhost:9100/metrics --metric node_cpu_seconds_total --interval 1s`
