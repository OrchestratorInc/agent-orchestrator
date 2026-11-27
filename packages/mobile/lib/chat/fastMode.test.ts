import { describe, expect, it } from "vitest";
import { FAST_MODE_SHOWN } from "./fastMode";
import { turnSettingsRows, turnSettingTopic } from "./turnSettingsModel";
import type { ChatConfigOption, ChatModel, ConversationSnapshot } from "./types";

const chat = (harness: string, settings: ConversationSnapshot["settings"] = {}): ConversationSnapshot => ({
	conversationId: "c", sessionId: "s", harness, mode: "chat", controller: { state: "ready" },
	latestSequence: 0, oldestSequence: 0, hasMoreBefore: false, turns: [], items: [], settings, capabilities: ["config_options"],
});

// Fast mode is off for now: whatever an agent offers, no row reaches the
// effort slider's bolt, the model wheels or the menus.
describe("Fast mode, off for now", () => {
	it("is off", () => {
		expect(FAST_MODE_SHOWN).toBe(false);
	});

	it("leaves no Fast row for an agent that offers it, or a model that supports it", () => {
		const claude: ChatConfigOption[] = [
			{ id: "model", name: "Model", category: "model", type: "select", currentValue: "opus", choices: [{ value: "opus", name: "Opus 5.5" }] },
			{ id: "fast", name: "Fast mode", category: "model_config", type: "select", currentValue: "off", choices: [{ value: "on", name: "On" }, { value: "off", name: "Off" }] },
		];
		expect(turnSettingsRows(chat("claude-code"), [], claude).some((row) => turnSettingTopic(row) === "fast")).toBe(false);
		const codex: ChatModel[] = [{ id: "gpt", displayName: "GPT", default: true, supportsFast: true }];
		expect(turnSettingsRows({ ...chat("codex"), capabilities: [] }, codex, []).some((row) => turnSettingTopic(row) === "fast")).toBe(false);
	});
});
