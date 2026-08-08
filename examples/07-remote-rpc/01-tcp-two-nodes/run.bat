@echo off
setlocal
cd /d "%~dp0..\..\.."
go run ./examples/07-remote-rpc/01-tcp-two-nodes start --app-name remote-tcp --config ./examples/07-remote-rpc/01-tcp-two-nodes/config --node discovery-1,player-1,gateway-1
