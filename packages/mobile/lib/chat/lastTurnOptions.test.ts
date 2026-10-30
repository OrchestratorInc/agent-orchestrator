import { beforeEach, expect, it } from "vitest";
import { forgetRememberedTurnOptions, rememberedTurnOptions, rememberTurnOptions } from "./lastTurnOptions";
import type { ChatConfigOption } from "./types";

const option = (value: string): ChatConfigOption => ({ id: "model", name: "Model", category: "model", type: "select", currentValue: value, choices: [{ value, name: value }] });
const catalog = (value: string) => ({ models: [], configOptions: [option(value)] });

beforeEach(() => forgetRememberedTurnOptions());

it("remembers a catalog with choices and ignores an empty one", () => {
	rememberTurnOptions("chat", catalog("opus"));
	rememberTurnOptions("chat", { models: [], configOptions: [] });
	rememberTurnOptions("", catalog("sonnet"));
	expect(rememberedTurnOptions("chat")?.configOptions[0]?.currentValue).toBe("opus");
	expect(rememberedTurnOptions("")).toBeUndefined();
});

it("keeps the 32 chats used most recently", () => {
	for (let index = 0; index < 33; index++) rememberTurnOptions(`chat-${index}`, catalog(`m${index}`));
	expect(rememberedTurnOptions("chat-0")).toBeUndefined();
	expect(rememberedTurnOptions("chat-32")?.configOptions[0]?.currentValue).toBe("m32");

	rememberTurnOptions("chat-1", catalog("again"));
	rememberTurnOptions("chat-33", catalog("m33"));
	expect(rememberedTurnOptions("chat-1")?.configOptions[0]?.currentValue).toBe("again");
	expect(rememberedTurnOptions("chat-2")).toBeUndefined();
});

it("forgets every remembered catalog", () => {
	rememberTurnOptions("a", catalog("opus"));
	rememberTurnOptions("b", catalog("sonnet"));
	forgetRememberedTurnOptions();
	expect(rememberedTurnOptions("a")).toBeUndefined();
	expect(rememberedTurnOptions("b")).toBeUndefined();
});
