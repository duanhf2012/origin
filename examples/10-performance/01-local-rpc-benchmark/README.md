# 本地 RPC 基准

该基准测量真实生成客户端在同一 Node 内执行 `Await` RPC 的开销。它不是手写函数调用 microbenchmark，因此包含客户端准备、调度和生成编解码路径。

## 运行

执行 `run.bat` 或 `./run.sh`；脚本等价于：

```bash
# 跳过普通测试，只采样生成的同 Node Await RPC，并输出分配统计。
go test ./tests/integration/rpcfixture -run '^$' \
  -bench '^BenchmarkGeneratedLocalAwait$' -benchmem -benchtime=3s
```

## 如何阅读结果

`ns/op` 表示单次平均耗时，`B/op` 与 `allocs/op` 表示分配压力。重复运行三次以上，比较中位数；只与相同 Go 版本、机器、提交和采样参数的结果比较。

## 可修改实验

可增大 `-benchtime` 获得更稳定采样。不要用本地结果直接推断跨 Node 吞吐，它不包含网络、发现或传输调度开销。

对应教程：[性能测试与容量理解](../../../docs/baseline/v3.0/guides/10-performance.md)。
