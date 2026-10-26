<div align="center">

<img src="../docs/assets/readme/banner.png" alt="Orchestrator.inc" width="100%" />

<h3>Pare de vigiar agentes. Comece a fazer merge de trabalho de verdade.</h3>

<p>
  Execute uma frota de agentes de programação mantendo branches, revisões e falhas de CI sob controle.
</p>

[![GitHub stars](https://img.shields.io/github/stars/OrchestratorInc/agent-orchestrator?style=flat&logo=github&color=1A9BF0)](https://github.com/OrchestratorInc/agent-orchestrator/stargazers)
[![GitHub release](https://img.shields.io/github/v/release/OrchestratorInc/agent-orchestrator?style=flat&logo=github&color=1A9BF0)](https://github.com/OrchestratorInc/agent-orchestrator/releases/latest)
[![GitHub downloads](https://img.shields.io/github/downloads/OrchestratorInc/agent-orchestrator/total?style=flat&logo=github&color=1A9BF0)](https://github.com/OrchestratorInc/agent-orchestrator/releases)
[![License: Apache-2.0](https://img.shields.io/badge/license-Apache--2.0-0B1A33?style=flat)](../LICENSE)
[![X](https://img.shields.io/badge/@useOrchestrator-0B1A33?style=flat&logo=x&logoColor=white)](https://x.com/useOrchestrator)
[![Discord](https://img.shields.io/badge/Discord-5865F2?style=flat&logo=discord&logoColor=white)](https://discord.com/invite/UZv7JjxbwG)

<br />

[**Baixar o Orchestrator.inc**](https://orchestrator.inc) &nbsp;&bull;&nbsp; [Documentação](https://docs.orchestrator.inc) &nbsp;&bull;&nbsp; [Versões](https://github.com/OrchestratorInc/agent-orchestrator/releases) &nbsp;&bull;&nbsp; [Como contribuir](../CONTRIBUTING.md) &nbsp;&bull;&nbsp; [Discord](https://discord.com/invite/UZv7JjxbwG)

[English](../README.md) · [简体中文](README.zh-CN.md) · [日本語](README.ja.md) · [한국어](README.ko.md) · [Español](README.es.md) · [Français](README.fr.md) · [Deutsch](README.de.md) · **Português (Brasil)**

<br />

<img src="../docs/assets/readme/hero.png" alt="Kanban do Orchestrator.inc com sessões de workers agrupadas por status em tempo real" width="100%" />

</div>

<br />

## Por que o Orchestrator.inc

Um agente de programação dá conta de uma tarefa. Rodar vários significa dividir o trabalho, evitar conflitos entre branches e acompanhar cada mudança pela CI, pela revisão e pelo merge. O Orchestrator.inc é um workspace desktop local feito para isso: cada tarefa ganha seu próprio agente, branch e worktree, e seu código continua na sua máquina.

## Recursos

### O orquestrador comanda a frota

Diga ao agente principal do projeto o que você quer construir. Ele planeja o trabalho, inicia um worker para cada parte em seu próprio worktree e só volta quando algo precisa de você.

<img src="../docs/assets/readme/site/orchestrator.jpg" alt="O orquestrador dividindo um recurso de cobrança entre três workers, cada um em seu próprio worktree com um agente de programação diferente" width="100%" />

### Um quadro para todas as sessões

As sessões se organizam sozinhas em colunas com base no agente, no PR, na CI e nas revisões, para que o que precisa de você fique no topo.

<img src="../docs/assets/readme/site/board.jpg" alt="O quadro Kanban com as colunas Working, Needs you, In review e Ready to merge" width="100%" />

### A CI falha, o agente certo corrige

Verificações com falha, comentários de revisão e conflitos de merge voltam para o worker responsável pela branch até o PR ser aprovado. Um agente revisor pode ler cada PR antes de uma pessoa.

<img src="../docs/assets/readme/site/feedback.jpg" alt="Linha do tempo de um pull request em que uma verificação com falha e um comentário de revisão são devolvidos ao Claude Code, que envia uma correção" width="100%" />

### Use o agente em que você já confia

Escolha o agente e o modelo para cada tarefa e trabalhe pelo Chat estruturado ou pela interface de terminal do próprio agente. Cada worker também ganha um navegador para visualizar e testar seu aplicativo.

#### Agentes compatíveis

O suporte a Worker, Chat e revisor varia de acordo com o agente; os [guias de configuração dos agentes](https://docs.orchestrator.inc/plugins/agents) trazem a matriz atualizada.

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

Controle pelo celular os agentes que rodam no seu desktop. Instale o app para [iPhone](https://apps.apple.com/app/ao-mobile/id6792552173) ou [Android](https://play.google.com/store/apps/details?id=aoagents.dev). Escaneie o QR code em **Settings → Connect Mobile** para parear com o desktop. Funciona no Wi-Fi de casa e fora dela.

- **Receba alertas.** Uma notificação push avisa quando um agente tem uma pergunta ou precisa de aprovação.
- **Responda.** Responda no chat, aprove a solicitação ou abra o terminal do agente.
- **Comece um trabalho.** Crie um novo worker e escolha o agente e o modelo.
- **Revise.** Confira pull requests e resultados do revisor.

<img src="../docs/assets/readme/site/mobile.jpg" alt="O aplicativo móvel mostrando um agente perguntando qual provedor de e-mail usar e a resposta enviada pelo celular" width="100%" />

### Cloud Agents

Rode workers em espaços de trabalho hospedados em vez de na sua máquina. Escolha **Clone to the cloud** e um repositório do GitHub para começar.

- **Continua rodando.** As tarefas seguem mesmo com o notebook fechado.
- **Acompanhe.** Abra o chat, os arquivos e o terminal pelo app desktop.
- **Nada na sua máquina.** O código, as worktrees e os agentes ficam todos na nuvem.

Veja o [guia do Cloud](https://docs.orchestrator.inc/guides/cloud) para a configuração.

## Instalação

Baixe em [orchestrator.inc/download](https://orchestrator.inc/download/) ou nas [Releases do GitHub](https://github.com/OrchestratorInc/agent-orchestrator/releases/latest). O aplicativo se atualiza sozinho.

| Plataforma | Download |
| :-- | :-- |
| **macOS** (Apple silicon) | [`agent-orchestrator-darwin-arm64.dmg`](https://github.com/OrchestratorInc/agent-orchestrator/releases/latest/download/agent-orchestrator-darwin-arm64.dmg) |
| **macOS** (Intel) | [`agent-orchestrator-darwin-x64.dmg`](https://github.com/OrchestratorInc/agent-orchestrator/releases/latest/download/agent-orchestrator-darwin-x64.dmg) |
| **Windows** | [`agent-orchestrator-win32-x64.exe`](https://github.com/OrchestratorInc/agent-orchestrator/releases/latest/download/agent-orchestrator-win32-x64.exe) |
| **Linux** (AppImage) | [`agent-orchestrator-linux-x64.AppImage`](https://github.com/OrchestratorInc/agent-orchestrator/releases/latest/download/agent-orchestrator-linux-x64.AppImage) |
| **Linux** (Debian/Ubuntu) | [`agent-orchestrator-linux-x64.deb`](https://github.com/OrchestratorInc/agent-orchestrator/releases/latest/download/agent-orchestrator-linux-x64.deb) |
| **Linux** (Fedora/RHEL) | [`agent-orchestrator-linux-x64.rpm`](https://github.com/OrchestratorInc/agent-orchestrator/releases/latest/download/agent-orchestrator-linux-x64.rpm) |

Abra o Orchestrator.inc e indique o repositório que você quer que ele gerencie. O aplicativo desktop executa o daemon para você, então não é preciso usar a CLI. Consulte o [guia de instalação](https://docs.orchestrator.inc/installation) para configurar as CLIs dos agentes e resolver problemas.

## Documentação

| Documento | Comece aqui quando precisar de |
| :-- | :-- |
| [Documentação do produto](https://docs.orchestrator.inc) | Instalação, configuração de agentes e uso cotidiano do produto. |
| [docs/documentation-map.md](../docs/documentation-map.md) | Quais documentos são voltados a pessoas, quais são contratos legíveis por máquina e qual prevalece em caso de divergência. |
| [docs/architecture.md](../docs/architecture.md) | Modelo mental do backend, ciclo de vida, persistência, CDC, derivação de status e limites do daemon. |
| [docs/backend-code-structure.md](../docs/backend-code-structure.md) | Responsabilidade dos pacotes e onde cada aspecto do backend deve ficar. |
| [docs/cli/README.md](../docs/cli/README.md) | Comportamento da CLI e mapeamento das rotas do daemon. |
| [docs/development.md](../docs/development.md) | Pré-requisitos, etapas de build, execução de testes e solução de problemas no desenvolvimento local. |
| [docs/STATUS.md](../docs/STATUS.md) | O que está disponível atualmente em `main` e o que ainda está em desenvolvimento. |

## Como contribuir

Comece pelo [guia de desenvolvimento](../docs/development.md) para ver pré-requisitos, configuração local e comandos de teste. Leia [CONTRIBUTING.md](../CONTRIBUTING.md) antes de abrir um pull request e use as [Issues do GitHub](https://github.com/OrchestratorInc/agent-orchestrator/issues) para bugs e solicitações de recursos.

### Relatar um bug

[Abra um relatório de bug](https://github.com/OrchestratorInc/agent-orchestrator/issues/new?template=bug_report.yml) usando sua própria conta do GitHub. Algumas frases sobre o que você fez e o que deu errado são suficientes; versão, sistema operacional e etapas para reproduzir ajudam, se você tiver.

Um agente de programação local pode ajudar a reunir evidências com a [skill de triagem de bugs](../.agents/skills/bug-triage/SKILL.md); mantenha os logs e as notas dele em anexos e revise o rascunho antes de enviá-lo você mesmo. Não peça a um bot para abrir issues em seu nome.

Para obter ajuda ao descrever um problema, participe do [canal de triagem de bugs no Discord](https://discord.com/channels/1476302178913357958/1491735678156013588). Consulte o [guia de contribuição](../CONTRIBUTING.md#bugs-and-features) para mais detalhes.

## Comunidade

Participe do [Discord](https://discord.com/invite/UZv7JjxbwG) para receber ajuda e conversar com outros colaboradores, siga [@useOrchestrator](https://x.com/useOrchestrator) para acompanhar as novidades ou inicie uma conversa nas [Issues do GitHub](https://github.com/OrchestratorInc/agent-orchestrator/issues).

## Histórico de estrelas

<a href="https://www.star-history.com/#OrchestratorInc/agent-orchestrator&Date">
  <picture>
    <source media="(prefers-color-scheme: dark)" srcset="https://api.star-history.com/svg?repos=OrchestratorInc/agent-orchestrator&type=Date&theme=dark" />
    <img src="https://api.star-history.com/svg?repos=OrchestratorInc/agent-orchestrator&type=Date" alt="Gráfico do histórico de estrelas de OrchestratorInc/agent-orchestrator" width="100%" />
  </picture>
</a>

## Telemetria do produto

O Orchestrator.inc coleta telemetria limitada de uso e confiabilidade, nunca seu código, prompts, conversas com agentes ou credenciais. Ela inclui um ID de instalação e seu nome de usuário do GitHub; se você entrar no AO Cloud, também inclui seu ID de usuário do AO Cloud e o endereço de e-mail dessa conta, definido uma única vez como propriedade de pessoa no PostHog (nunca lido da configuração do git). Desative tudo em Settings > General > Privacy > Share usage analytics, o que abrange o desktop, o daemon e um celular pareado. [Saiba exatamente o que coletamos e como desativar](../docs/telemetry.md).

## Licença

O Orchestrator.inc está disponível sob a [Licença Apache 2.0](../LICENSE).
