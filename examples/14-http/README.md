# 14 - 业务 HTTP

本章把 Gin Server 作为业务 Module，并用代码持有的 HTTP Client 调用同一个 Service 的 Safe 路由。

| 示例 | 学习目标 |
| --- | --- |
| [`01-gin-safe-self-call`](./01-gin-safe-self-call/README.md) | 普通/Safe 路由、两级鉴权、Server 配置、`Await` HTTP 自调用与 Client 所有权 |

示例采用“薄 Service + 业务 HTTP Module”的结构。路由、鉴权、业务状态和 HTTP Client 都集中在业务
Module；Service 只装配 Module，并用 Timer 发起一次运行期自调用。
