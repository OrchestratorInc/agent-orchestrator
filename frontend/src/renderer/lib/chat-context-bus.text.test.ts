import { describe, expect, it, vi } from "vitest";
import { sendTextToChat, subscribeChatComposerText, subscribeChatReveal } from "./chat-context-bus";

describe("quoted text for the chat composer", () => {
	it("is refused while no composer listens, and accepted and revealed once one does", () => {
		expect(sendTextToChat("s1", "> a comment")).toBe(false);
		const received: string[] = [];
		const reveal = vi.fn();
		const stopText = subscribeChatComposerText("s1", (text) => { received.push(text); });
		const stopReveal = subscribeChatReveal("s1", reveal);
		expect(sendTextToChat("s1", "> a comment")).toBe(true);
		expect(received).toEqual(["> a comment"]);
		expect(reveal).toHaveBeenCalledTimes(1);
		stopText();
		stopReveal();
		expect(sendTextToChat("s1", "> again")).toBe(false);
	});

	it("is refused when the composer turns it down, such as while a message is being sent", () => {
		const stop = subscribeChatComposerText("s2", () => false);
		expect(sendTextToChat("s2", "> a comment")).toBe(false);
		stop();
	});
});
