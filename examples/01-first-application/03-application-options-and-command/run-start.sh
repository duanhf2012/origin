#!/usr/bin/env sh
set -eu
cd "$(dirname "$0")/../../.."
go run ./examples/01-first-application/03-application-options-and-command start --app-name application-options --config ./examples/01-first-application/03-application-options-and-command/config
