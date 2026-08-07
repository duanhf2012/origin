@echo off
setlocal
cd /d "%~dp0..\..\.."
go test ./tests/integration/rpcfixture -run "^TestGeneratedTimeoutAndAsyncImmediateFailure$" -count=1 -v
