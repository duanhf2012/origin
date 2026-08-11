@echo off
setlocal
if "%ORIGIN_REDIS_ADDRESS%"=="" set "ORIGIN_REDIS_ADDRESS=127.0.0.1:6379"
cd /d "%~dp0..\..\.."
go run ./examples/16-redis/01-cache-and-session start --app-name redis-cache --config ./examples/16-redis/01-cache-and-session/config --node redis-cache-1
