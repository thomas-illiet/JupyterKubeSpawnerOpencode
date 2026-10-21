#!/bin/sh
set -eu

exec /opt/conda/bin/python3 /usr/local/libexec/opencode-selector.py "$@"
