# 术语表

- **Application**：本进程中 Origin 运行时和全部 Node 的边界。
- **Node**：稳定部署/通信身份，以及 Service 容器。
- **Service**：业务运行和串行调度的基本单元。
- **Module**：一个 Service 内部的生命周期组织单元。
- **Running**：默认可被路由选择的运行状态。
- **Retired**：仍运行但默认不参与自动选择的维护状态。
- **Await**：暂时释放当前 Service 执行权并等待操作完成。
- **Provider**：服务发现后端的可替换实现。
- **Diagnostics Snapshot**：某个时刻的只读运行状态聚合。
