<div align="center">

<img src="../docs/assets/readme/banner.png" alt="Orchestrator.inc" width="100%" />

<h3>Arrêtez de surveiller vos agents. Commencez à fusionner du vrai travail.</h3>

<p>
  Pilotez une flotte d'agents de développement tout en gardant la maîtrise des branches, des revues et des échecs de CI.
</p>

[![GitHub stars](https://img.shields.io/github/stars/OrchestratorInc/agent-orchestrator?style=flat&logo=github&color=1A9BF0)](https://github.com/OrchestratorInc/agent-orchestrator/stargazers)
[![GitHub release](https://img.shields.io/github/v/release/OrchestratorInc/agent-orchestrator?style=flat&logo=github&color=1A9BF0)](https://github.com/OrchestratorInc/agent-orchestrator/releases/latest)
[![GitHub downloads](https://img.shields.io/github/downloads/OrchestratorInc/agent-orchestrator/total?style=flat&logo=github&color=1A9BF0)](https://github.com/OrchestratorInc/agent-orchestrator/releases)
[![License: Apache-2.0](https://img.shields.io/badge/license-Apache--2.0-0B1A33?style=flat)](../LICENSE)
[![X](https://img.shields.io/badge/@useOrchestrator-0B1A33?style=flat&logo=x&logoColor=white)](https://x.com/useOrchestrator)
[![Discord](https://img.shields.io/badge/Discord-5865F2?style=flat&logo=discord&logoColor=white)](https://discord.com/invite/UZv7JjxbwG)

<br />

[**Télécharger Orchestrator.inc**](https://orchestrator.inc) &nbsp;&bull;&nbsp; [Documentation](https://docs.orchestrator.inc) &nbsp;&bull;&nbsp; [Versions](https://github.com/OrchestratorInc/agent-orchestrator/releases) &nbsp;&bull;&nbsp; [Contribuer](../CONTRIBUTING.md) &nbsp;&bull;&nbsp; [Discord](https://discord.com/invite/UZv7JjxbwG)

[English](../README.md) · [简体中文](README.zh-CN.md) · [日本語](README.ja.md) · [한국어](README.ko.md) · [Español](README.es.md) · **Français** · [Deutsch](README.de.md) · [Português (Brasil)](README.pt-BR.md)

<br />

<img src="../docs/assets/readme/hero.png" alt="Kanban d'Orchestrator.inc montrant les sessions de workers regroupées selon leur état en direct" width="100%" />

</div>

<br />

## Pourquoi Orchestrator.inc

Un agent de développement peut prendre en charge une tâche. En faire tourner plusieurs implique de découper le travail, d'éviter les collisions entre branches et de suivre chaque changement à travers la CI, la revue et la fusion. Orchestrator.inc est un espace de travail de bureau local conçu pour cela : chaque tâche reçoit son propre agent, sa propre branche et son propre worktree, et votre code reste sur votre machine.

## Fonctionnalités

### L'orchestrateur pilote la flotte

Dites à l'agent principal d'un projet ce que vous voulez construire. Il planifie le travail, lance un worker pour chaque partie dans son propre worktree et ne revient vers vous que lorsque quelque chose requiert votre attention.

<img src="../docs/assets/readme/site/orchestrator.jpg" alt="L'orchestrateur découpe une fonctionnalité de facturation en trois workers, chacun dans son propre worktree avec un agent de développement différent" width="100%" />

### Un seul tableau pour toutes les sessions

Les sessions se rangent d'elles-mêmes dans des colonnes en fonction de l'agent, de sa PR, de la CI et des revues, pour que tout ce qui vous attend reste en haut.

<img src="../docs/assets/readme/site/board.jpg" alt="Le tableau Kanban avec les colonnes Working, Needs you, In review et Ready to merge" width="100%" />

### La CI échoue, le bon agent corrige

Les vérifications en échec, les commentaires de revue et les conflits de fusion sont renvoyés au worker propriétaire de la branche jusqu'à l'approbation de la PR. Un agent relecteur peut lire chaque PR avant qu'un humain ne s'en charge.

<img src="../docs/assets/readme/site/feedback.jpg" alt="La chronologie d'une pull request où une vérification en échec et un commentaire de revue sont renvoyés à Claude Code, qui pousse un correctif" width="100%" />

### Utilisez l'agent en qui vous avez déjà confiance

Choisissez l'agent et le modèle pour chaque tâche, puis travaillez dans le Chat structuré ou dans l'interface de terminal native de l'agent. Chaque worker dispose aussi d'un navigateur pour prévisualiser et tester son application.

#### Agents pris en charge

La prise en charge des modes worker, Chat et relecteur varie selon l'agent ; les [guides de configuration des agents](https://docs.orchestrator.inc/plugins/agents) présentent la matrice à jour.

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

Pilotez depuis votre téléphone les agents qui tournent sur votre ordinateur. Installez l'application pour [iPhone](https://apps.apple.com/app/ao-mobile/id6792552173) ou [Android](https://play.google.com/store/apps/details?id=aoagents.dev). Scannez le QR code dans **Settings → Connect Mobile** pour l'associer à votre ordinateur. Elle fonctionne sur le Wi-Fi de la maison comme en déplacement.

- **Alertes.** Une notification push vous prévient quand un agent a une question ou attend une approbation.
- **Réponses.** Répondez dans le chat, approuvez la demande ou ouvrez le terminal de l'agent.
- **Nouveau travail.** Créez un worker et choisissez son agent et son modèle.
- **Revue.** Consultez les pull requests et les résultats du relecteur.

<img src="../docs/assets/readme/site/mobile.jpg" alt="L'application mobile montrant un agent qui demande quel fournisseur de messagerie utiliser et la réponse envoyée depuis le téléphone" width="100%" />

### Cloud Agents

Exécutez des workers dans des espaces de travail hébergés plutôt que sur votre machine. Choisissez **Clone to the cloud** puis un dépôt GitHub pour commencer.

- **Toujours actif.** Les tâches continuent, même ordinateur portable fermé.
- **Suivi en direct.** Ouvrez le chat, les fichiers et le terminal depuis l'application de bureau.
- **Rien sur votre machine.** Le code, les worktrees et les agents se trouvent entièrement dans le cloud.

Consultez le [guide Cloud](https://docs.orchestrator.inc/guides/cloud) pour la configuration.

## Installation

Téléchargez l'application depuis [orchestrator.inc/download](https://orchestrator.inc/download/) ou les [GitHub Releases](https://github.com/OrchestratorInc/agent-orchestrator/releases/latest). Elle se met à jour automatiquement.

| Plateforme | Téléchargement |
| :-- | :-- |
| **macOS** (Apple silicon) | [`agent-orchestrator-darwin-arm64.dmg`](https://github.com/OrchestratorInc/agent-orchestrator/releases/latest/download/agent-orchestrator-darwin-arm64.dmg) |
| **macOS** (Intel) | [`agent-orchestrator-darwin-x64.dmg`](https://github.com/OrchestratorInc/agent-orchestrator/releases/latest/download/agent-orchestrator-darwin-x64.dmg) |
| **Windows** | [`agent-orchestrator-win32-x64.exe`](https://github.com/OrchestratorInc/agent-orchestrator/releases/latest/download/agent-orchestrator-win32-x64.exe) |
| **Linux** (AppImage) | [`agent-orchestrator-linux-x64.AppImage`](https://github.com/OrchestratorInc/agent-orchestrator/releases/latest/download/agent-orchestrator-linux-x64.AppImage) |
| **Linux** (Debian/Ubuntu) | [`agent-orchestrator-linux-x64.deb`](https://github.com/OrchestratorInc/agent-orchestrator/releases/latest/download/agent-orchestrator-linux-x64.deb) |
| **Linux** (Fedora/RHEL) | [`agent-orchestrator-linux-x64.rpm`](https://github.com/OrchestratorInc/agent-orchestrator/releases/latest/download/agent-orchestrator-linux-x64.rpm) |

Ouvrez Orchestrator.inc et sélectionnez le dépôt qu'il doit gérer. L'application de bureau exécute le daemon pour vous, aucune CLI n'est donc nécessaire. Consultez le [guide d'installation](https://docs.orchestrator.inc/installation) pour configurer les CLI des agents et résoudre les problèmes.

## Documentation

| Document | Commencez ici pour |
| :-- | :-- |
| [Documentation produit](https://docs.orchestrator.inc) | Installation, configuration des agents et utilisation quotidienne du produit. |
| [docs/documentation-map.md](../docs/documentation-map.md) | Quels documents s'adressent aux humains, lesquels sont des contrats lisibles par machine, et lequel fait foi en cas de divergence. |
| [docs/architecture.md](../docs/architecture.md) | Modèle mental du backend, cycle de vie, persistance, CDC, dérivation de l'état et limites du daemon. |
| [docs/backend-code-structure.md](../docs/backend-code-structure.md) | Responsabilité des paquets et emplacement de chaque domaine du backend. |
| [docs/cli/README.md](../docs/cli/README.md) | Comportement de la CLI et correspondance avec les routes du daemon. |
| [docs/development.md](../docs/development.md) | Prérequis, étapes de compilation, exécution des tests et dépannage pour le développement local. |
| [docs/STATUS.md](../docs/STATUS.md) | Ce qui est actuellement livré sur `main` et ce qui reste en cours. |

## Contribuer

Commencez par le [guide de développement](../docs/development.md) pour connaître les prérequis, la configuration locale et les commandes de test. Lisez [CONTRIBUTING.md](../CONTRIBUTING.md) avant d'ouvrir une pull request et utilisez les [GitHub Issues](https://github.com/OrchestratorInc/agent-orchestrator/issues) pour les bugs et les demandes de fonctionnalités.

### Signaler un bug

[Ouvrez un rapport de bug](https://github.com/OrchestratorInc/agent-orchestrator/issues/new?template=bug_report.yml) depuis votre propre compte GitHub. Quelques phrases sur ce que vous avez fait et ce qui s'est mal passé suffisent ; la version, le système d'exploitation et les étapes de reproduction sont utiles si vous les avez.

Un agent de développement local peut vous aider à recueillir des éléments avec la [skill de triage des bugs](../.agents/skills/bug-triage/SKILL.md) ; placez ses journaux et ses notes en pièces jointes, et relisez le brouillon avant de l'envoyer vous-même. Merci de ne pas confier la création d'issues à un bot.

Pour obtenir de l'aide afin de décrire un problème, rejoignez le [canal de triage des bugs sur Discord](https://discord.com/channels/1476302178913357958/1491735678156013588). Consultez le [guide de contribution](../CONTRIBUTING.md#bugs-and-features) pour plus de détails.

## Communauté

Rejoignez [Discord](https://discord.com/invite/UZv7JjxbwG) pour obtenir de l'aide et échanger avec les contributeurs, suivez [@useOrchestrator](https://x.com/useOrchestrator) pour les nouveautés ou lancez une discussion dans les [GitHub Issues](https://github.com/OrchestratorInc/agent-orchestrator/issues).

## Historique des étoiles

<a href="https://www.star-history.com/#OrchestratorInc/agent-orchestrator&Date">
  <picture>
    <source media="(prefers-color-scheme: dark)" srcset="https://api.star-history.com/svg?repos=OrchestratorInc/agent-orchestrator&type=Date&theme=dark" />
    <img src="https://api.star-history.com/svg?repos=OrchestratorInc/agent-orchestrator&type=Date" alt="Graphique de l'historique des étoiles d'OrchestratorInc/agent-orchestrator" width="100%" />
  </picture>
</a>

## Télémétrie produit

Orchestrator.inc collecte une télémétrie limitée d'usage et de fiabilité, jamais votre code, vos prompts, vos conversations avec les agents ni vos identifiants de connexion. Elle inclut un identifiant d'installation et votre nom d'utilisateur GitHub ; si vous vous connectez à AO Cloud, elle inclut aussi votre identifiant utilisateur AO Cloud et l'adresse e-mail de ce compte, définie une seule fois comme propriété de personne PostHog (jamais lue depuis la configuration git). Désactivez-la entièrement dans Settings > General > Privacy > Share usage analytics, ce qui couvre l'application de bureau, le daemon et un téléphone associé. [Découvrez précisément ce que nous collectons et comment la désactiver](../docs/telemetry.md).

## Licence

Orchestrator.inc est disponible sous [licence Apache 2.0](../LICENSE).
