import { cleanup, fireEvent, render, waitFor } from "@testing-library/react";
import { afterEach, describe, expect, it, vi } from "vitest";
import { ChatMarkdown } from "../components/chat/ChatMarkdown";
import { captureExcerptSnapshot, captureExcerptTurn, excerptRenderedText, renderedExcerptRange, upgradeExcerpt } from "./chat-excerpt-snapshot";
import { codeSourceOffsets } from "./rehype-excerpt-source";
import { excerptSizeError, fitExcerptContext, formatSnapshotExcerpt, formatNumberedAnnotations, isExcerptSnapshot } from "../../shared/chat-excerpt";
import { captureFileReference } from "../../shared/chat-file-reference";
import { renderMermaidDiagram } from "./mermaid-diagram";
import type { ConversationSnapshot } from "../types/conversation";
import type { ChatDraftExcerptReference } from "./chat-drafts";

vi.mock("./mermaid-diagram", () => ({ isRenderableDiagram: () => true, renderMermaidDiagram: vi.fn(async () => "<svg><text>Start</text></svg>") }));
afterEach(cleanup);

function select(body: HTMLElement, first: string, last: string = first) {
	const rendered = excerptRenderedText(body);
	const from = rendered.text.indexOf(first);
	const to = rendered.text.lastIndexOf(last) + last.length;
	const start = rendered.entries.find(entry => entry.start <= from && entry.end > from)!;
	const end = rendered.entries.findLast(entry => entry.start < to && entry.end >= to)!;
	const range = document.createRange();
	range.setStart(start.node, from - start.start);
	range.setEnd(end.node, to - end.start);
	return range;
}

describe("captured chat excerpts", () => {
	it("captures exact source after a repaired streaming tail settles", () => {
		const source = "**finished** and `code`";
		const { container, rerender } = render(<ChatMarkdown streaming text="**finished** and `code" />);
		expect(container.querySelector("[data-excerpt-map]")).toBeNull();
		rerender(<ChatMarkdown text={source} />);
		const captured = captureExcerptSnapshot(container, select(container, "finished", "code"), source);
		expect(captured.text).toBe("finished and code");
		expect(captured.snapshot.captureMode).toBe("exact");
	});
	it.each([
		{ sourceTurnId: 123 },
		{ contextOmitted: "yes" },
		{ sourceTurnId: "t", turn: { id: "t", messages: [{ role: "assistant", text: "context" }], truncated: "yes" } },
		{ sourceTurnId: "", turn: { id: "", messages: [{ role: "assistant", text: "context" }] } },
	])("rejects malformed optional snapshot metadata: %o", metadata => {
		expect(isExcerptSnapshot({ version: 1, captureMode: "exact", rawText: "quote", ...metadata })).toBe(false);
	});
	it("captures formatted prose through code without UI labels, preserving raw syntax", () => {
		const source = "Before **bold** and *italics*.\n\n```ts\n\tgo();\n```\n\nAfter";
		const { container } = render(<ChatMarkdown text={source} />);
		const captured = captureExcerptSnapshot(container, select(container, "bold", "go();"), source);
		expect(captured.text).toBe("bold and italics.\n\tgo();");
		expect(captured.snapshot.rawText).toBe(source.slice(source.indexOf("bold"), source.indexOf("go();") + 5));
		expect(captured.snapshot.captureMode).toBe("exact");
		expect(captured.text).not.toContain("Copy");
		expect(isExcerptSnapshot(captured.snapshot)).toBe(true);
	});
	it.each([
		["Say &quot;hello&quot; &amp; bye.", '"hello" & bye', "&quot;hello&quot; &amp; bye"],
		["Escape \\*literal\\* please", "*literal*", "\\*literal\\*"],
		["Literal &unknown; remains", "unknown", "unknown"],
		["Emoji 🙂 **second**", "second", "second"],
	])("maps decoded text to its own source offsets: %s", (source, quote, raw) => {
		const { container } = render(<ChatMarkdown text={source} />);
		const captured = captureExcerptSnapshot(container, select(container, quote), source);
		expect(captured.text).toBe(quote);
		expect(captured.snapshot.rawText).toBe(raw);
		expect(captured.snapshot.captureMode).toBe("exact");
	});
	it("preserves code tabs, blank lines, entities, backslashes and CRLF offsets", () => {
		const raw = "```txt\r\n\t&quot;\\x\r\n\r\nlast\r\n```";
		const code = "\t&quot;\\x\n\nlast";
		const map = codeSourceOffsets(raw, code, 0, true)!;
		expect(map).toHaveLength(code.length + 1);
		expect(raw.slice(map[0], map[map.length - 1])).toBe("\t&quot;\\x\r\n\r\nlast");
	});

	it("maps inline code literally rather than decoding entities or backslashes", () => {
		const raw = "`` &quot;\\x\r\nnext ``";
		const code = "&quot;\\x next";
		const map = codeSourceOffsets(raw, code, 0, false)!;
		expect(map).toHaveLength(code.length + 1);
		expect(raw.slice(map[0], map[map.length - 1])).toBe("&quot;\\x\r\nnext");
	});

	it("preserves a hard line break once while retaining its raw Markdown syntax", () => {
		const source = "First  \nsecond";
		const { container } = render(<ChatMarkdown text={source} />);
		const captured = captureExcerptSnapshot(container, select(container, "First", "second"), source);
		expect(captured.text).toBe("First\nsecond");
		expect(captured.snapshot.rawText).toBe(source);
	});

	it("captures KaTeX's visible glyphs once and includes the whole formula source", () => {
		const source = "$x+y$";
		const body = document.createElement("div");
		body.innerHTML = '<span data-excerpt-block data-excerpt-start="0" data-excerpt-end="5"><span data-math="inline"><span class="katex-mathml">duplicate source</span><span class="katex-html" aria-hidden="true">x+y</span></span></span>';
		const captured = captureExcerptSnapshot(body, select(body, "x+y"), source);
		expect(captured.text).toBe("x+y");
		expect(captured.snapshot.rawText).toBe(source);
		expect(captured.snapshot.captureMode).toBe("block");
	});
	it("uses a transformed block's complete source without exposing its controls", () => {
		const source = "before\n\n```mermaid\nflowchart LR; A[Start]-->B[End]\n```\n\nafter";
		const body = document.createElement("div");
		const from = source.indexOf("```mermaid");
		const to = source.lastIndexOf("```") + 3;
		body.innerHTML = `<p>before</p><div data-excerpt-block data-excerpt-start="${from}" data-excerpt-end="${to}"><span>Start</span><button>Copy diagram</button></div><p>after</p>`;
		const captured = captureExcerptSnapshot(body, select(body, "Start"), source);
		expect(captured.text).toBe("Start");
		expect(captured.snapshot.rawText).toBe(source.slice(from, to));
		expect(captured.snapshot.captureMode).toBe("block");
	});

	it("maps a label in the production diagram renderer to the full source fence", async () => {
		const source = "Before\n\n```mermaid\nflowchart LR; A[Start]-->B[End]\n```\n\nAfter";
		const { container } = render(<ChatMarkdown text={source} />);
		await waitFor(() => expect(container.querySelector("svg text")).not.toBeNull());
		const range = document.createRange(); range.selectNodeContents(container.querySelector("svg text")!);
		const captured = captureExcerptSnapshot(container, range, source);
		expect(captured.text).toBe("");
		expect(captured.snapshot.rawText).toBe("```mermaid\nflowchart LR; A[Start]-->B[End]\n```");
		expect(captured.snapshot.captureMode).toBe("block");
		expect(captured.snapshot.containsFlowchart).toBe(true);
		expect(captured.snapshot.badgeOnly).toBe(true);
		expect(captured.snapshot.renderedRange).toBeUndefined();
		expect(captured.snapshot.diagramRange).toEqual({ start: source.indexOf("```mermaid"), end: source.lastIndexOf("```") + 3, offsetUnit: "utf-16" });
	});
	it("hides Mermaid source while preserving the complete raw block and safe navigation", async () => {
		const source = "```mermaid\nflowchart LR; A[Start]-->B[End]\n```";
		const { container, getByRole } = render(<ChatMarkdown text={source} />);
		await waitFor(() => expect(container.querySelector("svg text")).not.toBeNull());
		fireEvent.click(getByRole("button", { name: /^Code$/ }));
		const range = document.createRange(); range.selectNodeContents(container.querySelector("[data-excerpt-flowchart] pre")!);
		const captured = captureExcerptSnapshot(container, range, source);
		expect(captured.text).toBe("");
		expect(captured.snapshot).toMatchObject({ rawText: source, containsFlowchart: true, badgeOnly: true });
		expect(captured.snapshot.renderedRange).toBeUndefined();
		expect(renderedExcerptRange(container, captured.text, captured.snapshot)).toBeUndefined();
		expect(isExcerptSnapshot(captured.snapshot)).toBe(true);
	});
	it.each([false, true])("keeps prose but excludes all chart words from a mixed quote (source view: %s)", async (sourceView) => {
		const source = "Before\n\n```mermaid\nflowchart LR; A[Start]-->B[End]\n```\n\nAfter";
		const { container, getByRole } = render(<ChatMarkdown text={source} />);
		await waitFor(() => expect(container.querySelector("svg text")).not.toBeNull());
		if (sourceView) fireEvent.click(getByRole("button", { name: /^Code$/ }));
		const captured = captureExcerptSnapshot(container, select(container, "Before", "After"), source);
		expect(captured.text).toContain("Before");
		expect(captured.text).toContain("After");
		expect(captured.text).not.toContain("flowchart");
		expect(captured.text).not.toContain("Start");
		expect(captured.text).not.toContain("End");
		expect(captured.snapshot.rawText).toBe(source);
		expect(captured.snapshot.badgeOnly).toBeUndefined();
		expect(captured.snapshot.diagramRange).toEqual({ start: source.indexOf("```mermaid"), end: source.lastIndexOf("```") + 3, offsetUnit: "utf-16" });
		expect(renderedExcerptRange(container, captured.text, captured.snapshot)).toBeUndefined();
	});
	it("captures the first touched diagram independently of raw coverage across multiple blocks", () => {
		const fence = "```mermaid\nflowchart LR; A-->B\n```";
		const source = `🙂 Before\r\n\r\n${fence}\r\n\r\nBetween\r\n\r\n${fence}\r\n\r\nAfter`;
		const first = source.indexOf("```mermaid"), second = source.indexOf("```mermaid", first + 1);
		const body = document.createElement("div");
		body.innerHTML = `<p>Before</p><div data-excerpt-flowchart data-excerpt-block data-excerpt-start="${first}" data-excerpt-end="${first + fence.length}">Same</div><p>Between</p><div data-excerpt-flowchart data-excerpt-block data-excerpt-start="${second}" data-excerpt-end="${second + fence.length}">Same</div><p>After</p>`;
		const range = document.createRange(); range.selectNodeContents(body);
		const captured = captureExcerptSnapshot(body, range, source);
		expect(captured.snapshot.diagramRange).toEqual({ start: first, end: first + fence.length, offsetUnit: "utf-16" });
		expect(captured.text).not.toContain("Same");
		expect(isExcerptSnapshot(captured.snapshot)).toBe(true);
	});
	it("hides source when a diagram cannot render", async () => {
		vi.mocked(renderMermaidDiagram).mockRejectedValueOnce(new Error("Invalid diagram"));
		const source = "```mermaid\ninvalid graph\n```";
		const { container, getByText } = render(<ChatMarkdown text={source} />);
		await waitFor(() => expect(getByText(/Couldn't render this diagram/)).toBeVisible());
		const range = document.createRange(); range.selectNodeContents(container.querySelector("[data-excerpt-flowchart] pre")!);
		const captured = captureExcerptSnapshot(container, range, source);
		expect(captured.text).toBe("");
		expect(captured.snapshot.rawText).toBe(source);
		expect(captured.snapshot.badgeOnly).toBe(true);
	});
	it("numbers mixed attachments in visible order with one instruction and one shared turn", () => {
		const file = { ...captureFileReference({ path: "a.ts", side: "file", line: 1, endLine: 1, lines: [{ kind: "context", oldNo: 1, newNo: 1, text: "code();" }] }, "s"), order: 2 };
		const ref = { ...upgradeExcerpt({ id: "a", conversationId: "c", messageId: "m", revision: 1, role: "assistant", text: "quote", order: 1 }), snapshot: { version: 1, rawText: "**quote**", captureMode: "exact" as const, sourceTurnId: "t", turn: { id: "t", messages: [{ role: "assistant" as const, text: "background" }] } } };
		const text = formatNumberedAnnotations([ref, { ...ref, order: 3 }], [file]);
		expect(text.match(/Referenced annotations\./g)).toHaveLength(1);
		expect(text.indexOf("Annotation 1 — Selected text:")).toBeLessThan(text.indexOf('Annotation 2 — File: "a.ts"'));
		expect(text).toContain("Annotation 3 — Selected text:");
		expect(text.match(/Captured completed source turn/g)).toHaveLength(1);
		expect(text).toContain("**quote**");
	});

	it("maps the production math renderer's visible text to its full source", async () => {
		const source = "```math\nx+y\n```";
		const { container } = render(<ChatMarkdown text={source} />);
		await waitFor(() => expect(container.querySelector(".katex-html")).not.toBeNull(), { timeout: 5000 });
		const range = document.createRange(); range.selectNodeContents(container.querySelector(".katex-html")!);
		const captured = captureExcerptSnapshot(container, range, source);
		expect(captured.text).toBe("x+y");
		expect(captured.snapshot.rawText).toBe(source);
		expect(captured.snapshot.captureMode).toBe("block");
	});
	it("navigates to the selected occurrence rather than the first matching phrase", () => {
		const source = "same and same";
		const body = document.createElement("div"); body.textContent = source;
		const range = document.createRange(); range.setStart(body.firstChild!, 9); range.setEnd(body.firstChild!, 13);
		const captured = captureExcerptSnapshot(body, range, source);
		expect(renderedExcerptRange(body, captured.text, captured.snapshot)?.startOffset).toBe(9);
		expect(renderedExcerptRange(body, "same", { version: 1, rawText: "same", captureMode: "exact" })).toBeUndefined();
	});
	it("does not guess after a saved location and its surrounding text disappear", () => {
		const body = document.createElement("div"); body.textContent = "before quote after";
		const captured = captureExcerptSnapshot(body, select(body, "quote"), body.textContent);
		body.textContent = "unrelated quote elsewhere";
		expect(renderedExcerptRange(body, captured.text, captured.snapshot)).toBeUndefined();
	});
	it("captures a completed turn in order without requiring a user/assistant pair", () => {
		const ref: ChatDraftExcerptReference = { id: "r", conversationId: "c", messageId: "b", revision: 2, role: "assistant", text: "second", snapshot: { version: 1, rawText: "second", captureMode: "exact" } };
		const snapshot = { turns: [{ id: "turn", state: "completed" }], items: [
			{ kind: "message", id: "a", turnId: "turn", revision: 1, role: "assistant", text: "first" },
			{ kind: "message", id: "b", turnId: "turn", revision: 2, role: "assistant", text: "second" },
		] } as ConversationSnapshot;
		expect(captureExcerptTurn(ref, snapshot).snapshot?.turn?.messages.map(m => m.text)).toEqual(["first", "second"]);
		snapshot.turns[0].state = "failed";
		expect(captureExcerptTurn(ref, snapshot).snapshot?.turn).toBeUndefined();
		snapshot.items = [];
		expect(captureExcerptTurn(ref, snapshot).text).toBe("second");
	});
	it("enforces UTF-8 limits, drops only optional context, and upgrades legacy quotes", () => {
		expect(excerptSizeError([{ text: "é".repeat(13000) }])).toMatch(/24 KiB/);
		const ref = upgradeExcerpt({ id: "r", conversationId: "c", messageId: "m", revision: 1, role: "user", text: "quote" });
		expect(ref.snapshot?.captureMode).toBe("legacy");
		const rich = { ...ref, snapshot: { ...ref.snapshot!, sourceTurnId: "t", turn: { id: "t", messages: [{ role: "user" as const, text: "x".repeat(2000) }] } } };
		const fitted = fitExcerptContext([rich], "question", 1000);
		expect(fitted[0].snapshot?.contextOmitted).toBe(true);
		expect(fitted[0].text).toBe("quote");
		expect(formatSnapshotExcerpt(fitted[0])).toContain("quote");
		expect(rich.snapshot.turn).toBeDefined();
	});
});
