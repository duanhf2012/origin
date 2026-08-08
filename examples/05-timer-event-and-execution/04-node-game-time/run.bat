@echo off
setlocal
cd /d "%~dp0..\..\.."
go run ./examples/05-timer-event-and-execution/04-node-game-time start --app-name node-game-time-demo --config ./examples/05-timer-event-and-execution/04-node-game-time/config --node game-1
