import { expect, it } from "vitest";
import { IMPORTABLE_AGENTS } from "./SessionImportPanel";

it("gates session import to the harnesses with a verified import + resume path", () => {
	// Kimi joins Claude Code and Codex; its chats import and resume by the native
	// session_<uuid> id over ACP session/load.
	expect([...IMPORTABLE_AGENTS].sort()).toEqual(["claude-code", "codex", "kimi"]);
});
