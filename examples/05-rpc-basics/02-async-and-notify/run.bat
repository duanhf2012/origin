@echo off
setlocal
cd /d "%~dp0..\..\.."
go run ./examples/05-rpc-basics/02-async-and-notify start --app-name rpc-async --config ./examples/05-rpc-basics/02-async-and-notify/config --node game-1
