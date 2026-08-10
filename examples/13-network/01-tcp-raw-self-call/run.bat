@echo off
setlocal
cd /d "%~dp0..\..\.."
go run ./examples/13-network/01-tcp-raw-self-call start --app-name tcp-raw-self-call --config ./examples/13-network/01-tcp-raw-self-call/config --node network-1
