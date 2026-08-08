@echo off
setlocal
cd /d "%~dp0..\..\.."
go run ./examples/08-discovery/02-etcd-provider start --app-name etcd-discovery --config ./examples/08-discovery/02-etcd-provider/config --node game-1,game-2
