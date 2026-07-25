@echo off
setlocal
pushd "%~dp0.."
if errorlevel 1 exit /b 1

set "CGO_ENABLED=0"
set "GOOS=linux"
set "GOARCH=amd64"

if not defined ORIGIN_BUILD_TIME for /f "delims=" %%I in ('powershell -NoProfile -Command "Get-Date -Format 'yyyy-MM-ddTHH:mm:ssK'"') do set "ORIGIN_BUILD_TIME=%%I"
if not defined ORIGIN_BUILD_VERSION for /f "delims=" %%I in ('git describe --tags --exact-match --dirty 2^>nul') do set "ORIGIN_BUILD_VERSION=%%I"
if not defined ORIGIN_BUILD_COMMIT for /f "delims=" %%I in ('git rev-parse --short HEAD 2^>nul') do set "ORIGIN_BUILD_COMMIT=%%I"

set "ORIGIN_LDFLAGS=-X=github.com/duanhf2012/origin/v3/buildinfo.buildTime=%ORIGIN_BUILD_TIME% -X=github.com/duanhf2012/origin/v3/buildinfo.version=%ORIGIN_BUILD_VERSION% -X=github.com/duanhf2012/origin/v3/buildinfo.commit=%ORIGIN_BUILD_COMMIT%"

echo Building linux/amd64...
if "%~1"=="" goto build_all
go build -v -ldflags "%ORIGIN_LDFLAGS%" %*
goto build_done

:build_all
go build -v -ldflags "%ORIGIN_LDFLAGS%" ./...

:build_done
if errorlevel 1 goto build_failed
echo Build succeeded.
popd
exit /b 0

:build_failed
set "ORIGIN_BUILD_EXIT=%ERRORLEVEL%"
echo Build failed.
popd
exit /b %ORIGIN_BUILD_EXIT%
