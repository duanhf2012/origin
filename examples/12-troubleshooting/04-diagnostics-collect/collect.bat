@echo off
setlocal
curl "http://127.0.0.1:6063/admin/v1/diagnostics?detail=full" -o "%~dp0diagnostics.json"
type "%~dp0diagnostics.json"
