# Origin 文档入口

文档分为“已发布基线”和“后续维护”两部分。不得把后续维护内容追加到已冻结基线。

| 版本 | 目录 | 状态 | 使用方式 |
| --- | --- | --- | --- |
| v3.0 基线 | [baseline/v3.0/](./baseline/v3.0/README.md) | 已发布、冻结 | v3.0 架构、实现过程、验收和迁移事实；仅允许勘误或修复失效链接。 |
| v3.0 维护 | [maintenance/v3.0/](./maintenance/v3.0/README.md) | 预留 | 已发布 v3.0 的补丁、兼容性和运维维护记录。 |
| v3.1 维护 | [maintenance/v3.1/](./maintenance/v3.1/README.md) | 进行中 | v3.1 的设计、计划、变更记录、报告和使用指南。 |
| v3.2 维护 | [maintenance/v3.2/](./maintenance/v3.2/README.md) | 预留 | v3.2 的独立维护空间。 |
| v3.2.1 维护 | [maintenance/v3.2.1/](./maintenance/v3.2.1/README.md) | 进行中 | v3.2.1 网络 Session 身份修正。 |
| v3.3 维护 | [maintenance/v3.3/](./maintenance/v3.3/README.md) | 进行中 | v3.3 的独立维护空间。 |

## 维护规则

1. 新需求和维护改动先写入 `maintenance/v{主版本}.{次版本}/design/`；未确认方案放入同级 `proposals/`。
2. 设计确认后才创建 `plans/`；实现完成后写入 `changes/` 和必要的 `reports/`。
3. 面向使用者的内容统一放入目标版本的 `guides/`，不与内部设计混放。
4. 每篇维护文档开头注明状态、目标版本、兼容性和依赖基线。例如：`基线：v3.0`、`目标：v3.1.0`。
5. 发布时将定版材料归入 `baseline/v{主版本}.{次版本}/` 并冻结；之后所有修复和演进进入对应 `maintenance/` 目录，不回填新的功能设计。
