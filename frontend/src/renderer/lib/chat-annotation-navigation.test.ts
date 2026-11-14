import { describe, expect, it } from "vitest";
import { annotationTextMatches, annotationTextRange, annotationDiagramBlock, annotationReferenceRange, highlightChatAnnotation, renderedSnippetText } from "./chat-annotation-navigation";
import type { ChatExcerptSnapshot } from "../../shared/chat-excerpt";

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

describe("diagram annotation navigation", () => {
	function fixture() {
		const source = document.createElement("div");
		source.dataset.chatMessageRevision = "2";
		source.innerHTML = '<div data-chat-message-body><div data-excerpt-flowchart data-excerpt-start="10" data-excerpt-end="40">same label</div><p>Between</p><div data-excerpt-flowchart data-excerpt-start="100" data-excerpt-end="130">same label</div></div>';
		const snapshot: ChatExcerptSnapshot = { version: 1, rawText: "raw", captureMode: "block", containsFlowchart: true, sourceRange: { start: 0, end: 150, offsetUnit: "utf-16" }, diagramRange: { start: 100, end: 130, offsetUnit: "utf-16" } };
		return { source, snapshot };
	}
	it("resolves the selected block even when two diagrams have identical labels", () => {
		const { source, snapshot } = fixture();
		expect(annotationDiagramBlock(source, { revision: 2, snapshot })).toBe(source.querySelectorAll("[data-excerpt-flowchart]")[1]);
		expect(annotationReferenceRange(source, { text: "same label", revision: 2, snapshot })).toBeUndefined();
	});
	it("does not guess after a source revision change or when a block is missing or ambiguous", () => {
		const { source, snapshot } = fixture();
		expect(annotationDiagramBlock(source, { revision: 1, snapshot })).toBeUndefined();
		expect(annotationDiagramBlock(source, { snapshot })).toBeUndefined();
		const block = source.querySelectorAll("[data-excerpt-flowchart]")[1];
		block.remove();
		expect(annotationDiagramBlock(source, { revision: 2, snapshot })).toBeUndefined();
		source.querySelector("[data-chat-message-body]")!.append(block, block.cloneNode(true));
		expect(annotationDiagramBlock(source, { revision: 2, snapshot })).toBeUndefined();
	});
	it("supports old exact-block captures without guessing within broader old captures", () => {
		const { source, snapshot } = fixture();
		snapshot.diagramRange = undefined;
		expect(annotationDiagramBlock(source, { revision: 2, snapshot })).toBeUndefined();
		snapshot.sourceRange = { start: 100, end: 130, offsetUnit: "utf-16" };
		expect(annotationDiagramBlock(source, { revision: 2, snapshot })).toBe(source.querySelectorAll("[data-excerpt-flowchart]")[1]);
	});
});

describe("renderedSnippetText", () => {
	it("drops inline Markdown so a raw snippet line matches the rendered message", () => {
		expect(renderedSnippetText("- Run **`npm test`** and see [the docs](https://example.com)")).toBe("Run npm test and see the docs");
		expect(renderedSnippetText("## Heading  text")).toBe("Heading text");
		expect(renderedSnippetText("> quoted *line*")).toBe("quoted line");
		expect(renderedSnippetText("2. second step")).toBe("second step");
	});
	it("keeps the rendered text findable in the transcript", () => {
		const body = document.createElement("div");
		body.innerHTML = "<ul><li>Run <strong><code>npm test</code></strong> and see <a>the docs</a></li></ul>";
		expect(annotationTextRange(body, renderedSnippetText("- Run **`npm test`** and see [the docs](https://example.com)"))).toBeDefined();
	});
});
