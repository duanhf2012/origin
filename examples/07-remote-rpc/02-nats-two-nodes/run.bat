@echo off
setlocal
cd /d "%~dp0..\..\.."
go run ./examples/07-remote-rpc/02-nats-two-nodes start --app-name remote-nats --config ./examples/07-remote-rpc/02-nats-two-nodes/config --node discovery-1,player-1,gateway-1
