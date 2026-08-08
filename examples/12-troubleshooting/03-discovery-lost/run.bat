@echo off
setlocal
cd /d "%~dp0..\..\.."
go run ./examples/08-discovery/03-watch-and-lost start --app-name lost-lab --config ./examples/08-discovery/03-watch-and-lost/config --node watcher-1
