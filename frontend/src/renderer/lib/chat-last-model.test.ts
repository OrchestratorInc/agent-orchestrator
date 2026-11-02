import { beforeEach, describe, expect, it } from "vitest";
import { readChatLastModel, rememberChatLastModel } from "./chat-last-model";

describe("chat last model", () => {
	beforeEach(() => window.localStorage.clear());

	it("names the model a chat last ran under the same agent only", () => {
		rememberChatLastModel("chat-1", "cursor", "composer-2.5-fast");
		expect(readChatLastModel("chat-1", "cursor")).toBe("composer-2.5-fast");
		expect(readChatLastModel("chat-1", "codex")).toBeUndefined();
		expect(readChatLastModel("chat-2", "cursor")).toBeUndefined();
	});

	it("reads nothing from corrupt storage", () => {
		window.localStorage.setItem("ao.chatLastModel.v1", "{not json");
		expect(readChatLastModel("chat-1", "cursor")).toBeUndefined();
		rememberChatLastModel("chat-1", "cursor", "auto");
		expect(readChatLastModel("chat-1", "cursor")).toBe("auto");
	});

	it("keeps the most recent chats", () => {
		for (let index = 0; index < 505; index++) rememberChatLastModel(`chat-${index}`, "codex", "gpt-x");
		expect(readChatLastModel("chat-0", "codex")).toBeUndefined();
		expect(readChatLastModel("chat-504", "codex")).toBe("gpt-x");
	});
});
