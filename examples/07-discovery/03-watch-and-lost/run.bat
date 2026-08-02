@echo off
setlocal
cd /d "%~dp0..\..\.."
go run ./examples/07-discovery/03-watch-and-lost start --app-name lost-demo --config ./examples/07-discovery/03-watch-and-lost/config --node watcher-1
