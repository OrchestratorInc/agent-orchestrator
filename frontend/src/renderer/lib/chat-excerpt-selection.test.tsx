import { fireEvent, render, screen } from "@testing-library/react";
import { afterEach, describe, expect, it, vi } from "vitest";
import {
	captureChatExcerpt,
	captureChatExcerptSelection,
} from "./chat-excerpt-selection";
import {
	excerptRequest,
	excerptSourceIdentity,
	validExcerptSource,
} from "../../shared/chat-excerpt";
import {
	ChatLinkProvider,
	ChatMarkdown,
} from "../components/chat/ChatMarkdown";
import { annotationTextRange } from "./chat-annotation-navigation";
import { MermaidBlock } from "../components/chat/MermaidBlock";
import { renderMermaidDiagram } from "./mermaid-diagram";
import { ActivityRow } from "../components/chat/ChatTimelineItems";
import type { ConversationActivity } from "../types/conversation";

vi.mock("./mermaid-diagram", () => ({
	isRenderableDiagram: () => true,
	renderMermaidDiagram: vi.fn(
		async () => "<svg><text>Actual diagram label</text></svg>",
	),
}));

afterEach(() => {
	window.getSelection()?.removeAllRanges();
	vi.mocked(renderMermaidDiagram).mockClear();
});
const finished = new Set(["turn"]);
function select(root: HTMLElement) {
	const range = document.createRange();
	range.selectNodeContents(root);
	return range;
}
function capture(root: HTMLElement, range = select(root)) {
	return captureChatExcerpt(range, root, "main", finished);
}
function Message({ children }: { children: React.ReactNode }) {
	return (
		<div
			data-chat-message-id="reply"
			data-chat-message-revision="3"
			data-chat-message-role="assistant"
			data-chat-excerpt-turn-id="turn"
		>
			<div data-chat-message-body="">{children}</div>
		</div>
	);
}

describe("content-only chat selection", () => {
	it.each(["powershell", "sh", ""])(
		"keeps prose and %s code but not its header or Copy controls",
		(language) => {
			const { container } = render(
				<Message>
					<ChatMarkdown
						text={`Before Code and Diagram.\n\n\`\`\`${language}\nWrite-Output 'Copy Copied'\n\`\`\`\n\nAfter.`}
					/>
				</Message>,
			);
			const excerpt = capture(container)!;
			expect(excerpt.messageId).toBe("reply");
			expect(excerpt.text).toContain("Before Code and Diagram.");
			expect(excerpt.text).toContain("Write-Output 'Copy Copied'");
			expect(excerpt.text).toContain("After.");
			if (language) expect(excerpt.text).not.toContain(language);
			expect(excerpt.text.match(/Copy/g)).toHaveLength(1);
			expect(screen.getByRole("button", { name: "Copy code" })).toBeEnabled();
			expect(
				screen.getByRole("button", { name: "Wrap long lines" }),
			).toBeEnabled();
		},
	);

	it("captures selected prose, command explanation, command, input and output as one ordered annotation", () => {
		const command: ConversationActivity = {
			kind: "activity",
			id: "cmd",
			turnId: "turn",
			sequence: 2,
			revision: 4,
			activityKind: "command",
			status: "completed",
			summary: "Ran powershell",
			detail: {
				command: "Write-Output 'Copy'",
				reason: "Inspect Code",
				terminalInput: "plain input",
				output: "Copied\nDiagram ✓",
			},
			createdAt: "2026-11-01T00:00:00Z",
		};
		const { container } = render(
			<div>
				<Message>
					<p>Before.</p>
				</Message>
				<ActivityRow activity={command} />
			</div>,
		);
		fireEvent.click(screen.getByRole("button", { expanded: false }));
		const excerpt = capture(container)!;
		expect(
			excerpt.segments?.map((part) => [part.kind, part.field, part.text]),
		).toEqual([
			["message", undefined, "Before."],
			["command", "explanation", "Inspect Code"],
			["command", "command", "Write-Output 'Copy'"],
			["command", "terminalInput", "plain input"],
			["command", "output", "Copied\nDiagram ✓"],
		]);
		expect(excerpt.messageId).toBe("");
		expect(excerpt.revision).toBe(0);
		expect(validExcerptSource(excerpt)).toBe(true);
		expect(excerpt.text).not.toContain("Agent typed");
		expect(excerptRequest(excerpt).segments).toEqual(excerpt.segments);
	});

	it("clips text at the real selection boundaries", () => {
		const { container } = render(
			<Message>
				<p>Prefix selected suffix.</p>
			</Message>,
		);
		const text = container.querySelector("p")!.firstChild!;
		const range = document.createRange();
		range.setStart(text, 7);
		range.setEnd(text, 15);
		expect(capture(container, range)?.text).toBe("selected");
	});

	it("preserves leading indentation and trailing line breaks in selected command output", () => {
		const text = "  indented ✓\n\n";
		const { container } = render(
			<div
				data-chat-activity-id="command"
				data-chat-activity-revision="1"
				data-chat-excerpt-turn-id="turn"
				data-chat-activity-settled="true"
			>
				<pre data-chat-command-field="output">{text}</pre>
			</div>,
		);
		const excerpt = capture(container)!;
		expect(excerpt.text).toBe(text);
		expect(excerpt.segments?.[0].text).toBe(text);
		expect(validExcerptSource(excerpt)).toBe(true);
	});

	it("preserves selected whitespace in a pre-wrapped user message within a mixed range", () => {
		const text = "  user indented\n";
		const { container } = render(
			<div>
				<div
					data-chat-message-id="user"
					data-chat-message-revision="1"
					data-chat-message-role="user"
					data-chat-excerpt-turn-id="turn"
				>
					<p data-chat-message-body="">{text}</p>
				</div>
				<div
					data-chat-activity-id="command"
					data-chat-activity-revision="1"
					data-chat-excerpt-turn-id="turn"
					data-chat-activity-settled="true"
				>
					<pre data-chat-command-field="output">done</pre>
				</div>
			</div>,
		);
		const excerpt = capture(container)!;
		expect(excerpt.segments?.[0].text).toBe(text);
		expect(excerpt.text).toBe(`${text}\n\ndone`);
		expect(validExcerptSource(excerpt)).toBe(true);
	});

	it("preserves actual spaces at partial prose selection boundaries", () => {
		const { container } = render(
			<Message>
				<p>Before selected text after</p>
			</Message>,
		);
		const node = container.querySelector("p")!.firstChild!;
		const range = document.createRange();
		range.setStart(node, 6);
		range.setEnd(node, 21);
		expect(capture(container, range)?.text).toBe(" selected text ");
	});

	it("preserves a mixed selection starting within an indented Markdown code block", () => {
		const text = "    return value\n";
		const { container } = render(
			<div>
				<Message>
					<ChatMarkdown text={"```python\n" + text + "```"} />
				</Message>
				<div
					data-chat-activity-id="command"
					data-chat-activity-revision="1"
					data-chat-excerpt-turn-id="turn"
					data-chat-activity-settled="true"
				>
					<pre data-chat-command-field="output">done</pre>
				</div>
			</div>,
		);
		const code = container.querySelector("pre code")!;
		const output = container.querySelector(
			'[data-chat-command-field="output"]',
		)!;
		const range = document.createRange();
		range.setStart(code, 0);
		range.setEnd(output, output.childNodes.length);
		const excerpt = capture(container, range)!;
		expect(excerpt.segments?.[0].text).toBe(text.trimEnd());
		expect(excerpt.text.startsWith("    return value")).toBe(true);
	});

	it("excludes ignored ancestors even when a selection starts or ends inside their text", () => {
		const { container } = render(
			<Message>
				<p>Before</p>
				<div data-chat-selection-ignore="">
					<span>powershell</span>
					<button>Copy</button>
				</div>
				<pre>command</pre>
			</Message>,
		);
		const button = screen.getByRole("button", { name: "Copy" }).firstChild!;
		const code = container.querySelector("pre")!.firstChild!;
		const before = container.querySelector("p")!.firstChild!;
		const range = document.createRange();
		range.setStart(button, 1);
		range.setEnd(button, 3);
		expect(capture(container, range)).toBeUndefined();
		range.setStart(button, 1);
		range.setEnd(code, 4);
		expect(capture(container, range)?.text).toBe("comm");
		range.setStart(before, 0);
		range.setEnd(button, 3);
		expect(capture(container, range)?.text).toBe("Before");
	});

	it("preserves clickable workspace-file text and finds it during navigation", () => {
		const { container } = render(
			<ChatLinkProvider workspacePaths={["src/file.ts"]} onFileOpen={vi.fn()}>
				<Message>
					<ChatMarkdown text="Inspect `src/file.ts` now." />
				</Message>
			</ChatLinkProvider>,
		);
		expect(
			screen.getByRole("button", { name: "Open src/file.ts in Files" }),
		).toBeEnabled();
		const excerpt = capture(container)!;
		expect(excerpt.text).toBe("Inspect src/file.ts now.");
		expect(annotationTextRange(container, excerpt.text)?.toString()).toBe(
			excerpt.text,
		);
	});

	it("preserves table-header and body cell boundaries, including partial cells", () => {
		const { container } = render(
			<Message>
				<ChatMarkdown
					text={"| Name | Value |\n| --- | --- |\n| Code | Copy |"}
				/>
			</Message>,
		);
		const excerpt = capture(container)!;
		expect(excerpt.text.replace(/\s+/g, " ")).toBe("Name Value Code Copy");
		expect(annotationTextRange(container, excerpt.text)).toBeDefined();
		const range = document.createRange();
		range.setStart(container.querySelector("th")!.firstChild!, 1);
		range.setEnd(container.querySelectorAll("td")[1].firstChild!, 2);
		expect(capture(container, range)?.text.replace(/\s+/g, " ")).toBe(
			"ame Value Code Co",
		);
	});

	it("rejects stale nodes, running commands and selections crossing turns without changing the native selection", () => {
		const { container } = render(
			<div>
				<Message>Before.</Message>
				<div
					data-chat-activity-id="cmd"
					data-chat-activity-revision="2"
					data-chat-excerpt-turn-id="other"
					data-chat-activity-settled="true"
				>
					<pre data-chat-command-field="output">output</pre>
				</div>
			</div>,
		);
		const range = select(container);
		const selection = window.getSelection()!;
		selection.addRange(range);
		expect(() => capture(container, range)).toThrow("turn");
		expect(selection.toString()).toContain("output");
		container.querySelector<HTMLElement>(
			"[data-chat-activity-id]",
		)!.dataset.chatActivitySettled = "false";
		expect(() => capture(container, range)).toThrow("changing");
		container.remove();
		expect(capture(container, range)).toBeUndefined();
	});

	it("enforces segment and byte budgets before capture", () => {
		const { container, unmount } = render(
			<Message>{"x".repeat(24 * 1024 + 1)}</Message>,
		);
		expect(() => capture(container)).toThrow("bytes");
		unmount();
		const fixture = document.createElement("div");
		document.body.append(fixture);
		fixture.innerHTML = Array.from(
			{ length: 33 },
			(_, i) =>
				`<div data-chat-activity-id="a${i}" data-chat-activity-revision="1" data-chat-activity-settled="true" data-chat-excerpt-turn-id="turn"><pre data-chat-command-field="output">${i}</pre></div>`,
		).join("");
		try {
			expect(() => capture(fixture)).toThrow("segments");
		} finally {
			fixture.remove();
		}
	});

	it("keeps identical selected text from different command sources independent", () => {
		const a = {
			conversationId: "main",
			messageId: "",
			revision: 0,
			text: "same",
			segments: [
				{
					kind: "command" as const,
					sourceId: "a",
					revision: 1,
					field: "output" as const,
					text: "same",
				},
			],
		};
		expect(excerptSourceIdentity(a)).not.toBe(
			excerptSourceIdentity({
				...a,
				segments: [{ ...a.segments[0], sourceId: "b" }],
			}),
		);
		expect(validExcerptSource({ ...a, messageId: "legacy" })).toBe(false);
		expect(validExcerptSource({ ...a, text: "forged" })).toBe(false);
	});

	it("rejects selected rendered diagrams and preserves Mermaid source in Code view", async () => {
		const code = "flowchart LR\n A[Code] --> B[Diagram Copy]";
		const { container } = render(
			<Message>
				<p>Before.</p>
				<MermaidBlock code={code} />
				<p>After.</p>
			</Message>,
		);
		await screen.findByTestId("mermaid-diagram");
		expect(() => capture(container)).toThrow("diagram");
		fireEvent.click(screen.getByRole("button", { name: "Code" }));
		const excerpt = capture(container)!;
		expect(excerpt.text).toContain(code);
		expect(excerpt.text).not.toContain("mermaid");
		expect(excerpt.text).not.toContain("Actual diagram label");
		expect(excerpt.text.match(/Diagram/g)).toHaveLength(1);
		expect(screen.getByRole("button", { name: "Diagram" })).toBeEnabled();
		const range = select(container.querySelector("pre")!);
		const previous = capture(container, range)!;
		fireEvent.click(screen.getByRole("button", { name: "Diagram" }));
		expect(capture(container, range)).not.toEqual(previous);
	});

	it("excludes rendering failure notices while preserving fallback source", async () => {
		vi.mocked(renderMermaidDiagram).mockRejectedValueOnce(
			new Error("invalid diagram"),
		);
		const { container } = render(
			<Message>
				<MermaidBlock code={"flowchart LR\n A --> B"} />
			</Message>,
		);
		await screen.findByText(/Couldn't render this diagram/);
		expect(capture(container)?.text).toBe("flowchart LR\n A --> B");
	});
});

describe("unsupported selection results", () => {
	it.each([
		{ output: { text: "structured" } },
		{ output: "\x1b[32mcolored\x1b[0m" },
		{ output: "10%\r20%" },
		{ terminalInput: "\x03" },
	])("rejects transformed fields without partial capture: %j", (detail) => {
		const activity: ConversationActivity = {
			kind: "activity",
			id: "cmd",
			turnId: "turn",
			sequence: 2,
			revision: 1,
			activityKind: "command",
			status: "completed",
			summary: "Ran",
			detail: detail as ConversationActivity["detail"],
			createdAt: "2026-11-01T00:00:00Z",
		};
		const { container } = render(
			<div>
				<Message>plain</Message>
				<ActivityRow activity={activity} />
			</div>,
		);
		fireEvent.click(screen.getByRole("button", { expanded: false }));
		expect(
			captureChatExcerptSelection(
				select(container),
				container,
				"main",
				finished,
			),
		).toEqual({ kind: "unsupported", reason: "formatted" });
	});
});
