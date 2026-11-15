import { describe, expect, it } from "vitest";
import { controllerStoppedBanner, errorBanner, mcpNoticeKey, mcpNoticeText, reauthBanner, rolledBackBanner, threadBanner } from "./conversationBanners";

describe("conversation banners", () => {
	it("says which tool servers did not start in the desktop's words", () => {
		expect(mcpNoticeText([
			{ name: "canva", status: "failed", error: "The canva MCP server is not logged in. Run `codex mcp login canva`." },
			{ name: "posthog", status: "failed", failureReason: "reauthenticationRequired", error: "long text" },
		])).toBe("Canva, Posthog didn’t start. Continuing without them.");
		expect(mcpNoticeText([{ name: "notion", status: "failed" }])).toBe("Notion didn’t start. Continuing without it.");
		expect(mcpNoticeText([])).toBeUndefined();
	});

	it("gives each run of a session its own tool-server note", () => {
		const first = mcpNoticeKey("h_1", "ao-1", "2026-11-09T10:00:00Z");
		expect(mcpNoticeKey("h_1", "ao-1", "2026-11-09T10:00:00Z")).toBe(first);
		expect(mcpNoticeKey("h_1", "ao-1", "2026-11-09T11:00:00Z")).not.toBe(first);
		expect(mcpNoticeKey("h_2", "ao-1", "2026-11-09T10:00:00Z")).not.toBe(first);
		expect(mcpNoticeKey(undefined, "ao-1", undefined)).toBe(":ao-1:");
	});

	it("brings a closed banner back only when what it reports changes", () => {
		expect(rolledBackBanner(1)?.key).toBe(rolledBackBanner(1)?.key);
		expect(rolledBackBanner(1)?.key).not.toBe(rolledBackBanner(2)?.key);
	});

	it("uses the desktop's short headlines", () => {
		expect(reauthBanner("t", "codex login")).toMatchObject({ title: "Sign in again to keep going", body: "Run “codex login” on the Orchestrator host, then send again." });
		expect(threadBanner("system_error")?.title).toBe("The agent's thread hit an internal error");
		expect(threadBanner("closed")?.title).toBe("The agent closed this thread");
		expect(threadBanner("active")).toBeUndefined();
		expect(controllerStoppedBanner(true).title).toBe("This session is terminated");
		expect(rolledBackBanner(2)?.title).toBe("2 turns were rolled back");
		expect(rolledBackBanner(0)).toBeUndefined();
		expect(errorBanner("action", "boom")).toMatchObject({ title: "That didn't work", body: "boom" });
	});
});
