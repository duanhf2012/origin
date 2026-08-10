#!/usr/bin/env sh
set -eu
cd "$(dirname "$0")/../../.."
exec go run \
  -ldflags "-X=github.com/duanhf2012/origin/v3/buildinfo.buildTime=2026-08-08T10:00:00+08:00 -X=github.com/duanhf2012/origin/v3/buildinfo.version=v3.0.0-demo -X=github.com/duanhf2012/origin/v3/buildinfo.commit=demo123" \
  ./examples/01-first-application/04-build-information version
