@echo off
setlocal
cd /d "%~dp0..\..\.."
go test ./tests/integration/rpcfixture -run "^$" -bench "^BenchmarkGeneratedLocalAwait$" -benchmem -benchtime=3s
