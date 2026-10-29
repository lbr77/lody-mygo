<p align="center">
  <img src="icon.png" width="96" alt="Lody Native">
</p>

<h1 align="center">Lody Native</h1>

<p align="center">
  <a href="https://lody.ai">Lody</a> 的原生桌面客户端,界面在 GPU 上直接绘制,不使用 WebView。
  <br>
  <a href="README.md">English</a> · <b>简体中文</b>
</p>

---

Lody Native 是用 Go 编写的 [Lody](https://github.com/LodyAI/Lody) 桌面客户端。界面由 [MyGo](https://github.com/egoist/mygo) 的 `ui` 包直接在 GPU 上绘制——不使用 WebKit、Electron 或任何 WebView——并以与 Lody 官方应用相同的方式连接 Lody Cloud。布局、设计 token、文案和动效都按 Lody 桌面版 1:1 还原。

> [!NOTE]
> Lody Native 是第三方项目,与 Lody 官方无关。协议实现参考自 [lody-ios](https://github.com/Innei/lody-ios) 和 Lody 开源的桌面应用。

## 功能

- **登录与工作区**:通过浏览器设备码流程登录,会话保存在 macOS 钥匙串;可切换、创建和加入工作区。
- **会话**:侧边栏按机器和项目分组,带 Lody 的「工作中 / 等待你 / 未读」标记、置顶、归档、分叉及其打开的会话。支持会话标签页、⌘K 命令面板(快捷键可自定义)、⌘F 对话内查找。
- **对话**:流式 Markdown、代码高亮、数学公式和 Mermaid 图(流程图、时序图、状态图、类图、ER 图、XY 图表);工具调用、思考过程、计划、diff、子任务和目标;权限请求与提问;Lody 的各类提示,从失败回合(模型繁忙时可重试)到定时任务与 Agent Role 提议;将消息分享为文字或图片。
- **输入框**:`@` 提及文件、issue、PR、会话和 Agent Role,`$` 技能,`/` 快捷指令与命令;附件;选择 Agent、模型、推理强度、权限模式和 MCP 服务器;快捷回复;在运行中排队或引导(steer);以及「定时发送」。
- **定时任务**:Lody 的定时任务——创建与编辑(每天、工作日、每周、每月、每几小时/分钟、只执行一次或手动),暂停、恢复、立即运行、删除,以及运行记录。
- **你的机器**:通过本机的 Lody daemon,提供每个会话的终端、文件浏览与编辑、HTML 与开发服务器预览、Git 状态与工作树。
- **仅连接本机(离线)**:「设置 → 偏好设置 → 仅连接本机 Lody(离线)」改为通过 daemon 的本地数据通道同步,不经过云端,断网也能用。
- **设置**:账号、外观(主题、语言、字号、全宽显示)、Agent 与服务商、MCP 服务器、Agent Role、记忆、快捷指令、项目、GitHub、用量、分享与账单(账单在 lody.ai 打开)。
- **英文与简体中文**,跟随系统或在设置中选择。

lody-ios 调用的全部接口都已实现,见 [docs/ios-api-parity.md](docs/ios-api-parity.md)。

## 安装

从 [Releases](https://github.com/lbr77/lody-mygo/releases) 下载磁盘映像,打开后把 **Lody Native** 拖到「应用程序」。需要 macOS 12 或更高版本,支持 Apple 芯片和 Intel。

你需要一个 Lody 账号,并在运行 Agent 的机器上运行 [Lody CLI](https://lody.ai/docs)。

## 从源码构建

需要 Go(版本见 `go.mod`;设置 `GOTOOLCHAIN=auto` 时会自动下载)和 Xcode 命令行工具:[loro-go](https://github.com/aholstenson/loro-go) 通过 cgo 链接预编译的 Rust 库。

```sh
CGO_ENABLED=1 go run .                                        # 运行
CGO_ENABLED=1 go test ./...                                   # 单元测试与无窗口 UI 测试
CGO_ENABLED=1 go tool mygo build                              # 为本机构建 .app 与 .dmg
CGO_ENABLED=1 go tool mygo build -platform darwin/universal   # 同时支持 Apple 芯片与 Intel
```

UI 测试在离屏渲染;把 `LODY_SNAPSHOTS` 设为一个目录,即可把画面保存为 PNG:

```sh
LODY_SNAPSHOTS=/tmp/snaps CGO_ENABLED=1 go test ./internal/app
```

文案在 `locales/`(`en.json`、`zh-Hans.json`);修改后运行 `go tool mygo generate` 生成访问函数。

## 发布

推送 `vX.Y.Z` 标签(须与 `mygo.json` 的 `version` 一致)后,[Release 工作流](.github/workflows/release.yml) 会在 macOS runner 上构建通用版应用,并把磁盘映像上传到一个草稿 Release,检查后再发布。手动运行工作流则只把磁盘映像保存为构建产物。

设置以下仓库 secrets 后,应用会用你的 Developer ID 签名,磁盘映像会经过公证并附上票据(staple);未设置时为 ad hoc 签名,在其他 Mac 上会被 Gatekeeper 拦截:

| Secret | 内容 |
| --- | --- |
| `MACOS_CERTIFICATE` | 「Developer ID Application」证书及其私钥导出的 `.p12`,base64 编码 |
| `MACOS_CERTIFICATE_PASSWORD` | 该 `.p12` 的密码 |
| `APPLE_ID` | 开发者账号的 Apple ID |
| `APPLE_TEAM_ID` | 团队 ID,即证书名称中的 `(TEAMID)` |
| `APPLE_APP_PASSWORD` | 该 Apple ID 的 [App 专用密码](https://account.apple.com) |

```sh
base64 -i certificate.p12 | gh secret set MACOS_CERTIFICATE
gh secret set MACOS_CERTIFICATE_PASSWORD
gh secret set APPLE_ID
gh secret set APPLE_TEAM_ID
gh secret set APPLE_APP_PASSWORD

# 修改 mygo.json 中的 "version" 并提交,然后:
git tag v0.2.0 && git push origin v0.2.0
```

## 实现原理

```
main.go               窗口(MyGo,hidden-inset 标题栏)
internal/app          状态与视图:侧边栏、对话、输入框、设置、定时任务……
internal/cloud        backend.lody.ai:设备码登录、账号、Convex、Streams grant、Machine RPC
internal/streams      Durable Streams:bootstrap(multipart)、long-poll、append、帧编解码、zstd
internal/catalog      工作区的 Flock 副本(<ws>:meta、wf:workspace、mf:<machine>)
internal/session      会话的 Loro 副本(<ws>:s:<id>):对话投影、发送、引导
internal/flock        flock-rs 的 wasm,在 wazero 中运行,wasm-bindgen 导入由 Go 实现
internal/flocksync    单个 Flock,经由云端流或 daemon 的数据通道同步
internal/localplane   Lody daemon 的本地 Loro 数据通道(协议 7)
internal/localterm    daemon 的终端与控制 socket
internal/schedule     定时任务:文档、Schedule Registry、指纹、cron
internal/collab       Code Collab:文件树、编辑、加密 RPC
internal/sharing      会话分享
locales               英文与简体中文文案
```

lody-ios 在离屏 WKWebView 中运行 Loro 和 Flock,Lody Native 则用原生方案:会话使用 [loro-go](https://github.com/aholstenson/loro-go);Flock 只以 `@loro-dev/flock-wasm` 形式发布,这里用纯 Go 的 WebAssembly 运行时 [wazero](https://wazero.io) 执行它,并用 Go 实现了它需要的 66 个 wasm-bindgen 导入。快照与 JSON 增量格式与官方实现逐字节一致。

## 限制

- 目前只提供 macOS 版本。
- 端到端加密(E2EE)工作区会显示为锁定:与 Lody 的网页版和手机版一样,Lody Native 目前还没有这类工作区的密钥托管。

## 许可

[AGPL-3.0-only](LICENSE)。内嵌的 `@loro-dev/flock-wasm` 为 AGPL-3.0-only(见 `internal/flock/wasm/LICENSE`),协议逻辑移植自 AGPL-3.0-only 的 lody-ios。图标来自 Lucide(ISC)与 Myna UI(MIT);Lody 标志归 Lody 所有。
