@echo off
setlocal
cd /d "%~dp0..\..\.."
go run ./examples/06-remote-rpc/03-route-and-broadcast start --app-name route-broadcast --config ./examples/06-remote-rpc/03-route-and-broadcast/config --node discovery-1,player-1,player-2,gateway-1
