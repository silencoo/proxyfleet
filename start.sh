#!/bin/bash
# Ensure config files exist as regular files before starting Docker containers.
# Docker will create bind-mount targets as directories if they don't exist,
# which breaks the application.

# Fix config.yaml if it's a directory from a previous failed attempt
if [ -d "config.yaml" ]; then
  echo "WARNING: config.yaml is a directory (Docker bind-mount artifact). Removing and recreating as file..."
  rm -rf config.yaml
fi

# Fix nodes.txt if it's a directory from a previous failed attempt
if [ -d "nodes.txt" ]; then
  echo "WARNING: nodes.txt is a directory (Docker bind-mount artifact). Removing and recreating as file..."
  rm -rf nodes.txt
fi

# Create config.yaml if it doesn't exist
if [ ! -f "config.yaml" ]; then
  if [ -f "config.example.yaml" ]; then
    echo "INFO: Creating config.yaml from config.example.yaml"
    cp config.example.yaml config.yaml
  else
    echo "INFO: Creating empty config.yaml"
    touch config.yaml
  fi
fi

# Create nodes.txt if it doesn't exist
if [ ! -f "nodes.txt" ]; then
  echo "INFO: Creating empty nodes.txt"
  touch nodes.txt
fi

# Ensure config files are writable for WebUI settings
chmod 666 config.yaml nodes.txt 2>/dev/null || true

# Build the current checkout so this fork never silently starts the upstream
# prebuilt image referenced by older versions of docker-compose.yml.
docker compose down && docker compose up -d --build
