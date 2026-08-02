@echo off
setlocal
cd /d "%~dp0..\..\.."
go test ./tests/integration/rpcfixture -run "^$" -bench "^BenchmarkGeneratedRemoteLoopback$" -benchmem -benchtime=3s
