import { describe, expect, it } from "vitest";
import { annotationTextMatches, annotationTextRange, highlightChatAnnotation, renderedSnippetText } from "./chat-annotation-navigation";

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
	it("highlights segments across sibling visual groups of one logical turn", () => {
		const root = document.createElement("div");
		root.innerHTML = '<section data-chat-turn-source="turn"><div data-chat-message-id="reply" data-chat-message-revision="1"><p data-chat-message-body>Before</p></div></section><section data-chat-turn-source="turn"><div data-chat-activity-id="command" data-chat-activity-revision="2"><pre data-chat-command-field="output">After</pre></div></section>';
		const source = root.querySelector<HTMLElement>("[data-chat-message-id]")!;
		const cleanup = highlightChatAnnotation(source, "Before", 1, [
			{ kind: "message", sourceId: "reply", revision: 1, text: "Before" },
			{ kind: "command", sourceId: "command", revision: 2, field: "output", text: "After" },
		], root);
		expect(root.querySelectorAll(".chat-annotation-target")).toHaveLength(2);
		cleanup();
		expect(root.querySelectorAll(".chat-annotation-target")).toHaveLength(0);
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
