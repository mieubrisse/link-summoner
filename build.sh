#!/bin/bash

set -euo pipefail
script_dirpath="$(cd "$(dirname "${0}")" && pwd)"

# Build script for link-summoner

# Create build directory if it doesn't exist
mkdir -p "${script_dirpath}/build"

# Build the binary
echo "Building link-summoner..."
cd "${script_dirpath}"
go build -o build/link-summoner .

echo "Build complete! Binary available at: build/link-summoner"