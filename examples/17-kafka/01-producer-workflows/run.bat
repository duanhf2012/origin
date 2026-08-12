@echo off
setlocal
if "%ORIGIN_KAFKA_BROKERS%"=="" set "ORIGIN_KAFKA_BROKERS=192.168.8.3:9092"
cd /d "%~dp0..\..\.."
go run ./examples/17-kafka/01-producer-workflows start --app-name kafka-producer --config ./examples/17-kafka/01-producer-workflows/config --node kafka-producer-1
