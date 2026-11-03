#!/usr/bin/env bash
set -euo pipefail

if ! command -v go >/dev/null 2>&1; then
    printf 'Error: Go is required to install Promptcat. Install Go and try again.\n' >&2
    exit 1
fi

# Install this checkout regardless of the caller's working directory.
project_root="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd)"
cd -- "$project_root"
exec go install ./cmd/promptcat
