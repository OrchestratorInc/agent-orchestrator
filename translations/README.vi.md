<div align="center">

<img src="../docs/assets/readme/logo.png" alt="Orchestrator.inc logo" width="96" height="96" />

### Orchestrator.inc

<h4>Đừng trông trẻ cho agent nữa. Hãy bắt đầu merge công việc thật.</h4>

<p>
  Chạy cả một đội agent lập trình mà vẫn quản lý được branch, review và lỗi CI.
</p>

[![GitHub stars](https://img.shields.io/github/stars/OrchestratorInc/agent-orchestrator?style=flat&logo=github&color=1A9BF0)](https://github.com/OrchestratorInc/agent-orchestrator/stargazers)
[![GitHub release](https://img.shields.io/github/v/release/OrchestratorInc/agent-orchestrator?style=flat&logo=github&color=1A9BF0)](https://github.com/OrchestratorInc/agent-orchestrator/releases/latest)
[![GitHub downloads](https://img.shields.io/github/downloads/OrchestratorInc/agent-orchestrator/total?style=flat&logo=github&color=1A9BF0)](https://github.com/OrchestratorInc/agent-orchestrator/releases)
[![License: Apache-2.0](https://img.shields.io/badge/license-Apache--2.0-0B1A33?style=flat)](../LICENSE)
[![X](https://img.shields.io/badge/@useOrchestrator-0B1A33?style=flat&logo=x&logoColor=white)](https://x.com/useOrchestrator)
[![Discord](https://img.shields.io/badge/Discord-5865F2?style=flat&logo=discord&logoColor=white)](https://discord.com/invite/UZv7JjxbwG)

<br />

[**Tải Orchestrator.inc**](https://orchestrator.inc) &nbsp;&bull;&nbsp; [Tài liệu](https://docs.orchestrator.inc) &nbsp;&bull;&nbsp; [Bản phát hành](https://github.com/OrchestratorInc/agent-orchestrator/releases) &nbsp;&bull;&nbsp; [Đóng góp](../CONTRIBUTING.md) &nbsp;&bull;&nbsp; [Discord](https://discord.com/invite/UZv7JjxbwG)

[English](../README.md) · [简体中文](README.zh-CN.md) · [日本語](README.ja.md) · [한국어](README.ko.md) · [Español](README.es.md) · [Français](README.fr.md) · [Deutsch](README.de.md) · [Português (Brasil)](README.pt-BR.md) · **Tiếng Việt**

<br />

<img src="../docs/assets/readme/hero.png" alt="Bảng Kanban của Orchestrator.inc hiển thị các phiên worker được nhóm theo trạng thái trực tiếp" width="100%" />

</div>

<br />

## Vì sao chọn Orchestrator.inc

Một agent lập trình có thể xử lý một nhiệm vụ. Chạy nhiều agent cùng lúc nghĩa là phải chia nhỏ công việc, tránh để các branch xung đột nhau, và theo dõi từng thay đổi qua CI, review rồi merge. Orchestrator.inc là một workspace desktop chạy ngay trên máy của bạn dành cho việc đó: mỗi nhiệm vụ có agent, branch và worktree riêng, còn code của bạn vẫn ở trên máy của bạn.

## Tính năng

### Orchestrator điều hành cả đội agent

Hãy nói cho agent chính của dự án biết bạn muốn xây dựng gì. Nó lập kế hoạch, khởi động một worker cho từng phần việc trong worktree riêng, và chỉ quay lại khi có việc cần bạn.

<img src="../docs/assets/readme/site/orchestrator.jpg" alt="Orchestrator chia một tính năng thanh toán thành ba worker, mỗi worker trong một worktree riêng với một agent lập trình khác nhau" width="100%" />

### Một bảng cho mọi phiên làm việc

Các phiên tự sắp xếp vào từng làn dựa trên agent, PR, CI và review của chúng, nên việc nào cần bạn sẽ luôn nằm ở trên cùng.

<img src="../docs/assets/readme/site/board.jpg" alt="Bảng Kanban với các làn Working, Needs you, In review và Ready to merge" width="100%" />

### CI lỗi, đúng agent sẽ sửa

Các check lỗi, bình luận review và xung đột merge được trả lại cho worker sở hữu branch cho đến khi PR được phê duyệt. Một agent reviewer có thể đọc mọi PR trước cả khi có người xem.

<img src="../docs/assets/readme/site/feedback.jpg" alt="Dòng thời gian của một pull request, trong đó một check lỗi và một bình luận review được chuyển lại cho Claude Code, rồi agent này push bản sửa" width="100%" />

### Dùng agent mà bạn đã tin tưởng

Chọn agent và model cho từng nhiệm vụ, rồi làm việc trong khung chat có cấu trúc hoặc ngay trong giao diện terminal của agent. Mỗi worker còn có một trình duyệt để xem trước và thử nghiệm ứng dụng của mình.

#### Các agent được hỗ trợ

Mức độ hỗ trợ Worker, Chat và reviewer khác nhau tùy agent; [hướng dẫn thiết lập agent](https://docs.orchestrator.inc/plugins/agents) có bảng đầy đủ mới nhất.

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

Điều khiển các agent đang chạy trên máy tính của bạn ngay từ điện thoại. Cài đặt ứng dụng cho [iPhone](https://apps.apple.com/app/ao-mobile/id6792552173) hoặc [Android](https://play.google.com/store/apps/details?id=aoagents.dev). Quét mã QR trong **Settings → Connect Mobile** để ghép nối với máy tính. Ứng dụng hoạt động cả trên Wi-Fi nhà bạn lẫn khi bạn ra ngoài.

- **Nhận cảnh báo.** Một thông báo đẩy cho bạn biết khi agent có câu hỏi hoặc cần phê duyệt.
- **Trả lời.** Trả lời trong chat, phê duyệt yêu cầu, hoặc mở terminal của agent.
- **Bắt đầu việc mới.** Tạo một worker mới và chọn agent cùng model cho nó.
- **Xem xét.** Kiểm tra các pull request và kết quả review.

<img src="../docs/assets/readme/site/mobile.jpg" alt="Ứng dụng di động hiển thị một agent đang hỏi nên dùng nhà cung cấp mail nào và câu trả lời được gửi từ điện thoại" width="100%" />

### Cloud Agents

Chạy worker trong workspace được host sẵn thay vì trên máy của bạn. Chọn **Clone to the cloud** rồi chọn một GitHub repository để bắt đầu.

- **Chạy liên tục.** Nhiệm vụ vẫn tiếp tục chạy ngay cả khi bạn đã gập laptop.
- **Theo dõi trực tiếp.** Mở chat, file và terminal ngay từ ứng dụng desktop.
- **Không có gì trên máy của bạn.** Code, worktree và agent đều nằm trên cloud.

Xem [hướng dẫn Cloud](https://docs.orchestrator.inc/guides/cloud) để thiết lập.

## Cài đặt

Tải về từ [orchestrator.inc/download](https://orchestrator.inc/download/) hoặc [GitHub Releases](https://github.com/OrchestratorInc/agent-orchestrator/releases/latest). Ứng dụng tự cập nhật.

| Nền tảng | Tải về |
| :-- | :-- |
| **macOS** (Apple silicon) | [`Orchestrator-darwin-arm64.dmg`](https://github.com/OrchestratorInc/agent-orchestrator/releases/latest/download/Orchestrator-darwin-arm64.dmg) |
| **macOS** (Intel) | [`Orchestrator-darwin-x64.dmg`](https://github.com/OrchestratorInc/agent-orchestrator/releases/latest/download/Orchestrator-darwin-x64.dmg) |
| **Windows** | [`agent-orchestrator-win32-x64.exe`](https://github.com/OrchestratorInc/agent-orchestrator/releases/latest/download/agent-orchestrator-win32-x64.exe) |
| **Linux** (AppImage) | [`agent-orchestrator-linux-x64.AppImage`](https://github.com/OrchestratorInc/agent-orchestrator/releases/latest/download/agent-orchestrator-linux-x64.AppImage) |
| **Linux** (Debian/Ubuntu) | [`agent-orchestrator-linux-x64.deb`](https://github.com/OrchestratorInc/agent-orchestrator/releases/latest/download/agent-orchestrator-linux-x64.deb) |
| **Linux** (Fedora/RHEL) | [`agent-orchestrator-linux-x64.rpm`](https://github.com/OrchestratorInc/agent-orchestrator/releases/latest/download/agent-orchestrator-linux-x64.rpm) |

Mở Orchestrator.inc và trỏ nó tới repository bạn muốn quản lý. Ứng dụng desktop sẽ chạy daemon giúp bạn nên không cần CLI. Xem [hướng dẫn cài đặt](https://docs.orchestrator.inc/installation) để thiết lập CLI của agent và khắc phục sự cố.

## Tài liệu

| Tài liệu | Bắt đầu từ đây khi bạn cần |
| :-- | :-- |
| [Tài liệu sản phẩm](https://docs.orchestrator.inc) | Cài đặt, thiết lập agent và cách dùng sản phẩm hằng ngày. |
| [docs/documentation-map.md](../docs/documentation-map.md) | Tài liệu nào dành cho con người, tài liệu nào là hợp đồng máy đọc được, và tài liệu nào thắng khi có lệch lạc. |
| [docs/architecture.md](../docs/architecture.md) | Mô hình tư duy về backend, vòng đời, persistence, CDC, cách suy ra trạng thái và ranh giới daemon. |
| [docs/backend-code-structure.md](../docs/backend-code-structure.md) | Quyền sở hữu package và vị trí của từng mối quan tâm backend. |
| [docs/cli/README.md](../docs/cli/README.md) | Hành vi CLI và bảng ánh xạ route của daemon. |
| [docs/development.md](../docs/development.md) | Điều kiện tiên quyết, các bước build, chạy test và khắc phục sự cố khi phát triển local. |
| [docs/STATUS.md](../docs/STATUS.md) | Những gì hiện đã có trên `main` và những gì vẫn đang triển khai. |

## Đóng góp

Bắt đầu với [hướng dẫn phát triển](../docs/development.md) để biết điều kiện tiên quyết, cách thiết lập local và các lệnh test. Đọc [CONTRIBUTING.md](../CONTRIBUTING.md) trước khi mở pull request, và dùng [GitHub Issues](https://github.com/OrchestratorInc/agent-orchestrator/issues) để báo lỗi và đề xuất tính năng.

### Báo lỗi

[Mở một báo cáo lỗi](https://github.com/OrchestratorInc/agent-orchestrator/issues/new?template=bug_report.yml) từ chính tài khoản GitHub của bạn. Chỉ cần vài câu mô tả bạn đã làm gì và đã xảy ra lỗi gì; phiên bản, hệ điều hành và các bước tái hiện sẽ rất hữu ích nếu bạn có.

Một agent lập trình local có thể giúp thu thập bằng chứng bằng [bug-triage skill](../.agents/skills/bug-triage/SKILL.md); hãy giữ log và ghi chú của nó trong file đính kèm, và xem lại bản nháp trước khi tự gửi. Xin đừng để bot mở issue thay bạn.

Nếu cần trợ giúp mô tả vấn đề, hãy tham gia [kênh bug-triaging trên Discord](https://discord.com/channels/1476302178913357958/1491735678156013588). Xem [hướng dẫn đóng góp](../CONTRIBUTING.md#bugs-and-features) để biết thêm chi tiết.

## Cộng đồng

Tham gia [Discord](https://discord.com/invite/UZv7JjxbwG) để được giúp đỡ và thảo luận với các contributor, theo dõi [@useOrchestrator](https://x.com/useOrchestrator) để cập nhật tin tức, hoặc bắt đầu một cuộc thảo luận trong [GitHub Issues](https://github.com/OrchestratorInc/agent-orchestrator/issues).

## Lịch sử star

<a href="https://www.star-history.com/#OrchestratorInc/agent-orchestrator&Date">
  <picture>
    <source media="(prefers-color-scheme: dark)" srcset="https://api.star-history.com/svg?repos=OrchestratorInc/agent-orchestrator&type=Date&theme=dark" />
    <img src="https://api.star-history.com/svg?repos=OrchestratorInc/agent-orchestrator&type=Date" alt="Biểu đồ lịch sử star của OrchestratorInc/agent-orchestrator" width="100%" />
  </picture>
</a>

## Dữ liệu đo lường sản phẩm

Orchestrator.inc chỉ thu thập dữ liệu sử dụng và độ tin cậy ở mức giới hạn, không bao giờ thu thập code, prompt, cuộc trò chuyện với agent hay thông tin đăng nhập của bạn. Dữ liệu này gồm một ID cài đặt và tên người dùng GitHub của bạn; nếu bạn đăng nhập AO Cloud thì còn gồm ID người dùng AO Cloud và địa chỉ email của tài khoản đó, được đặt một lần làm thuộc tính person của PostHog (không bao giờ đọc từ git config). Bạn có thể tắt toàn bộ trong Settings > General > Privacy > Share usage analytics, áp dụng cho cả desktop, daemon và điện thoại đã ghép nối. [Tìm hiểu chính xác chúng tôi thu thập gì và cách từ chối](../docs/telemetry.md).

## Giấy phép

Orchestrator.inc được phát hành theo [Giấy phép Apache 2.0](../LICENSE).
