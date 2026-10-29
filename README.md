# Lody Native

一个原生的 [Lody](https://github.com/LodyAI/Lody) 桌面客户端:界面由 [MyGo](https://github.com/egoist/mygo) 的 `ui` 包在 GPU 上直接绘制,**不使用 WebKit / 任何 WebView**,直接连接 Lody Cloud 的端点同步数据。样式按 Lody 桌面版的设计 token 还原(Lody Light / Vesper deep-sea 配色、侧边栏 + 工作区卡片布局、墨色主按钮、对话节奏)。

> 第三方项目,与 Lody 官方无关。协议实现参考自 [lody-ios](https://github.com/Innei/lody-ios)。

## 功能

- 浏览器设备码登录(Better Auth Device Flow,`lody-cli` client),会话令牌存在 macOS 钥匙串
- 工作区切换;按项目分组的会话侧边栏,带「工作中 / 等待你 / 未读」状态标记
- 实时同步会话:用户气泡、Markdown 回复(流式)、可折叠的工具调用组、思考过程、计划、文件变更
- 发送消息(Agent 忙时自动排队)、停止运行、回应权限请求
- 新建对话:选择机器 / Agent / 项目后发送首条消息
- 跟随系统的浅色 / 深色模式;目录缓存让冷启动立即显示上次的侧边栏

## 架构

```
main.go                  MyGo 原生窗口(hidden-inset 标题栏)
internal/app             状态 + 视图(登录、侧边栏、对话、输入框、新建对话、主题、图标)
internal/cloud           backend.lody.ai:设备码登录、账号/工作区、Streams grant、Machine RPC
internal/streams         Durable Streams 客户端:bootstrap(multipart)、long-poll、append、帧编解码、zstd
internal/catalog         Flock 目录副本(<ws>:meta、<ws>:mf:<machine>)→ 项目/会话/机器/Agent
internal/session         Loro 会话副本(<ws>:s:<id>,loro-go)→ 对话投影;发送/排队/停止/权限
internal/flock           flock-rs 的 Go 绑定:官方 wasm 跑在 wazero 里,纯 Go 实现 wasm-bindgen 导入
```

lody-ios 在一个离屏 WKWebView 里运行 Loro/Flock;这里换成了原生方案:

- **会话(Loro)**:`github.com/aholstenson/loro-go`(cgo + 预编译 Rust 静态库)
- **目录(Flock)**:flock-rs 没有公开源码,只有 `@loro-dev/flock-wasm`。`internal/flock` 把它的 wasm 嵌进二进制,用 [wazero](https://wazero.io)(纯 Go 的 WebAssembly 运行时)执行,并用 Go 实现了它需要的 66 个 wasm-bindgen 导入(JS 值堆、对象/数组/Map、迭代器……)。快照 / JSON 增量格式与官方实现逐字节一致。

## 构建与运行

需要 Go 1.27.3+(`GOTOOLCHAIN=auto` 会自动下载)和 Xcode 命令行工具(loro-go 需要 cgo)。

```sh
CGO_ENABLED=1 go run .                    # 开发运行
CGO_ENABLED=1 go test ./...               # 单元测试 + 无窗口 UI 测试
CGO_ENABLED=1 go build -trimpath -ldflags "-s -w" -o build/lody-native .
CGO_ENABLED=1 go tool mygo build          # 打包 .app / .dmg(需要先 go get -tool github.com/egoist/mygo/cmd/mygo)
```

UI 测试可以导出截图:`LODY_SNAPSHOTS=/some/dir CGO_ENABLED=1 go test ./internal/app`。

## 尚未实现

附件/图片上传、`$` 快捷指令与 `@` 提及展开、MCP 服务器选择、模型/模式切换、steer(引导正在运行的回合)、归档/重命名/置顶操作的界面入口、文件浏览与 diff 查看器、预览隧道、机器在线状态(presence)。

## 许可

AGPL-3.0-only。内嵌的 `@loro-dev/flock-wasm` 为 AGPL-3.0-only(见 `internal/flock/wasm/LICENSE`),协议逻辑移植自 AGPL-3.0-only 的 lody-ios。图标来自 Lucide(ISC)与 Myna UI(MIT);Lody 标志归 Lody 所有。
