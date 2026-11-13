#!/usr/bin/env bash
set -o pipefail
cd /mnt/d/Desktop/go-zero2/apps/petshop || exit 1
CGO_ENABLED=1 GOFLAGS=-mod=mod go test -race ./internal/... ./e2e/ -count=1 -timeout 900s
