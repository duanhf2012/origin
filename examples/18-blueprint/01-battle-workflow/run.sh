#!/usr/bin/env sh
set -eu
cd "$(dirname "$0")/../../.."
exec go run ./examples/18-blueprint/01-battle-workflow start --app-name blueprint-example --config ./examples/18-blueprint/01-battle-workflow/config --node blueprint-1
