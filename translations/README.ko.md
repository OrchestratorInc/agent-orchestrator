<div align="center">

<img src="../docs/assets/readme/logo.png" alt="Orchestrator.inc logo" width="96" height="96" />

### Orchestrator.inc

<h4>에이전트를 일일이 돌보지 마세요. 실제 작업을 머지하세요.</h4>

<p>
  브랜치, 리뷰, CI 실패를 관리 가능한 상태로 유지하면서 여러 코딩 에이전트를 함께 실행하세요.
</p>

[![GitHub stars](https://img.shields.io/github/stars/OrchestratorInc/agent-orchestrator?style=flat&logo=github&color=1A9BF0)](https://github.com/OrchestratorInc/agent-orchestrator/stargazers)
[![GitHub release](https://img.shields.io/github/v/release/OrchestratorInc/agent-orchestrator?style=flat&logo=github&color=1A9BF0)](https://github.com/OrchestratorInc/agent-orchestrator/releases/latest)
[![GitHub downloads](https://img.shields.io/github/downloads/OrchestratorInc/agent-orchestrator/total?style=flat&logo=github&color=1A9BF0)](https://github.com/OrchestratorInc/agent-orchestrator/releases)
[![License: Apache-2.0](https://img.shields.io/badge/license-Apache--2.0-0B1A33?style=flat)](../LICENSE)
[![X](https://img.shields.io/badge/@useOrchestrator-0B1A33?style=flat&logo=x&logoColor=white)](https://x.com/useOrchestrator)
[![Discord](https://img.shields.io/badge/Discord-5865F2?style=flat&logo=discord&logoColor=white)](https://discord.com/invite/UZv7JjxbwG)

<br />

[**Orchestrator.inc 받기**](https://orchestrator.inc) &nbsp;&bull;&nbsp; [문서](https://docs.orchestrator.inc) &nbsp;&bull;&nbsp; [릴리스](https://github.com/OrchestratorInc/agent-orchestrator/releases) &nbsp;&bull;&nbsp; [기여하기](../CONTRIBUTING.md) &nbsp;&bull;&nbsp; [Discord](https://discord.com/invite/UZv7JjxbwG)

[English](../README.md) · [简体中文](README.zh-CN.md) · [日本語](README.ja.md) · **한국어** · [Español](README.es.md) · [Français](README.fr.md) · [Deutsch](README.de.md) · [Português (Brasil)](README.pt-BR.md)

<br />

<img src="../docs/assets/readme/hero.png" alt="워커 세션을 실시간 상태별로 보여주는 Orchestrator.inc Kanban" width="100%" />

</div>

<br />

## 왜 Orchestrator.inc인가

코딩 에이전트 하나는 작업 하나를 처리할 수 있습니다. 여러 에이전트를 실행하려면 작업을 나누고, 브랜치 충돌을 막고, 모든 변경 사항을 CI, 리뷰, 머지까지 추적해야 합니다. Orchestrator.inc는 이를 위한 로컬 데스크톱 워크스페이스입니다. 모든 작업은 전용 에이전트, 브랜치, worktree를 가지며, 코드는 내 컴퓨터를 벗어나지 않습니다.

## 기능

### 오케스트레이터가 에이전트 전체를 운영합니다

프로젝트의 메인 에이전트에게 무엇을 만들고 싶은지 알려주세요. 오케스트레이터가 작업을 계획하고, 각 부분마다 별도 worktree에서 워커를 시작하며, 사용자가 필요할 때만 돌아옵니다.

<img src="../docs/assets/readme/site/orchestrator.jpg" alt="결제 기능을 세 개의 워커로 나누는 오케스트레이터. 각 워커는 별도 worktree에서 서로 다른 코딩 에이전트로 실행됩니다" width="100%" />

### 모든 세션을 하나의 보드에서

세션은 에이전트, PR, CI, 리뷰 상태에 따라 레인으로 자동 정렬되므로 사용자의 확인이 필요한 항목이 항상 맨 위에 표시됩니다.

<img src="../docs/assets/readme/site/board.jpg" alt="Working, Needs you, In review, Ready to merge 레인이 있는 Kanban 보드" width="100%" />

### CI가 실패하면 담당 에이전트가 고칩니다

실패한 검사, 리뷰 코멘트, 머지 충돌은 PR이 승인될 때까지 해당 브랜치를 맡은 워커에게 돌아갑니다. 사람이 보기 전에 리뷰어 에이전트가 모든 PR을 먼저 검토할 수도 있습니다.

<img src="../docs/assets/readme/site/feedback.jpg" alt="실패한 검사와 리뷰 코멘트가 Claude Code로 전달되고, Claude Code가 수정 사항을 푸시하는 풀 리퀘스트 타임라인" width="100%" />

### 이미 신뢰하는 에이전트를 그대로 사용하세요

작업마다 에이전트와 모델을 선택한 뒤 구조화된 Chat 또는 에이전트 고유의 터미널 UI에서 작업하세요. 각 워커에는 앱을 미리 보고 테스트할 수 있는 브라우저도 제공됩니다.

#### 지원 에이전트

워커, Chat, 리뷰어 지원 여부는 에이전트마다 다릅니다. 최신 지원 현황은 [에이전트 설정 가이드](https://docs.orchestrator.inc/plugins/agents)를 확인하세요.

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

데스크톱에서 실행 중인 에이전트를 휴대폰으로 제어하세요. [iPhone](https://apps.apple.com/app/ao-mobile/id6792552173) 또는 [Android](https://play.google.com/store/apps/details?id=aoagents.dev)용 앱을 설치하세요. **Settings → Connect Mobile**의 QR 코드를 스캔해 데스크톱과 페어링하세요. 집 Wi-Fi에서도, 외부에서도 작동합니다.

- **알림 받기.** 에이전트에게 질문이 있거나 승인이 필요하면 푸시 알림이 옵니다.
- **답하기.** 채팅으로 답하거나, 요청을 승인하거나, 에이전트의 터미널을 여세요.
- **작업 시작.** 새 워커를 만들고 에이전트와 모델을 고르세요.
- **리뷰.** 풀 리퀘스트와 리뷰어 결과를 확인하세요.

<img src="../docs/assets/readme/site/mobile.jpg" alt="어떤 메일 제공업체를 사용할지 묻는 에이전트와 휴대폰에서 보낸 답장을 보여주는 모바일 앱" width="100%" />

### Cloud Agents

워커를 내 컴퓨터가 아닌 호스팅된 작업 공간에서 실행하세요. **Clone to the cloud**를 선택하고 GitHub 저장소를 고르면 시작됩니다.

- **계속 실행.** 노트북을 덮어도 작업이 계속됩니다.
- **진행 상황 확인.** 데스크톱 앱에서 채팅, 파일, 터미널을 여세요.
- **내 컴퓨터에는 아무것도 없음.** 코드, 워크트리, 에이전트가 모두 클라우드에 있습니다.

설정 방법은 [Cloud 가이드](https://docs.orchestrator.inc/guides/cloud)를 참고하세요.

## 설치

[orchestrator.inc/download](https://orchestrator.inc/download/) 또는 [GitHub Releases](https://github.com/OrchestratorInc/agent-orchestrator/releases/latest)에서 다운로드하세요. 앱은 자동으로 업데이트됩니다.

| 플랫폼 | 다운로드 |
| :-- | :-- |
| **macOS** (Apple silicon) | [`agent-orchestrator-darwin-arm64.dmg`](https://github.com/OrchestratorInc/agent-orchestrator/releases/latest/download/agent-orchestrator-darwin-arm64.dmg) |
| **macOS** (Intel) | [`agent-orchestrator-darwin-x64.dmg`](https://github.com/OrchestratorInc/agent-orchestrator/releases/latest/download/agent-orchestrator-darwin-x64.dmg) |
| **Windows** | [`agent-orchestrator-win32-x64.exe`](https://github.com/OrchestratorInc/agent-orchestrator/releases/latest/download/agent-orchestrator-win32-x64.exe) |
| **Linux** (AppImage) | [`agent-orchestrator-linux-x64.AppImage`](https://github.com/OrchestratorInc/agent-orchestrator/releases/latest/download/agent-orchestrator-linux-x64.AppImage) |
| **Linux** (Debian/Ubuntu) | [`agent-orchestrator-linux-x64.deb`](https://github.com/OrchestratorInc/agent-orchestrator/releases/latest/download/agent-orchestrator-linux-x64.deb) |
| **Linux** (Fedora/RHEL) | [`agent-orchestrator-linux-x64.rpm`](https://github.com/OrchestratorInc/agent-orchestrator/releases/latest/download/agent-orchestrator-linux-x64.rpm) |

Orchestrator.inc를 열고 관리할 저장소를 지정하세요. 데스크톱 앱이 데몬을 실행하므로 CLI는 필요하지 않습니다. 에이전트 CLI 설정 및 문제 해결은 [설치 가이드](https://docs.orchestrator.inc/installation)를 참고하세요.

## 문서

| 문서 | 다음 정보가 필요할 때 |
| :-- | :-- |
| [제품 문서](https://docs.orchestrator.inc) | 설치, 에이전트 설정, 일상적인 제품 사용법. |
| [docs/documentation-map.md](../docs/documentation-map.md) | 어떤 문서가 사람을 위한 것이고 어떤 문서가 기계 판독용 계약인지, 내용이 어긋날 때 어느 쪽이 우선하는지. |
| [docs/architecture.md](../docs/architecture.md) | 백엔드 멘탈 모델, 라이프사이클, 영속성, CDC, 상태 도출, 데몬 경계. |
| [docs/backend-code-structure.md](../docs/backend-code-structure.md) | 패키지 소유권과 각 백엔드 관심사가 속하는 위치. |
| [docs/cli/README.md](../docs/cli/README.md) | CLI 동작과 데몬 라우트 매핑. |
| [docs/development.md](../docs/development.md) | 로컬 개발을 위한 사전 요구 사항, 빌드 단계, 테스트 실행, 문제 해결. |
| [docs/STATUS.md](../docs/STATUS.md) | 현재 `main`에 출시된 내용과 아직 진행 중인 항목. |

## 기여하기

사전 요구 사항, 로컬 설정, 테스트 명령은 [개발 가이드](../docs/development.md)부터 확인하세요. 풀 리퀘스트를 열기 전에 [CONTRIBUTING.md](../CONTRIBUTING.md)를 읽고, 버그와 기능 요청에는 [GitHub Issues](https://github.com/OrchestratorInc/agent-orchestrator/issues)를 사용하세요.

### 버그 신고

본인의 GitHub 계정으로 [버그 신고를 여세요](https://github.com/OrchestratorInc/agent-orchestrator/issues/new?template=bug_report.yml). 무엇을 했고 어떤 문제가 발생했는지 몇 문장이면 충분합니다. 버전, 운영체제, 재현 단계가 있다면 도움이 됩니다.

로컬 코딩 에이전트는 [bug-triage skill](../.agents/skills/bug-triage/SKILL.md)로 증거 수집을 도울 수 있습니다. 에이전트의 로그와 메모는 첨부 파일로 제공하고, 직접 제출하기 전에 초안을 검토하세요. 봇에게 대신 이슈를 등록하게 하지 마세요.

문제 설명에 도움이 필요하면 [Discord bug-triaging 채널](https://discord.com/channels/1476302178913357958/1491735678156013588)에 참여하세요. 자세한 내용은 [기여 가이드](../CONTRIBUTING.md#bugs-and-features)를 참고하세요.

## 커뮤니티

도움 및 기여자 논의를 위해 [Discord](https://discord.com/invite/UZv7JjxbwG)에 참여하고, 업데이트를 보려면 [@useOrchestrator](https://x.com/useOrchestrator)를 팔로우하세요. [GitHub Issues](https://github.com/OrchestratorInc/agent-orchestrator/issues)에서 대화를 시작할 수도 있습니다.

## Star 기록

<a href="https://www.star-history.com/#OrchestratorInc/agent-orchestrator&Date">
  <picture>
    <source media="(prefers-color-scheme: dark)" srcset="https://api.star-history.com/svg?repos=OrchestratorInc/agent-orchestrator&type=Date&theme=dark" />
    <img src="https://api.star-history.com/svg?repos=OrchestratorInc/agent-orchestrator&type=Date" alt="OrchestratorInc/agent-orchestrator의 Star 기록 차트" width="100%" />
  </picture>
</a>

## 제품 텔레메트리

Orchestrator.inc는 제한된 사용 및 안정성 텔레메트리를 수집하며, 코드, 프롬프트, 에이전트 대화, 자격 증명은 절대 수집하지 않습니다. 여기에는 설치 ID와 GitHub 사용자 이름이 포함됩니다. AO Cloud에 로그인하면 AO Cloud 사용자 ID와 해당 계정의 이메일 주소도 포함되며, 이는 PostHog person 속성으로 한 번 설정됩니다(git config에서 읽지 않음). Settings > General > Privacy > Share usage analytics에서 모두 끌 수 있으며, 이 설정은 데스크톱, 데몬, 페어링된 휴대폰에 모두 적용됩니다. [수집 항목과 옵트아웃 방법 자세히 보기](../docs/telemetry.md).

## 라이선스

Orchestrator.inc는 [Apache License 2.0](../LICENSE)에 따라 제공됩니다.
