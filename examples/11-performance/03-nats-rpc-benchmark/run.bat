@echo off
setlocal
cd /d "%~dp0..\..\.."
go test ./tests/integration/rpcfixture -run "^$" -bench "^BenchmarkGeneratedNATSLoopback$" -benchmem -benchtime=3s
