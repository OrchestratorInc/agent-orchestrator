import type { AgentId } from "./agent-options";

/** Agents whose earlier chats AO can import as continuable sessions. */
export const IMPORTABLE_AGENTS: ReadonlySet<AgentId> = new Set<AgentId>(["claude-code", "codex", "pi", "gemini", "copilot", "omp", "kimchi", "qwen", "aider", "cline", "cursor", "grok", "opencode"]);
