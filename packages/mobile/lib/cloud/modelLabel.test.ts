import { describe, expect, it } from "vitest";
import { menuModels } from "../spawnModel";
import { cloudModelLabel } from "./modelCatalog";
import { cloudModelCatalog } from "./spawnSource";
import type { CloudChatModel } from "./types";

const cloud = (id: string, displayName: string, description?: string, isDefault = false): CloudChatModel => ({ id, displayName, ...(description ? { description } : {}), default: isDefault });

describe("cloudModelLabel", () => {
	it("names a Claude alias by the model its description says it runs", () => {
		expect(cloudModelLabel("claude-code", cloud("haiku", "Haiku", "Haiku 4.5 · Fastest for quick answers"))).toBe("Haiku 4.5");
		expect(cloudModelLabel("claude-code", cloud("opus", "Opus", "Opus 5.5 · Most capable for complex work"))).toBe("Opus 5.5");
		expect(cloudModelLabel("claude-code", cloud("sonnet[1m]", "Sonnet (1M context)", "Sonnet 4.6 with 1M context · Billed as extra usage"))).toBe("Sonnet 4.6 (1M context)");
		expect(cloudModelLabel("claude-code", cloud("opus", "Claude Opus", "Claude Opus 5 · Best for everyday tasks"))).toBe("Claude Opus 5");
	});

	it("keeps a name that has a version, a placeholder, or no model in its description", () => {
		expect(cloudModelLabel("claude-code", cloud("haiku", "Haiku 4.5", "Haiku 5.5 · Fastest"))).toBe("Haiku 4.5");
		expect(cloudModelLabel("claude-code", cloud("claude-opus-5", "Claude Opus 5"))).toBe("Claude Opus 5");
		expect(cloudModelLabel("claude-code", cloud("default", "Default (recommended)", "Opus 5.5 · Most capable"))).toBe("Default (recommended)");
		expect(cloudModelLabel("claude-code", cloud("haiku", "Haiku", "Fastest for quick answers"))).toBe("Haiku");
		expect(cloudModelLabel("claude-code", cloud("opus", "Opus", "Sonnet 4.6 · Best for everyday tasks"))).toBe("Opus");
	});

	it("leaves other agents' names alone", () => {
		expect(cloudModelLabel("codex", cloud("gpt-6-luna", "Luna", "Haiku 4.5 · not a Claude model"))).toBe("Luna");
	});
});

describe("a cloud New task's model menu", () => {
	it("names Claude's aliases with their versions, as the desktop does", () => {
		const catalog = cloudModelCatalog("claude-code", {
			models: [
				cloud("opus", "Opus", "Opus 5.5 · Most capable for complex work", true),
				cloud("fable", "Fable", "Fable 5.2 · Best for long agentic work"),
				cloud("sonnet", "Sonnet", "Sonnet 5.5 · Best for everyday tasks"),
				cloud("haiku", "Haiku", "Haiku 4.5 · Fastest for quick answers"),
			],
			source: "worker",
		});
		const menu = menuModels(catalog.models, "");
		expect(menu.defaultLabel).toBe("Opus 5.5");
		expect(menu.models.map((model) => model.label)).toEqual(["Fable 5.2", "Sonnet 5.5", "Haiku 4.5"]);
	});
});
