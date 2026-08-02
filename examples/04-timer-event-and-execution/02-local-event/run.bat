@echo off
setlocal
cd /d "%~dp0..\..\.."
go run ./examples/04-timer-event-and-execution/02-local-event start --app-name event-demo --config ./examples/04-timer-event-and-execution/02-local-event/config --node game-1
