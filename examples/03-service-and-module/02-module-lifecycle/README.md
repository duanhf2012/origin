# Module 生命周期

Module 用于把一个 Service 内部的职责拆开，而不是创建独立调度器或配置根。示例的 Service 添加根 Module，根 Module 在自己的 `OnInit` 中再添加 child Module。

## 生命周期规则

初始化和启动按父到子顺序执行；停止按子到父顺序执行。因此 child 可以安全地依赖 root 初始化出的资源，而 root 的资源会等 child 停止后才释放。

## 运行与练习

执行 `run.bat` 或 `./run.sh`，按 `Ctrl+C` 后比较 `root` 与 `child` 的停止日志。可给 root 与 child 各增加一项独立状态，确认 Module 的状态只服务于所属 Service，不用于跨 Service 通信。

对应教程：[Service 与 Module](../../../docs/baseline/v3.0/guides/03-service-and-module.md)。
