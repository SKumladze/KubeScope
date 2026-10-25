#!/usr/bin/env bash
# Deletes the "kubescope" kind cluster. Other kind clusters are left alone.
set -euo pipefail
kind delete cluster --name kubescope
