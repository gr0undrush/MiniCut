#!/usr/bin/env sh
# Builds ./minicut (Linux / macOS). Needs Go and a C compiler; see README.
set -e
cd "$(dirname "$0")"
[ -f go.sum ] || go mod tidy
go build -o minicut ./cmd/minicut
echo "Built ./minicut"
