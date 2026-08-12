# Blueprint Module 示例

| 目录 | 重点 | 建议顺序 |
| --- | --- | --- |
| [01-battle-workflow](./01-battle-workflow/README.md) | 业务 Module 组合、节点注册、一次性/长期执行、异步节点、完成回调与热加载 | 先运行 |

示例不依赖数据库或消息中间件。蓝图节点定义位于 `blueprints/nodes`，蓝图文件位于
`blueprints/graphs`；运行时不会修改这两个目录。
