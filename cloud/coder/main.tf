terraform {
  required_providers {
    coder = {
      source = "coder/coder"
    }
    docker = {
      source = "kreuzwerker/docker"
    }
  }
}

variable "docker_socket" {
  default     = ""
  description = "Optional Docker socket URI"
  type        = string
}

variable "workspace_image" {
  default     = "ao-coder-workspace:local"
  description = "Approved image containing the release-matched AO worker and coding harness"
  type        = string
}

variable "workspace_memory_mb" {
  default     = 0
  description = "Hard per-workspace memory limit in MB (0 = unlimited). Set this on shared single-host deployments (e.g. the Azure Coder VM) so one workspace cannot OOM the host and take down its siblings."
  type        = number
}

variable "workspace_cpu_shares" {
  default     = 0
  description = "Relative CPU weight per workspace (0 = default/unset). On a shared host this keeps CPU fair across concurrent workspaces without a hard cap."
  type        = number
}

variable "devkit_apt_packages" {
  default     = ""
  description = "Space-separated apt packages installed at workspace start. The dev-kit templates set this; the plain default template leaves it empty so its behavior is unchanged. Installed on the already-approved base image, so no new image has to be distributed to the Coder host."
  type        = string
}

provider "docker" {
  host = var.docker_socket != "" ? var.docker_socket : null
}

data "coder_provisioner" "me" {}
data "coder_workspace" "me" {}
data "coder_workspace_owner" "me" {}

resource "coder_agent" "main" {
  arch = data.coder_provisioner.me.arch
  os   = "linux"

  startup_script = <<-EOT
    set -e
    if [ ! -f ~/.init_done ]; then
      cp -rT /etc/skel ~
      touch ~/.init_done
    fi
    DEVKIT_PKGS="${var.devkit_apt_packages}"
    if [ -n "$DEVKIT_PKGS" ]; then
      echo "Installing dev-kit tooling: $DEVKIT_PKGS"
      sudo apt-get update -qq && sudo DEBIAN_FRONTEND=noninteractive apt-get install -y --no-install-recommends $DEVKIT_PKGS || true
    fi
    # Ensure every supported coding harness can launch. The baked workspace image
    # has proven unreliable (missing codex/cursor, intermittently opencode), so
    # codex, cursor-agent, and opencode are installed here when absent, and
    # claude-agent-acp (the ChatUI ACP bridge for claude -- the only harness whose
    # ACP is a separate package; opencode/cursor speak ACP via their own CLIs) is
    # ensured too, so neither a terminal session nor a ChatUI session hangs with
    # "harness binary unavailable". Idempotent (skipped once present) and non-fatal
    # (a failed optional install never blocks the workspace). Once the image bakes
    # all of these (see Sandbox.Dockerfile), these become no-ops.
    if ! command -v codex >/dev/null 2>&1; then
      echo "Installing codex harness"
      sudo npm install --global '@openai/codex@0.147.0' || true
    fi
    if ! command -v cursor-agent >/dev/null 2>&1; then
      echo "Installing cursor-agent harness"
      sudo mkdir -p /opt/cursor-agent/2026.08.11-e8db854 && \
        curl --fail --location --silent --show-error \
          'https://downloads.cursor.com/lab/2026.08.11-e8db854/linux/x64/agent-cli-package.tar.gz' \
          | sudo tar --strip-components=1 -xzf - -C /opt/cursor-agent/2026.08.11-e8db854 && \
        sudo ln -sf /opt/cursor-agent/2026.08.11-e8db854/cursor-agent /usr/local/bin/cursor-agent || true
    fi
    if ! command -v opencode >/dev/null 2>&1; then
      echo "Installing opencode harness"
      sudo npm install --global '@opencode/cli@2' || true
    fi
    if ! command -v claude-agent-acp >/dev/null 2>&1; then
      echo "Installing claude-agent-acp (ChatUI ACP bridge for claude)"
      sudo npm install --global '@agentclientprotocol/claude-agent-acp@0.70.0' || true
    fi
    claude --version
    codex --version || echo "codex unavailable"
    cursor-agent --version || echo "cursor-agent unavailable"
    opencode --version || echo "opencode unavailable"
    command -v claude-agent-acp >/dev/null 2>&1 && echo "claude-agent-acp present" || echo "claude-agent-acp unavailable"
  EOT

  env = {
    GIT_AUTHOR_NAME     = coalesce(data.coder_workspace_owner.me.full_name, data.coder_workspace_owner.me.name)
    GIT_AUTHOR_EMAIL    = data.coder_workspace_owner.me.email
    GIT_COMMITTER_NAME  = coalesce(data.coder_workspace_owner.me.full_name, data.coder_workspace_owner.me.name)
    GIT_COMMITTER_EMAIL = data.coder_workspace_owner.me.email
  }
}

resource "docker_volume" "home_volume" {
  name = "coder-${data.coder_workspace.me.id}-home"

  lifecycle {
    ignore_changes = all
  }

  labels {
    label = "coder.owner"
    value = data.coder_workspace_owner.me.name
  }
  labels {
    label = "coder.owner_id"
    value = data.coder_workspace_owner.me.id
  }
  labels {
    label = "coder.workspace_id"
    value = data.coder_workspace.me.id
  }
  labels {
    label = "coder.workspace_name_at_creation"
    value = data.coder_workspace.me.name
  }
}

resource "docker_container" "workspace" {
  count = data.coder_workspace.me.start_count
  image = var.workspace_image

  name     = "coder-${data.coder_workspace_owner.me.name}-${lower(data.coder_workspace.me.name)}"
  hostname = data.coder_workspace.me.name
  entrypoint = [
    "sh",
    "-c",
    replace(coder_agent.main.init_script, "/localhost|127\\.0\\.0\\.1/", "host.docker.internal"),
  ]
  env = ["CODER_AGENT_TOKEN=${coder_agent.main.token}"]

  # Optional per-workspace limits (no-op when the variables are 0, so the
  # existing single-tenant deployments are unchanged). Used on shared single-host
  # deployments so one workspace cannot exhaust the host.
  memory     = var.workspace_memory_mb > 0 ? var.workspace_memory_mb : null
  cpu_shares = var.workspace_cpu_shares > 0 ? var.workspace_cpu_shares : null

  host {
    host = "host.docker.internal"
    ip   = "host-gateway"
  }

  volumes {
    container_path = "/home/coder"
    volume_name    = docker_volume.home_volume.name
    read_only      = false
  }

  labels {
    label = "coder.owner"
    value = data.coder_workspace_owner.me.name
  }
  labels {
    label = "coder.owner_id"
    value = data.coder_workspace_owner.me.id
  }
  labels {
    label = "coder.workspace_id"
    value = data.coder_workspace.me.id
  }
  labels {
    label = "coder.workspace_name"
    value = data.coder_workspace.me.name
  }
}
