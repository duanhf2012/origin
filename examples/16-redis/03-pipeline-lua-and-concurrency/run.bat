@echo off
setlocal
if "%ORIGIN_REDIS_ADDRESS%"=="" set "ORIGIN_REDIS_ADDRESS=127.0.0.1:6379"
cd /d "%~dp0..\..\.."
go run ./examples/16-redis/03-pipeline-lua-and-concurrency start --app-name redis-atomic --config ./examples/16-redis/03-pipeline-lua-and-concurrency/config --node redis-atomic-1
