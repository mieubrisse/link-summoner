#!/bin/bash

# Build script for link-summoner
set -e

# Create build directory if it doesn't exist
mkdir -p build

# Build the binary
echo "Building link-summoner..."
go build -o build/link-summoner .

echo "Build complete! Binary available at: build/link-summoner"