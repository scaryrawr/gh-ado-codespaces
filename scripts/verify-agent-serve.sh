#!/usr/bin/env bash
set -euo pipefail

gofmt -w .
go test -race ./...
go vet ./...
go build -v .
git diff --check
