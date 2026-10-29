<p align="center">
  <img src="icon.png" width="96" alt="Lody Native">
</p>

<h1 align="center">Lody Native</h1>

<p align="center">
  A native desktop client for <a href="https://lody.ai">Lody</a>, drawn on the GPU — no web view.
  <br>
  <b>English</b> · <a href="README.zh-CN.md">简体中文</a>
</p>

---

Lody Native is a desktop client for [Lody](https://github.com/LodyAI/Lody), written in Go. Its interface is drawn directly on the GPU by the `ui` package of [MyGo](https://github.com/egoist/mygo) — there is no WebKit, Electron or any web view — and it talks to Lody Cloud the way Lody's own apps do. It follows Lody's desktop app one to one: its layout, design tokens, wording and motion.

> [!NOTE]
> Lody Native is an unofficial, third-party project, not affiliated with Lody. Its protocol work follows [lody-ios](https://github.com/Innei/lody-ios) and Lody's open-source desktop app.

## Features

- **Sign-in and workspaces** — sign in through the browser with Lody's device flow; the session is kept in the macOS Keychain. Switch, create and join workspaces.
- **Sessions** — the sidebar groups chats by machine and project, with Lody's working / waiting / unread marks, pins, archive, forks and the sessions they opened. Conversation tabs, a ⌘K command palette with rebindable shortcuts, and ⌘F to find in a chat.
- **Conversations** — streaming Markdown with code highlighting, math and Mermaid diagrams (flowchart, sequence, state, class, ER, XY charts); tool calls, thoughts, plans, diffs, subagents and goals; permission requests and questions; Lody's notices, from failed turns (with retry when a model is at capacity) to schedule and Agent Role proposals; sharing a message as text or an image.
- **Composer** — `@` mentions of files, issues, pull requests, sessions and Agent Roles, `$` skills, `/` prompt shortcuts and commands; attachments; the Agent, model, reasoning effort, permission mode and MCP servers; quick replies; queueing and steering a running turn; and *Schedule send*.
- **Schedules** — Lody's scheduled tasks: create and edit them (daily, weekdays, weekly, monthly, every few hours or minutes, once, or manual), pause, resume, run now, delete, and their run history.
- **Your machines** — through the Lody daemon on this computer: a terminal per session, the file explorer and editor, HTML and dev-server previews, Git state and worktrees.
- **Local-only mode** — *Settings → Preferences → Connect only to this computer's Lody* syncs with the daemon's local data plane instead of the cloud, so Lody Native works offline.
- **Settings** — account, appearance (theme, language, font size, full width), agents and providers, MCP servers, Agent Roles, memory, prompt shortcuts, projects, GitHub, usage, sharing and billing (which opens lody.ai).
- **English and Simplified Chinese**, following the system or chosen in Settings.

Everything lody-ios calls is implemented: see [docs/ios-api-parity.md](docs/ios-api-parity.md).

## Install

Download the disk image from [Releases](https://github.com/lbr77/lody-mygo/releases), open it and drag **Lody Native** to Applications. It runs on macOS 12 or later, on Apple silicon and Intel.

You need a Lody account, and the [Lody CLI](https://lody.ai/docs) running on the machines your agents work on.

## Build from source

You need Go (the version in `go.mod`; with `GOTOOLCHAIN=auto` Go fetches it) and the Xcode Command Line Tools: [loro-go](https://github.com/aholstenson/loro-go) links a prebuilt Rust library through cgo.

```sh
CGO_ENABLED=1 go run .                                        # run it
CGO_ENABLED=1 go test ./...                                   # unit tests and headless UI tests
CGO_ENABLED=1 go tool mygo build                              # build the .app and .dmg for this Mac
CGO_ENABLED=1 go tool mygo build -platform darwin/universal   # for Apple silicon and Intel
```

The UI tests render offscreen; set `LODY_SNAPSHOTS` to a folder to save their frames as PNGs:

```sh
LODY_SNAPSHOTS=/tmp/snaps CGO_ENABLED=1 go test ./internal/app
```

Strings live in `locales/` (`en.json`, `zh-Hans.json`); after changing them, `go tool mygo generate` writes their accessors.

## Releases

The [Release workflow](.github/workflows/release.yml) builds the universal app on a macOS runner when a tag `vX.Y.Z` is pushed — it must match `version` in `mygo.json` — and uploads the disk image to a draft release, to review and publish. Run it by hand to get the disk image as an artifact instead.

With these repository secrets, the app is signed with your Developer ID and the disk image notarized and stapled; without them it is signed ad hoc, which Gatekeeper blocks on other Macs:

| Secret | What it is |
| --- | --- |
| `MACOS_CERTIFICATE` | the *Developer ID Application* certificate with its private key, exported as `.p12`, in base64 |
| `MACOS_CERTIFICATE_PASSWORD` | the password of that `.p12` |
| `APPLE_ID` | the Apple ID of the developer account |
| `APPLE_TEAM_ID` | the team ID, as in the identity's `(TEAMID)` |
| `APPLE_APP_PASSWORD` | an [app-specific password](https://account.apple.com) of that Apple ID |

```sh
base64 -i certificate.p12 | gh secret set MACOS_CERTIFICATE
gh secret set MACOS_CERTIFICATE_PASSWORD
gh secret set APPLE_ID
gh secret set APPLE_TEAM_ID
gh secret set APPLE_APP_PASSWORD

# bump "version" in mygo.json, commit, then:
git tag v0.2.0 && git push origin v0.2.0
```

## How it works

```
main.go               the window (MyGo, hidden-inset title bar)
internal/app          state and views: sidebar, conversation, composer, settings, schedules…
internal/cloud        backend.lody.ai: device sign-in, account, Convex, Streams grants, machine RPC
internal/streams      Durable Streams: bootstrap (multipart), long poll, append, frames, zstd
internal/catalog      the workspace's Flock replicas (<ws>:meta, wf:workspace, mf:<machine>)
internal/session      a session's Loro replica (<ws>:s:<id>): the conversation, sending, steering
internal/flock        flock-rs's wasm, run in wazero, with its wasm-bindgen imports in Go
internal/flocksync    one Flock, over its stream or the daemon's data plane
internal/localplane   the Lody daemon's local Loro data plane (protocol 7)
internal/localterm    the daemon's terminals and control socket
internal/schedule     scheduled tasks: documents, the Schedule Registry, fingerprints, cron
internal/collab       Code Collab: file trees, editing, sealed RPC
internal/sharing      shared sessions
locales               English and Simplified Chinese strings
```

Where lody-ios runs Loro and Flock in an offscreen WKWebView, Lody Native runs them natively: sessions through [loro-go](https://github.com/aholstenson/loro-go), and Flock — published only as `@loro-dev/flock-wasm` — by running that wasm in [wazero](https://wazero.io), a WebAssembly runtime in pure Go, with the 66 wasm-bindgen imports it needs written in Go. Snapshots and JSON deltas match the official implementation byte for byte.

## Limitations

- macOS only, for now.
- Encrypted (E2EE) workspaces are shown as locked: like Lody's web and mobile apps, Lody Native has no key custody for them yet.

## License

[AGPL-3.0-only](LICENSE). The embedded `@loro-dev/flock-wasm` is AGPL-3.0-only (see `internal/flock/wasm/LICENSE`), and the protocol logic is ported from lody-ios, AGPL-3.0-only. Icons are from Lucide (ISC) and Myna UI (MIT). The Lody logo belongs to Lody.
