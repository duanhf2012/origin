@echo off
setlocal
cd /d "%~dp0..\..\.."
go run ./examples/14-http/01-gin-safe-self-call start --app-name gin-safe-self-call --config ./examples/14-http/01-gin-safe-self-call/config --node http-1
