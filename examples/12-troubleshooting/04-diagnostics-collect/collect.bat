@echo off
setlocal
set "ORIGIN_DIAGNOSTICS_OUTPUT=%~dp0diagnostics.json"
set "ORIGIN_DIAGNOSTICS_TEMP=%~dp0diagnostics.json.tmp"
del /q "%ORIGIN_DIAGNOSTICS_TEMP%" 2>nul
curl --fail --silent --show-error "http://127.0.0.1:6063/admin/v1/diagnostics?detail=full" -o "%ORIGIN_DIAGNOSTICS_TEMP%"
if errorlevel 1 (
  del /q "%ORIGIN_DIAGNOSTICS_TEMP%" 2>nul
  exit /b 1
)
move /y "%ORIGIN_DIAGNOSTICS_TEMP%" "%ORIGIN_DIAGNOSTICS_OUTPUT%" >nul
if errorlevel 1 exit /b 1
type "%ORIGIN_DIAGNOSTICS_OUTPUT%"
