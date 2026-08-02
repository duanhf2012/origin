@echo off
setlocal
cd /d "%~dp0..\..\.."
go run ./examples/05-rpc-basics/01-contract-generate-bind start --app-name rpc-bind --config ./examples/05-rpc-basics/01-contract-generate-bind/config --node game-1
