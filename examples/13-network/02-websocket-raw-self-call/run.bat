@echo off
setlocal
cd /d "%~dp0..\..\.."
go run ./examples/13-network/02-websocket-raw-self-call start --app-name websocket-raw-self-call --config ./examples/13-network/02-websocket-raw-self-call/config --node network-1
