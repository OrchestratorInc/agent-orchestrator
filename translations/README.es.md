<div align="center">

<img src="../docs/assets/readme/logo.png" alt="Orchestrator.inc logo" width="96" height="96" />

### Orchestrator.inc

<h4>Deja de hacer de niñera de tus agentes. Empieza a integrar trabajo real.</h4>

<p>
  Ejecuta una flota de agentes de programación sin perder el control de las ramas, las revisiones ni los fallos de CI.
</p>

[![GitHub stars](https://img.shields.io/github/stars/OrchestratorInc/agent-orchestrator?style=flat&logo=github&color=1A9BF0)](https://github.com/OrchestratorInc/agent-orchestrator/stargazers)
[![GitHub release](https://img.shields.io/github/v/release/OrchestratorInc/agent-orchestrator?style=flat&logo=github&color=1A9BF0)](https://github.com/OrchestratorInc/agent-orchestrator/releases/latest)
[![GitHub downloads](https://img.shields.io/github/downloads/OrchestratorInc/agent-orchestrator/total?style=flat&logo=github&color=1A9BF0)](https://github.com/OrchestratorInc/agent-orchestrator/releases)
[![License: Apache-2.0](https://img.shields.io/badge/license-Apache--2.0-0B1A33?style=flat)](../LICENSE)
[![X](https://img.shields.io/badge/@useOrchestrator-0B1A33?style=flat&logo=x&logoColor=white)](https://x.com/useOrchestrator)
[![Discord](https://img.shields.io/badge/Discord-5865F2?style=flat&logo=discord&logoColor=white)](https://discord.com/invite/UZv7JjxbwG)

<br />

[**Descargar Orchestrator.inc**](https://orchestrator.inc) &nbsp;&bull;&nbsp; [Documentación](https://docs.orchestrator.inc) &nbsp;&bull;&nbsp; [Versiones](https://github.com/OrchestratorInc/agent-orchestrator/releases) &nbsp;&bull;&nbsp; [Contribuir](../CONTRIBUTING.md) &nbsp;&bull;&nbsp; [Discord](https://discord.com/invite/UZv7JjxbwG)

[English](../README.md) · [简体中文](README.zh-CN.md) · [日本語](README.ja.md) · [한국어](README.ko.md) · **Español** · [Français](README.fr.md) · [Deutsch](README.de.md) · [Português (Brasil)](README.pt-BR.md)

<br />

<img src="../docs/assets/readme/hero.png" alt="Kanban de Orchestrator.inc con sesiones de workers agrupadas por estado en vivo" width="100%" />

</div>

<br />

## Por qué Orchestrator.inc

Un agente de programación puede encargarse de una tarea. Ejecutar varios implica dividir el trabajo, evitar que las ramas choquen y seguir cada cambio a través de la CI, la revisión y la integración. Orchestrator.inc es un espacio de trabajo local de escritorio pensado para eso: cada tarea tiene su propio agente, rama y worktree, y tu código se queda en tu máquina.

## Funciones

### El orquestador dirige la flota

Dile al agente principal del proyecto qué quieres construir. Planifica el trabajo, inicia un worker para cada parte en su propio worktree y solo vuelve a ti cuando algo requiere tu atención.

<img src="../docs/assets/readme/site/orchestrator.jpg" alt="El orquestador divide una función de facturación en tres workers, cada uno en su propio worktree con un agente de programación distinto" width="100%" />

### Un solo tablero para todas las sesiones

Las sesiones se ordenan solas en columnas según el agente, su PR, la CI y las revisiones, así que lo que te necesita queda arriba.

<img src="../docs/assets/readme/site/board.jpg" alt="El tablero Kanban con las columnas Working, Needs you, In review y Ready to merge" width="100%" />

### Si la CI falla, la corrige el agente adecuado

Las comprobaciones fallidas, los comentarios de revisión y los conflictos de integración vuelven al worker responsable de la rama hasta que se aprueba el PR. Un agente revisor puede leer cada PR antes que una persona.

<img src="../docs/assets/readme/site/feedback.jpg" alt="Cronología de un pull request en la que una comprobación fallida y un comentario de revisión se devuelven a Claude Code, que sube una corrección" width="100%" />

### Usa el agente en el que ya confías

Elige el agente y el modelo para cada tarea y trabaja desde el Chat estructurado o desde la interfaz de terminal propia del agente. Cada worker tiene además un navegador para previsualizar y probar su aplicación.

#### Agentes compatibles

La compatibilidad como worker, en Chat y como revisor varía según el agente; las [guías de configuración de agentes](https://docs.orchestrator.inc/plugins/agents) recogen la matriz actualizada.

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

Controla desde el teléfono los agentes que se ejecutan en tu escritorio. Instala la aplicación para [iPhone](https://apps.apple.com/app/ao-mobile/id6792552173) o [Android](https://play.google.com/store/apps/details?id=aoagents.dev). Escanea el código QR en **Settings → Connect Mobile** para vincularla con tu escritorio. Funciona en el Wi-Fi de casa y fuera de ella.

- **Recibe avisos.** Una notificación push te avisa cuando un agente tiene una pregunta o necesita aprobación.
- **Responde.** Contesta en el chat, aprueba la solicitud o abre la terminal del agente.
- **Inicia trabajo.** Crea un nuevo worker y elige su agente y modelo.
- **Revisa.** Consulta los pull requests y los resultados del revisor.

<img src="../docs/assets/readme/site/mobile.jpg" alt="La aplicación móvil muestra a un agente que pregunta qué proveedor de correo usar y la respuesta enviada desde el teléfono" width="100%" />

### Cloud Agents

Ejecuta workers en espacios de trabajo alojados en lugar de en tu máquina. Elige **Clone to the cloud** y un repositorio de GitHub para empezar.

- **Sigue funcionando.** Las tareas continúan con el portátil cerrado.
- **Síguelo de cerca.** Abre el chat, los archivos y la terminal desde la aplicación de escritorio.
- **Nada en tu máquina.** El código, los worktrees y los agentes viven en la nube.

Consulta la [guía de Cloud](https://docs.orchestrator.inc/guides/cloud) para configurarlo.

## Instalación

Descarga desde [orchestrator.inc/download](https://orchestrator.inc/download/) o [GitHub Releases](https://github.com/OrchestratorInc/agent-orchestrator/releases/latest). La aplicación se actualiza sola.

| Plataforma | Descarga |
| :-- | :-- |
| **macOS** (Apple silicon) | [`agent-orchestrator-darwin-arm64.dmg`](https://github.com/OrchestratorInc/agent-orchestrator/releases/latest/download/agent-orchestrator-darwin-arm64.dmg) |
| **macOS** (Intel) | [`agent-orchestrator-darwin-x64.dmg`](https://github.com/OrchestratorInc/agent-orchestrator/releases/latest/download/agent-orchestrator-darwin-x64.dmg) |
| **Windows** | [`agent-orchestrator-win32-x64.exe`](https://github.com/OrchestratorInc/agent-orchestrator/releases/latest/download/agent-orchestrator-win32-x64.exe) |
| **Linux** (AppImage) | [`agent-orchestrator-linux-x64.AppImage`](https://github.com/OrchestratorInc/agent-orchestrator/releases/latest/download/agent-orchestrator-linux-x64.AppImage) |
| **Linux** (Debian/Ubuntu) | [`agent-orchestrator-linux-x64.deb`](https://github.com/OrchestratorInc/agent-orchestrator/releases/latest/download/agent-orchestrator-linux-x64.deb) |
| **Linux** (Fedora/RHEL) | [`agent-orchestrator-linux-x64.rpm`](https://github.com/OrchestratorInc/agent-orchestrator/releases/latest/download/agent-orchestrator-linux-x64.rpm) |

Abre Orchestrator.inc y selecciona el repositorio que quieres que gestione. La aplicación de escritorio ejecuta el daemon por ti, así que no necesitas ninguna CLI. Consulta la [guía de instalación](https://docs.orchestrator.inc/installation) para configurar las CLI de los agentes y resolver problemas.

## Documentación

| Documento | Empieza aquí si necesitas |
| :-- | :-- |
| [Documentación del producto](https://docs.orchestrator.inc) | Instalación, configuración de agentes y uso cotidiano del producto. |
| [docs/documentation-map.md](../docs/documentation-map.md) | Qué documentos están pensados para personas, cuáles son contratos legibles por máquina y cuál prevalece si divergen. |
| [docs/architecture.md](../docs/architecture.md) | Modelo mental del backend, ciclo de vida, persistencia, CDC, derivación del estado y límites del daemon. |
| [docs/backend-code-structure.md](../docs/backend-code-structure.md) | Responsabilidad de los paquetes y ubicación de cada aspecto del backend. |
| [docs/cli/README.md](../docs/cli/README.md) | Comportamiento de la CLI y correspondencia con las rutas del daemon. |
| [docs/development.md](../docs/development.md) | Requisitos previos, compilación, pruebas y solución de problemas para el desarrollo local. |
| [docs/STATUS.md](../docs/STATUS.md) | Qué se distribuye actualmente en `main` y qué sigue en desarrollo. |

## Contribuir

Empieza por la [guía de desarrollo](../docs/development.md), donde encontrarás los requisitos previos, la configuración local y los comandos de prueba. Lee [CONTRIBUTING.md](../CONTRIBUTING.md) antes de abrir un pull request y utiliza [GitHub Issues](https://github.com/OrchestratorInc/agent-orchestrator/issues) para informar de bugs o proponer funciones.

### Informar de un bug

[Abre un informe de bug](https://github.com/OrchestratorInc/agent-orchestrator/issues/new?template=bug_report.yml) desde tu propia cuenta de GitHub. Bastan unas frases sobre lo que hiciste y lo que salió mal; la versión, el sistema operativo y los pasos para reproducirlo ayudan si los tienes.

Un agente de programación local puede ayudarte a recopilar pruebas con la [skill de triaje de bugs](../.agents/skills/bug-triage/SKILL.md); deja sus registros y notas en archivos adjuntos y revisa el borrador antes de enviarlo tú mismo. No pidas a un bot que presente issues por ti.

Si necesitas ayuda para describir un problema, únete al [canal de triaje de bugs en Discord](https://discord.com/channels/1476302178913357958/1491735678156013588). Consulta la [guía para contribuir](../CONTRIBUTING.md#bugs-and-features) para obtener más detalles.

## Comunidad

Únete a [Discord](https://discord.com/invite/UZv7JjxbwG) para pedir ayuda y hablar con otros colaboradores, sigue a [@useOrchestrator](https://x.com/useOrchestrator) para conocer las novedades o inicia una conversación en [GitHub Issues](https://github.com/OrchestratorInc/agent-orchestrator/issues).

## Historial de estrellas

<a href="https://www.star-history.com/#OrchestratorInc/agent-orchestrator&Date">
  <picture>
    <source media="(prefers-color-scheme: dark)" srcset="https://api.star-history.com/svg?repos=OrchestratorInc/agent-orchestrator&type=Date&theme=dark" />
    <img src="https://api.star-history.com/svg?repos=OrchestratorInc/agent-orchestrator&type=Date" alt="Gráfico del historial de estrellas de OrchestratorInc/agent-orchestrator" width="100%" />
  </picture>
</a>

## Telemetría del producto

Orchestrator.inc recopila telemetría limitada de uso y fiabilidad, nunca tu código, prompts, conversaciones con agentes ni credenciales. Incluye un ID de instalación y tu nombre de usuario de GitHub; si inicias sesión en AO Cloud, también incluye tu ID de usuario de AO Cloud y la dirección de correo de esa cuenta, que se registra una sola vez como propiedad de persona en PostHog (nunca se lee de la configuración de git). Desactívala por completo en Settings > General > Privacy > Share usage analytics; esto abarca el escritorio, el daemon y un teléfono vinculado. [Consulta exactamente qué recopilamos y cómo desactivarlo](../docs/telemetry.md).

## Licencia

Orchestrator.inc está disponible bajo la [licencia Apache 2.0](../LICENSE).
