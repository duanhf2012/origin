@echo off
setlocal
if "%ORIGIN_REDIS_ADDRESS%"=="" set "ORIGIN_REDIS_ADDRESS=127.0.0.1:6379"
cd /d "%~dp0..\..\.."
go run ./examples/16-redis/04-distributed-lock start --app-name redis-lock --config ./examples/16-redis/04-distributed-lock/config --node redis-lock-1
