# NATS RPC 基准

该基准在测试进程内启动 NATS Server，再通过真实生成客户端执行 Await RPC。使用内嵌 Server 可以固定环境，先比较 NATS 传输路径本身，而不受外部 Docker 集群状态影响。

## 运行

执行 `run.bat` 或 `./run.sh`。测试自行管理内嵌 Server 生命周期，不需要运行 `deps-up`；输出字段与其他 Go benchmark 相同。

## 如何阅读结果

NATS 的请求路径、服务器调度和消息协议与 TCP 直连不同。某个负载下更快不意味着所有业务都应切换传输；还要考虑已有基础设施、故障恢复、运维能力和消息系统容量。

## 可修改实验

用与 TCP 基准完全一致的 Go 版本、机器与采样时长对比。若要测真实集群，请单独记录 NATS 拓扑、网络、认证和服务器版本。

对应教程：[性能测试与容量理解](../../../docs/baseline/v3.0/guides/11.performance.md)。
