<div align="center">

<img src="../docs/assets/readme/banner.png" alt="Orchestrator.inc" width="100%" />

<h3>别再照看智能体，开始合并真正的成果。</h3>

<p>
  运行一整支编程智能体队伍，同时让分支、评审和 CI 失败始终可控。
</p>

[![GitHub stars](https://img.shields.io/github/stars/OrchestratorInc/agent-orchestrator?style=flat&logo=github&color=1A9BF0)](https://github.com/OrchestratorInc/agent-orchestrator/stargazers)
[![GitHub release](https://img.shields.io/github/v/release/OrchestratorInc/agent-orchestrator?style=flat&logo=github&color=1A9BF0)](https://github.com/OrchestratorInc/agent-orchestrator/releases/latest)
[![GitHub downloads](https://img.shields.io/github/downloads/OrchestratorInc/agent-orchestrator/total?style=flat&logo=github&color=1A9BF0)](https://github.com/OrchestratorInc/agent-orchestrator/releases)
[![License: Apache-2.0](https://img.shields.io/badge/license-Apache--2.0-0B1A33?style=flat)](../LICENSE)
[![X](https://img.shields.io/badge/@useOrchestrator-0B1A33?style=flat&logo=x&logoColor=white)](https://x.com/useOrchestrator)
[![Discord](https://img.shields.io/badge/Discord-5865F2?style=flat&logo=discord&logoColor=white)](https://discord.com/invite/UZv7JjxbwG)

<br />

[**获取 Orchestrator.inc**](https://orchestrator.inc) &nbsp;&bull;&nbsp; [文档](https://docs.orchestrator.inc) &nbsp;&bull;&nbsp; [版本](https://github.com/OrchestratorInc/agent-orchestrator/releases) &nbsp;&bull;&nbsp; [参与贡献](../CONTRIBUTING.md) &nbsp;&bull;&nbsp; [Discord](https://discord.com/invite/UZv7JjxbwG)

[English](../README.md) · **简体中文** · [日本語](README.ja.md) · [한국어](README.ko.md) · [Español](README.es.md) · [Français](README.fr.md) · [Deutsch](README.de.md) · [Português (Brasil)](README.pt-BR.md)

<br />

<img src="../docs/assets/readme/hero.png" alt="Orchestrator.inc 看板，按实时状态分组显示 worker 会话" width="100%" />

</div>

<br />

## 为什么选择 Orchestrator.inc

一个编程智能体可以完成一项任务。同时运行多个智能体，就意味着要拆分工作、避免分支冲突，并跟踪每项变更走完 CI、评审和合并。Orchestrator.inc 正是为此打造的本地桌面工作区：每项任务都有自己的智能体、分支和 worktree，你的代码始终留在你自己的机器上。

## 功能

### 编排器统领整支队伍

告诉项目的主智能体你想构建什么。它会规划工作，为每个部分在独立的 worktree 中启动一个 worker，只在需要你时才回来找你。

<img src="../docs/assets/readme/site/orchestrator.jpg" alt="编排器将一个计费功能拆分给三个 worker，每个 worker 在各自的 worktree 中使用不同的编程智能体" width="100%" />

### 一个看板，管理所有会话

会话会根据智能体、PR、CI 和评审状态自动归入各个泳道，需要你处理的内容始终排在最上面。

<img src="../docs/assets/readme/site/board.jpg" alt="包含 Working、Needs you、In review 和 Ready to merge 泳道的看板" width="100%" />

### CI 失败，由对应的智能体修复

失败的检查、评审意见和合并冲突会交还给负责该分支的 worker，直到 PR 获得批准。评审智能体可以在人工评审之前先审阅每个 PR。

<img src="../docs/assets/readme/site/feedback.jpg" alt="拉取请求时间线：失败的检查和评审意见被交还给 Claude Code，由它推送修复" width="100%" />

### 使用你已经信任的智能体

为每项任务选择智能体和模型，然后在结构化 Chat 或智能体原生的终端界面中工作。每个 worker 还配有一个浏览器，用于预览和测试其应用。

#### 支持的智能体

各智能体对 Worker、Chat 和评审者的支持情况不同；当前的支持矩阵请查看[智能体设置指南](https://docs.orchestrator.inc/plugins/agents)。

<table>
  <tr valign="middle">
    <td width="33%" valign="middle" nowrap><img src="../frontend/src/renderer/assets/agents/claude-code.svg" alt="Claude Code" width="24" height="24" align="middle" /> &nbsp; <b>Claude Code</b></td>
    <td width="33%" valign="middle" nowrap><img src="../frontend/src/renderer/assets/agents/codex.svg" alt="Codex" width="24" height="24" align="middle" /> &nbsp; <b>Codex</b></td>
    <td width="33%" valign="middle" nowrap><picture><source media="(prefers-color-scheme: dark)" srcset="../frontend/src/renderer/assets/agents/cursor-dark.svg" /><img src="../frontend/src/renderer/assets/agents/cursor-light.svg" alt="Cursor" width="24" height="24" align="middle" /></picture> &nbsp; <b>Cursor</b></td>
  </tr>
  <tr valign="middle">
    <td valign="middle" nowrap><picture><source media="(prefers-color-scheme: dark)" srcset="../frontend/src/renderer/assets/agents/opencode-dark.svg" /><img src="../frontend/src/renderer/assets/agents/opencode-light.svg" alt="opencode" width="24" height="24" align="middle" /></picture> &nbsp; <b>opencode</b></td>
    <td valign="middle" nowrap><img src="../frontend/src/renderer/assets/agents/aider.svg" alt="Aider" width="24" height="24" align="middle" /> &nbsp; <b>Aider</b></td>
    <td valign="middle" nowrap><picture><source media="(prefers-color-scheme: dark)" srcset="../frontend/src/renderer/assets/agents/copilot-dark.svg" /><img src="../frontend/src/renderer/assets/agents/copilot-light.svg" alt="GitHub Copilot" width="24" height="24" align="middle" /></picture> &nbsp; <b>GitHub Copilot</b></td>
  </tr>
  <tr valign="middle">
    <td valign="middle" nowrap><picture><source media="(prefers-color-scheme: dark)" srcset="../frontend/src/renderer/assets/agents/grok-dark.svg" /><img src="../frontend/src/renderer/assets/agents/grok-light.svg" alt="Grok" width="24" height="24" align="middle" /></picture> &nbsp; <b>Grok</b></td>
    <td valign="middle" nowrap><picture><source media="(prefers-color-scheme: dark)" srcset="../frontend/src/renderer/assets/agents/kimi-dark.svg" /><img src="../frontend/src/renderer/assets/agents/kimi-light.svg" alt="Kimi" width="24" height="24" align="middle" /></picture> &nbsp; <b>Kimi</b></td>
    <td valign="middle" nowrap><img src="../docs/assets/readme/agents/pi-coding-agent.svg" alt="Pi" width="24" height="24" align="middle" /> &nbsp; <b>Pi</b></td>
  </tr>
  <tr valign="middle">
    <td valign="middle" nowrap><picture><source media="(prefers-color-scheme: dark)" srcset="../frontend/src/renderer/assets/agents/amp-dark.svg" /><img src="../frontend/src/renderer/assets/agents/amp-light.svg" alt="Amp" width="24" height="24" align="middle" /></picture> &nbsp; <b>Amp</b></td>
    <td valign="middle" nowrap><picture><source media="(prefers-color-scheme: dark)" srcset="../frontend/src/renderer/assets/agents/auggie-dark.svg" /><img src="../frontend/src/renderer/assets/agents/auggie-light.svg" alt="Auggie" width="24" height="24" align="middle" /></picture> &nbsp; <b>Auggie</b></td>
    <td valign="middle" nowrap><picture><source media="(prefers-color-scheme: dark)" srcset="../frontend/src/renderer/assets/agents/droid-dark.svg" /><img src="../frontend/src/renderer/assets/agents/droid-light.svg" alt="Droid" width="24" height="24" align="middle" /></picture> &nbsp; <b>Droid</b></td>
  </tr>
  <tr valign="middle">
    <td valign="middle" nowrap><img src="../frontend/src/renderer/assets/agents/crush.svg" alt="Crush" width="24" height="24" align="middle" /> &nbsp; <b>Crush</b></td>
    <td valign="middle" nowrap><picture><source media="(prefers-color-scheme: dark)" srcset="../frontend/src/renderer/assets/agents/cline-dark.svg" /><img src="../frontend/src/renderer/assets/agents/cline-light.svg" alt="Cline" width="24" height="24" align="middle" /></picture> &nbsp; <b>Cline</b></td>
    <td valign="middle" nowrap><picture><source media="(prefers-color-scheme: dark)" srcset="../frontend/src/renderer/assets/agents/goose-dark.svg" /><img src="../frontend/src/renderer/assets/agents/goose-light.svg" alt="Goose" width="24" height="24" align="middle" /></picture> &nbsp; <b>Goose</b></td>
  </tr>
  <tr valign="middle">
    <td valign="middle" nowrap><picture><source media="(prefers-color-scheme: dark)" srcset="../frontend/src/renderer/assets/agents/qwen-dark.svg" /><img src="../frontend/src/renderer/assets/agents/qwen-light.svg" alt="Qwen" width="24" height="24" align="middle" /></picture> &nbsp; <b>Qwen</b></td>
    <td valign="middle" nowrap><picture><source media="(prefers-color-scheme: dark)" srcset="../frontend/src/renderer/assets/agents/continue-dark.svg" /><img src="../frontend/src/renderer/assets/agents/continue-light.svg" alt="Continue" width="24" height="24" align="middle" /></picture> &nbsp; <b>Continue</b></td>
    <td valign="middle" nowrap><picture><source media="(prefers-color-scheme: dark)" srcset="../frontend/src/renderer/assets/agents/devin-dark.svg" /><img src="../frontend/src/renderer/assets/agents/devin-light.svg" alt="Devin" width="24" height="24" align="middle" /></picture> &nbsp; <b>Devin</b></td>
  </tr>
  <tr valign="middle">
    <td valign="middle" nowrap><img src="../frontend/src/renderer/assets/agents/kiro.svg" alt="Kiro" width="24" height="24" align="middle" /> &nbsp; <b>Kiro</b></td>
    <td valign="middle" nowrap><picture><source media="(prefers-color-scheme: dark)" srcset="../frontend/src/renderer/assets/agents/kilocode-dark.svg" /><img src="../frontend/src/renderer/assets/agents/kilocode-light.svg" alt="Kilo Code" width="24" height="24" align="middle" /></picture> &nbsp; <b>Kilo Code</b></td>
    <td valign="middle" nowrap><img src="../frontend/src/renderer/assets/agents/vibe.svg" alt="Vibe" width="24" height="24" align="middle" /> &nbsp; <b>Vibe</b></td>
  </tr>
  <tr valign="middle">
    <td valign="middle" nowrap><img src="../frontend/src/renderer/assets/agents/muse.svg" alt="Muse" width="24" height="24" align="middle" /> &nbsp; <b>Muse</b></td>
    <td valign="middle" nowrap><img src="../frontend/src/renderer/assets/agents/agy.svg" alt="Agy" width="24" height="24" align="middle" /> &nbsp; <b>Agy</b></td>
    <td valign="middle" nowrap><picture><source media="(prefers-color-scheme: dark)" srcset="../docs/assets/readme/agents/autohand-stacked-dark.png" /><img src="../docs/assets/readme/agents/autohand-stacked-light.png" alt="Autohand" width="24" height="24" align="middle" /></picture> <b>Autohand</b></td>
  </tr>
  <tr valign="middle">
    <td valign="middle" nowrap><img src="../frontend/src/renderer/assets/agents/kimchi.svg" alt="Kimchi" width="24" height="24" align="middle" /> &nbsp; <b>Kimchi</b></td>
    <td valign="middle" nowrap><img src="../docs/assets/readme/agents/prime-agent.svg" alt="Prime Agent" width="24" height="24" align="middle" /> &nbsp; <b>Prime Agent</b></td>
    <td valign="middle" nowrap><img src="../frontend/src/renderer/assets/agents/omp.svg" alt="OMP" width="24" height="24" align="middle" /> &nbsp; <b>OMP</b></td>
  </tr>
  <tr valign="middle">
    <td valign="middle" nowrap><picture><source media="(prefers-color-scheme: dark)" srcset="../frontend/src/renderer/assets/agents/fx-dark.svg" /><img src="../frontend/src/renderer/assets/agents/fx-light.svg" alt="fx" width="24" height="24" align="middle" /></picture> &nbsp; <b>fx</b></td>
    <td valign="middle" nowrap><picture><source media="(prefers-color-scheme: dark)" srcset="../frontend/src/renderer/assets/agents/unreal-agent-dark.png" /><img src="../frontend/src/renderer/assets/agents/unreal-agent-light.png" alt="Unreal Agent" width="24" height="24" align="middle" /></picture> &nbsp; <b>Unreal Agent</b></td>
    <td valign="middle" nowrap><img src="../frontend/src/renderer/assets/agents/mimo-code.svg" alt="MiMo Code" width="24" height="24" align="middle" /> &nbsp; <b>MiMo Code</b></td>
  </tr>
  <tr valign="middle">
    <td valign="middle" nowrap><img src="../frontend/src/renderer/assets/agents/gemini.svg" alt="Gemini CLI" width="24" height="24" align="middle" /> &nbsp; <b>Gemini CLI</b></td>
    <td valign="middle" nowrap><picture><source media="(prefers-color-scheme: dark)" srcset="../frontend/src/renderer/assets/agents/deepseek-harness-dark.svg" /><img src="../frontend/src/renderer/assets/agents/deepseek-harness-light.svg" alt="DeepSeek" width="24" height="24" align="middle" /></picture> &nbsp; <b>DeepSeek</b></td>
    <td valign="middle" nowrap><picture><source media="(prefers-color-scheme: dark)" srcset="../frontend/src/renderer/assets/agents/opencode-dark.svg" /><img src="../frontend/src/renderer/assets/agents/opencode-light.svg" alt="OpenCode 2" width="24" height="24" align="middle" /></picture> &nbsp; <b>OpenCode 2</b></td>
  </tr>
  <tr valign="middle">
    <td valign="middle" nowrap><picture><source media="(prefers-color-scheme: dark)" srcset="../frontend/src/renderer/assets/agents/codewhale-dark.svg" /><img src="../frontend/src/renderer/assets/agents/codewhale-light.svg" alt="Codewhale" width="24" height="24" align="middle" /></picture> &nbsp; <b>Codewhale</b></td>
    <td valign="middle" nowrap><picture><source media="(prefers-color-scheme: dark)" srcset="../frontend/src/renderer/assets/agents/command-code-dark.svg" /><img src="../frontend/src/renderer/assets/agents/command-code-light.svg" alt="Command Code" width="24" height="24" align="middle" /></picture> &nbsp; <b>Command Code</b></td>
    <td valign="middle" nowrap><picture><source media="(prefers-color-scheme: dark)" srcset="../frontend/src/renderer/assets/agents/openhands-dark.svg" /><img src="../frontend/src/renderer/assets/agents/openhands-light.svg" alt="OpenHands" width="24" height="24" align="middle" /></picture> &nbsp; <b>OpenHands</b></td>
  </tr>
</table>

<br />

### Orchestrator for Mobile

用手机操控在桌面端运行的智能体。安装适用于 [iPhone](https://apps.apple.com/app/ao-mobile/id6792552173) 或 [Android](https://play.google.com/store/apps/details?id=aoagents.dev) 的应用，在 **Settings → Connect Mobile** 中扫描二维码即可与桌面端配对。在家里的 Wi-Fi 和外出时都能使用。

- **接收提醒**：智能体有问题或需要批准时，会收到推送通知。
- **回复**：在聊天中回答、批准请求，或打开智能体的终端。
- **开始工作**：创建新的工作者，并选择其智能体和模型。
- **评审**：查看拉取请求和评审者结果。

<img src="../docs/assets/readme/site/mobile.jpg" alt="移动应用中，一个智能体询问要使用哪家邮件服务商，以及从手机发送的回复" width="100%" />

### Cloud Agents

让工作者在托管的工作区中运行，而不是在你的机器上。选择 **Clone to the cloud** 并挑选一个 GitHub 仓库即可开始。

- **持续运行**：合上笔记本，任务也会继续。
- **随时跟进**：在桌面应用中打开聊天、文件和终端。
- **本机不留任何东西**：代码、工作树和智能体全部在云端。

设置方法请参阅 [Cloud 指南](https://docs.orchestrator.inc/guides/cloud)。

## 安装

从 [orchestrator.inc/download](https://orchestrator.inc/download/) 或 [GitHub Releases](https://github.com/OrchestratorInc/agent-orchestrator/releases/latest) 下载。应用会自动更新。

| 平台 | 下载 |
| :-- | :-- |
| **macOS**（Apple 芯片） | [`agent-orchestrator-darwin-arm64.dmg`](https://github.com/OrchestratorInc/agent-orchestrator/releases/latest/download/agent-orchestrator-darwin-arm64.dmg) |
| **macOS**（Intel） | [`agent-orchestrator-darwin-x64.dmg`](https://github.com/OrchestratorInc/agent-orchestrator/releases/latest/download/agent-orchestrator-darwin-x64.dmg) |
| **Windows** | [`agent-orchestrator-win32-x64.exe`](https://github.com/OrchestratorInc/agent-orchestrator/releases/latest/download/agent-orchestrator-win32-x64.exe) |
| **Linux**（AppImage） | [`agent-orchestrator-linux-x64.AppImage`](https://github.com/OrchestratorInc/agent-orchestrator/releases/latest/download/agent-orchestrator-linux-x64.AppImage) |
| **Linux**（Debian/Ubuntu） | [`agent-orchestrator-linux-x64.deb`](https://github.com/OrchestratorInc/agent-orchestrator/releases/latest/download/agent-orchestrator-linux-x64.deb) |
| **Linux**（Fedora/RHEL） | [`agent-orchestrator-linux-x64.rpm`](https://github.com/OrchestratorInc/agent-orchestrator/releases/latest/download/agent-orchestrator-linux-x64.rpm) |

打开 Orchestrator.inc，并选择你希望它管理的仓库。桌面应用会为你运行守护进程，因此无需使用 CLI。有关智能体 CLI 的设置和故障排除，请参阅[安装指南](https://docs.orchestrator.inc/installation)。

## 文档

| 文档 | 需要以下内容时从这里开始 |
| :-- | :-- |
| [产品文档](https://docs.orchestrator.inc) | 安装、智能体设置和日常产品使用。 |
| [docs/documentation-map.md](../docs/documentation-map.md) | 哪些文档面向人阅读，哪些是机器可读的契约，以及内容不一致时以哪个为准。 |
| [docs/architecture.md](../docs/architecture.md) | 后端心智模型、生命周期、持久化、CDC、状态推导和守护进程边界。 |
| [docs/backend-code-structure.md](../docs/backend-code-structure.md) | 包职责以及各项后端关注点应归属的位置。 |
| [docs/cli/README.md](../docs/cli/README.md) | CLI 行为和守护进程路由映射。 |
| [docs/development.md](../docs/development.md) | 本地开发的先决条件、构建步骤、测试运行方法和故障排除。 |
| [docs/STATUS.md](../docs/STATUS.md) | `main` 当前已提供的功能和尚在开发中的内容。 |

## 参与贡献

请先阅读[开发指南](../docs/development.md)，了解先决条件、本地设置和测试命令。提交拉取请求前请阅读 [CONTRIBUTING.md](../CONTRIBUTING.md)，并通过 [GitHub Issues](https://github.com/OrchestratorInc/agent-orchestrator/issues) 报告 bug 和提出功能请求。

### 报告 bug

请使用你自己的 GitHub 账号[提交 bug 报告](https://github.com/OrchestratorInc/agent-orchestrator/issues/new?template=bug_report.yml)。用几句话说明你做了什么、出了什么问题即可；如果有版本、操作系统和复现步骤，也会很有帮助。

本地编程智能体可以借助 [bug-triage skill](../.agents/skills/bug-triage/SKILL.md) 帮你收集证据；请把它的日志和笔记放在附件中，并在自行提交前检查草稿。请不要让机器人代你提交 issue。

如果需要帮助描述问题，可以加入 [Discord 上的 bug-triaging 频道](https://discord.com/channels/1476302178913357958/1491735678156013588)。更多信息请参阅[贡献指南](../CONTRIBUTING.md#bugs-and-features)。

## 社区

加入 [Discord](https://discord.com/invite/UZv7JjxbwG) 获取帮助并参与贡献者讨论，关注 [@useOrchestrator](https://x.com/useOrchestrator) 了解最新动态，或在 [GitHub Issues](https://github.com/OrchestratorInc/agent-orchestrator/issues) 中发起讨论。

## Star 历史

<a href="https://www.star-history.com/#OrchestratorInc/agent-orchestrator&Date">
  <picture>
    <source media="(prefers-color-scheme: dark)" srcset="https://api.star-history.com/svg?repos=OrchestratorInc/agent-orchestrator&type=Date&theme=dark" />
    <img src="https://api.star-history.com/svg?repos=OrchestratorInc/agent-orchestrator&type=Date" alt="OrchestratorInc/agent-orchestrator 的 Star 历史图表" width="100%" />
  </picture>
</a>

## 产品遥测

Orchestrator.inc 收集有限的使用情况和可靠性遥测数据，绝不收集你的代码、提示词、智能体对话或凭据。这些数据包括安装 ID 和你的 GitHub 用户名；如果你登录了 AO Cloud，还会包括你的 AO Cloud 用户 ID 和该账号的电子邮件地址，它们只会作为 PostHog 人员属性设置一次（绝不从 git config 读取）。你可以在 Settings > General > Privacy > Share usage analytics 中将其全部关闭，该设置同时适用于桌面端、守护进程和已配对的手机。[详细了解我们收集哪些数据以及如何选择退出](../docs/telemetry.md)。

## 许可证

Orchestrator.inc 基于 [Apache License 2.0](../LICENSE) 提供。
