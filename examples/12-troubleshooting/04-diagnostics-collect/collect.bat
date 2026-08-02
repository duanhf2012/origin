@echo off
setlocal
curl http://127.0.0.1:6061/debug/origin/diagnostics -o "%~dp0diagnostics.json"
type "%~dp0diagnostics.json"
