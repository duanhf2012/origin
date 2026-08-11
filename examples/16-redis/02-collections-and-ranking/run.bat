@echo off
setlocal
if "%ORIGIN_REDIS_ADDRESS%"=="" set "ORIGIN_REDIS_ADDRESS=127.0.0.1:6379"
cd /d "%~dp0..\..\.."
go run ./examples/16-redis/02-collections-and-ranking start --app-name redis-collections --config ./examples/16-redis/02-collections-and-ranking/config --node redis-collections-1
