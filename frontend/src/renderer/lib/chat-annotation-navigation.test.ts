import { describe, expect, it } from "vitest";
import { annotationTextMatches, annotationTextRange, highlightChatAnnotation } from "./chat-annotation-navigation";

describe("annotation navigation highlights", () => {
	it("matches across formatted spans and collapsed whitespace", () => {
		const body = document.createElement("div");
		body.innerHTML = "Read <strong>this</strong>   <a>link</a> now";
		expect(annotationTextRange(body, "this link")?.toString()).toBe("this   link");
	});
	it("falls back to rendered text when a range cannot be created", () => {
		const body = document.createElement("div");
		body.innerHTML = '<button>copy</button><p>Selected text from the message</p>';
		expect(annotationTextMatches(body, "Selected text from the message")).toBe(true);
	});
	it("highlights only the message body without changing text selection", () => {
		const source = document.createElement("div");
		source.dataset.chatMessageRevision = "3";
		source.innerHTML = '<button>1 annotation</button><p data-chat-message-body>source text</p><button>Copy</button>';
		const body = source.querySelector<HTMLElement>("p")!;
		const cleanup = highlightChatAnnotation(source, "no longer matching", 2);
		expect(body).toHaveClass("chat-annotation-target");
		expect(window.getSelection()?.rangeCount).toBe(0);
		cleanup();
		expect(body).not.toHaveClass("chat-annotation-target");
	});
});
