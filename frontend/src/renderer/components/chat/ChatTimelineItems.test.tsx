import { act, fireEvent, render as rtlRender, screen, waitFor } from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { StrictMode, type ReactElement } from "react";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import {
	ActivityRow,
	AssistantMessage,
	ChatHostId,
	HumanMessage,
	LiveResponseStatus,
	ProviderSwitchMarker,
	TurnOutcome,
} from "./ChatTimelineItems";
import { agentModelsQueryKey } from "../../hooks/useAgentModelsQuery";
import { groupScriptRuns } from "../../lib/cloud-script-runs";
import type { ConversationActivity, ConversationMessage } from "../../types/conversation";
import { TooltipProvider } from "../ui/tooltip";

function render(ui: ReactElement) {
	const result = rtlRender(<TooltipProvider>{ui}</TooltipProvider>);
	return {
		...result,
		rerender: (nextUi: ReactElement) => result.rerender(<TooltipProvider>{nextUi}</TooltipProvider>),
	};
}

let nextFrame = 1;
let frameTime = 0;
let frames = new Map<number, FrameRequestCallback>();

function message(overrides: Partial<ConversationMessage> = {}): ConversationMessage {
	return {
		kind: "message",
		id: "assistant-1",
		sequence: 1,
		revision: 1,
		role: "assistant",
		origin: "provider",
		text: "a",
		streaming: true,
		createdAt: "2026-08-24T00:00:00Z",
		...overrides,
	};
}

function runFrame(now: number) {
	const [id, callback] = frames.entries().next().value ?? [];
	if (id === undefined || callback === undefined) throw new Error("No animation frame scheduled");
	frames.delete(id);
	frameTime = now;
	act(() => callback(now));
}

beforeEach(() => {
	nextFrame = 1;
	frameTime = 0;
	frames = new Map();
	vi.spyOn(performance, "now").mockImplementation(() => frameTime);
	vi.spyOn(window, "requestAnimationFrame").mockImplementation((callback) => {
		const id = nextFrame++;
		frames.set(id, callback);
		return id;
	});
	vi.spyOn(window, "cancelAnimationFrame").mockImplementation((id) => {
		frames.delete(id);
	});
});

afterEach(() => {
	vi.useRealTimers();
	vi.restoreAllMocks();
});

/** Run frames at ~60 Hz until the drain finishes; returns the time it took. */
function drain(from = 0, limitMs = 5000) {
	let now = from;
	while (frames.size && now - from < limitMs) runFrame((now += 16));
	return now - from;
}

describe("TurnOutcome", () => {
	it("keeps a recovered historical turn distinct from success", () => {
		render(<TurnOutcome state="recovered" />);
		expect(screen.getByText("This turn was recovered from an earlier session")).toBeInTheDocument();
		expect(screen.queryByText("Done")).not.toBeInTheDocument();
	});

	it("shows the failed outcome and the provider's explanation", () => {
		render(<TurnOutcome state="failed" error="Provider error" />);

		expect(screen.getByText("The agent ran into a problem")).toBeInTheDocument();
		expect(screen.getByText("Provider error")).toBeInTheDocument();
	});

	it("presents a non-retryable usage limit as a historical notice without controls", () => {
		render(
			<TurnOutcome
				state="failed"
				error="Manage your limit at https://example.com/billing."
				usageLimit={{ resetsAt: "2026-10-14T16:50:00Z", autoResume: false }}
			/>,
		);

		expect(screen.getByText("Usage limit reached")).toBeInTheDocument();
		expect(screen.queryByText("The agent ran into a problem")).not.toBeInTheDocument();
		expect(screen.queryByRole("button")).not.toBeInTheDocument();
		expect(screen.getByRole("link", { name: "https://example.com/billing" })).toHaveAttribute("href", "https://example.com/billing");
	});

	it("resumes an eligible limited prompt after its reset", () => {
		const onRetry = vi.fn();
		render(<TurnOutcome state="failed" usageLimit={{ resetsAt: "2020-01-01T00:00:00Z", autoResume: false }} retry={{ onRetry }} />);

		fireEvent.click(screen.getByRole("button", { name: "Resume" }));

		expect(onRetry).toHaveBeenCalledOnce();
		expect(screen.queryByText("The agent ran into a problem")).not.toBeInTheDocument();
	});

	it("waits for a limited prompt's reset before exposing manual recovery", () => {
		vi.useFakeTimers();
		vi.setSystemTime(new Date("2026-10-14T15:00:00Z"));
		const onRetry = vi.fn();
		render(<TurnOutcome state="failed" usageLimit={{ resetsAt: "2026-10-14T15:01:00Z", autoResume: false }} retry={{ onRetry }} />);

		expect(screen.queryByRole("button")).not.toBeInTheDocument();
		act(() => vi.advanceTimersByTime(60_000));

		expect(screen.getByRole("button", { name: "Resume" })).toBeEnabled();
		expect(onRetry).not.toHaveBeenCalled();
	});

	it.each([{ pending: true }, { disabled: true }])("keeps limited-prompt retry errors visible while recovery is unavailable: %j", (control) => {
		const onRetry = vi.fn();
		render(<TurnOutcome state="failed" usageLimit={{ autoResume: false }} retry={{ onRetry, error: "Stop the current turn before retrying this one", ...control }} />);

		expect(screen.getByRole("alert")).toHaveTextContent("Stop the current turn before retrying this one");
		expect(screen.getByRole("button", { name: "Resume" })).toBeDisabled();
		fireEvent.click(screen.getByRole("button", { name: "Resume" }));
		expect(onRetry).not.toHaveBeenCalled();
	});

	it("preserves multiline provider text and links without interpreting its structure", () => {
		render(
			<TurnOutcome
				state="failed"
				error={"Usage limit reached\n\nManage billing at https://example.com/billing."}
			/>,
		);

		expect(screen.getByText(/Usage limit reached/)).toBeInTheDocument();
		expect(screen.getByText(/Manage billing at/)).toBeInTheDocument();
		expect(screen.getByRole("link", { name: "https://example.com/billing" })).toHaveAttribute(
			"href",
			"https://example.com/billing",
		);
	});

	it("closes a failed turn on one action row: retry, copy, then undo", () => {
		const onRetry = vi.fn();
		const onRollback = vi.fn();
		render(
			<TurnOutcome
				state="failed"
				error="unexpected status 401 Unauthorized"
				retry={{ onRetry }}
				copyText="The partial answer"
				onRollback={onRollback}
			/>,
		);

		const actions = [
			screen.getByRole("button", { name: "Retry this turn" }),
			screen.getByRole("button", { name: "Copy message as markdown" }),
			screen.getByRole("button", { name: "Roll back to here" }),
		];
		const rows = new Set(actions.map((action) => action.closest("[data-turn-actions]")));
		expect(rows.size).toBe(1);
		expect(rows.has(null)).toBe(false);

		fireEvent.click(actions[0]!);
		fireEvent.click(actions[2]!);
		expect(onRetry).toHaveBeenCalledOnce();
		expect(onRollback).toHaveBeenCalledOnce();
	});

	it("leads the row with how long the turn ran when no fold above says so", () => {
		render(
			<TurnOutcome
				state="interrupted"
				durationMs={61_000}
				copyText="The partial answer"
				onRollback={vi.fn()}
			/>,
		);

		const duration = screen.getByText("1m");
		const copy = screen.getByRole("button", { name: "Copy message as markdown" });
		expect(duration.closest("[data-turn-actions]")).toBe(copy.closest("[data-turn-actions]"));
		expect(duration.compareDocumentPosition(copy) & Node.DOCUMENT_POSITION_FOLLOWING).toBeTruthy();
		// Alone on the row, the number carries its name for a screen reader.
		expect(duration).toHaveTextContent("Worked for 1m");
	});

	it("says why a retry was refused, under the row it came from", () => {
		render(<TurnOutcome state="failed" retry={{ onRetry: vi.fn(), error: "Stop the agent first" }} />);
		expect(screen.getByRole("alert")).toHaveTextContent("Stop the agent first");
	});

	it("draws no action row when a stopped turn has nothing to act on", () => {
		render(<TurnOutcome state="interrupted" />);
		expect(screen.getByText("The agent was interrupted by you")).toBeInTheDocument();
		expect(screen.queryByRole("button")).not.toBeInTheDocument();
	});
});

describe("LiveResponseStatus", () => {
	// The provider re-saves its retry on every attempt. The chip has to update its
	// count in place; remounting it would replay its entrance several times a turn.
	it("keeps the same retry chip while the attempt count climbs", () => {
		const view = render(
			<LiveResponseStatus startedAt={new Date().toISOString()} reconnect={{ label: "Reconnecting… 1/5", detail: "401 Unauthorized" }} />,
		);
		const chip = screen.getByTestId("live-working-task");
		expect(chip).toHaveTextContent("Reconnecting… 1/5");

		view.rerender(
			<LiveResponseStatus startedAt={new Date().toISOString()} reconnect={{ label: "Reconnecting… 2/5", detail: "401 Unauthorized, again" }} />,
		);
		expect(screen.getByTestId("live-working-task")).toBe(chip);
		expect(chip).toHaveTextContent("Reconnecting… 2/5");
		expect(screen.getByTestId("live-turn-status")).toHaveTextContent("401 Unauthorized, again");
	});

	it("updates the step label in place as the step changes, without replaying its entrance", () => {
		const view = render(<LiveResponseStatus startedAt={new Date().toISOString()} task="Search" />);
		const label = screen.getByTestId("live-working-task");

		view.rerender(<LiveResponseStatus startedAt={new Date().toISOString()} task="Read file" />);
		expect(screen.getByTestId("live-working-task")).toBe(label);
		expect(label).toHaveTextContent("Read file");
	});

	it("names the current step beside the timer, and only the timer between steps", async () => {
		const view = render(<LiveResponseStatus startedAt={new Date().toISOString()} task="Search" />);
		expect(screen.getByTestId("live-working-task")).toHaveTextContent("Search");
		expect(screen.getByTestId("live-working-timer")).toHaveTextContent(/^\d+[smh]/);

		view.rerender(<LiveResponseStatus startedAt={new Date().toISOString()} />);
		// The old label fades out before it leaves.
		await waitFor(() => expect(screen.queryByTestId("live-working-task")).not.toBeInTheDocument());
		expect(screen.getByTestId("live-working-timer")).toBeInTheDocument();
	});
});

describe("AssistantMessage streaming", () => {
	it("shows completed messages without a duplicate animation segmentation pass", () => {
		const segment = vi.spyOn(Intl.Segmenter.prototype, "segment");
		const text = "Completed 👨‍👩‍👧‍👦 é answer";
		render(<AssistantMessage message={message({ text, streaming: false })} />);
		expect(document.querySelector("p")?.textContent).toBe(text);
		// Markdown may segment for emoji typography; settled text needs no
		// additional pass for the streaming animation.
		expect(segment.mock.calls.filter(([input]) => input === text).length).toBeLessThanOrEqual(1);
	});

	it("resumes a completed message without repeating its visible prefix", () => {
		const prefix = "Hello 👨‍👩‍👧‍👦 é";
		const text = prefix + " continued".repeat(20);
		const view = render(<AssistantMessage message={message({ text: prefix, streaming: false })} />);
		view.rerender(<AssistantMessage message={message({ text, streaming: true, revision: 2 })} />);

		runFrame(0);
		runFrame(100);
		const visible = document.querySelector("p")?.textContent ?? "";
		expect(visible.startsWith(prefix)).toBe(true);
		expect(text.startsWith(visible)).toBe(true);

		drain(100);
		expect(document.querySelector("p")?.textContent).toBe(text);
		expect(frames.size).toBe(0);
	});

	it("shows the first durable snapshot and a replacement message immediately", () => {
		const text = "A first snapshot 👨‍👩‍👧‍👦";
		const view = render(<AssistantMessage message={message({ text })} />);
		expect(document.querySelector("p")?.textContent).toBe(text);
		expect(frames.size).toBe(0);

		view.rerender(<AssistantMessage message={message({ text: text + " buffered" })} />);
		runFrame(0);
		view.rerender(<AssistantMessage message={message({ id: "assistant-2", text: "New message" })} />);
		expect(document.querySelector("p")?.textContent).toBe("New message");
		expect(frames.size).toBe(0);
	});

	it("shows a provider correction immediately and starts a fresh drain afterward", () => {
		const view = render(<AssistantMessage message={message()} />);
		view.rerender(<AssistantMessage message={message({ text: "a".padEnd(2000, "x") })} />);
		runFrame(0);
		runFrame(50);
		view.rerender(<AssistantMessage message={message({ text: "Corrected" })} />);
		expect(document.querySelector("p")?.textContent).toBe("Corrected");
		expect(frames.size).toBe(0);

		view.rerender(<AssistantMessage message={message({ text: "Corrected text" })} />);
		runFrame(190);
		expect(document.querySelector("p")?.textContent).not.toBe("Corrected text");
		drain(230);
		expect(document.querySelector("p")?.textContent).toBe("Corrected text");
	});

	it("does not force a character on high-refresh frames", () => {
		const view = render(<AssistantMessage message={message()} />);
		view.rerender(<AssistantMessage message={message({ text: "abcdefghij" })} />);

		runFrame(0);
		runFrame(8);

		expect(screen.getByText("a")).toBeInTheDocument();
		expect(screen.queryByText("ab")).not.toBeInTheDocument();
	});

	it("shows the current snapshot immediately when an occluded tab resumes", () => {
		const view = render(<AssistantMessage message={message()} />);
		view.rerender(<AssistantMessage message={message({ text: "a".padEnd(2000, "x") })} />);

		runFrame(0);
		runFrame(5 * 60 * 1000);
		const rendered = document.querySelector("p");

		expect(rendered?.textContent).toBe("a".padEnd(2000, "x"));
	});

	it("flushes on resume when a hidden tab received no initial animation frame", () => {
		const view = render(<AssistantMessage message={message()} />);
		const text = "a".padEnd(2000, "x");
		view.rerender(<AssistantMessage message={message({ text })} />);

		runFrame(5 * 60 * 1000);

		expect(document.querySelector("p")?.textContent).toBe(text);
		expect(frames.size).toBe(0);
	});

	it("spreads a large received burst over several frames instead of dumping it", () => {
		const view = render(<AssistantMessage message={message()} />);
		const text = "a".padEnd(10_000, "x");
		view.rerender(<AssistantMessage message={message({ text })} />);

		runFrame(0);
		runFrame(48);
		const partial = document.querySelector("p")?.textContent ?? "";
		expect(partial.length).toBeGreaterThan(1);
		expect(partial.length).toBeLessThan(text.length);

		drain(48);
		expect(document.querySelector("p")?.textContent).toBe(text);
		expect(frames.size).toBe(0);
	});

	it("segments each snapshot once and reuses it across animation frames", () => {
		const segment = vi.spyOn(Intl.Segmenter.prototype, "segment");
		const view = render(<AssistantMessage message={message()} />);
		const text = "a".padEnd(2000, "x");
		view.rerender(<AssistantMessage message={message({ text })} />);
		runFrame(0);
		runFrame(16);
		runFrame(32);

		expect(segment.mock.calls.filter(([input]) => input === text)).toHaveLength(1);
	});

	it("keeps emoji and combining sequences intact while streaming", () => {
		const view = render(<AssistantMessage message={message()} />);
		view.rerender(<AssistantMessage message={message({ text: "a👨‍👩‍👧‍👦e\u0301" })} />);

		runFrame(0);
		runFrame(1000);

		expect(document.querySelector("p")?.textContent).toBe("a👨‍👩‍👧‍👦e\u0301");
	});

	it("reconciles a grapheme when a later snapshot adds a ZWJ", () => {
		const view = render(<AssistantMessage message={message()} />);
		view.rerender(<AssistantMessage message={message({ text: "a👨" })} />);
		drain();
		expect(document.querySelector("p")?.textContent).toBe("a👨");

		view.rerender(<AssistantMessage message={message({ text: "a👨‍👩" })} />);
		expect(document.querySelector("p")?.textContent).toBe("a");
		drain();

		expect(document.querySelector("p")?.textContent).toBe("a👨‍👩");
	});

	it("reconciles a later combining mark without skipping the following grapheme", () => {
		const view = render(<AssistantMessage message={message({ text: "ae" })} />);
		view.rerender(<AssistantMessage message={message({ text: "ae\u0301z" })} />);
		expect(document.querySelector("p")?.textContent).toBe("a");
		runFrame(0);
		runFrame(20);
		expect(document.querySelector("p")?.textContent).toBe("ae\u0301");
		runFrame(40);
		expect(document.querySelector("p")?.textContent).toBe("ae\u0301z");
	});

	it("shows the latest snapshot immediately when reduced motion is requested", () => {
		vi.spyOn(window, "matchMedia").mockImplementation((query) => ({
			matches: query === "(prefers-reduced-motion: reduce)",
			media: query,
			onchange: null,
			addEventListener: vi.fn(),
			removeEventListener: vi.fn(),
			addListener: vi.fn(),
			removeListener: vi.fn(),
			dispatchEvent: vi.fn(() => false),
		}));
		const view = render(<AssistantMessage message={message()} />);
		view.rerender(<AssistantMessage message={message({ text: "The complete snapshot" })} />);

		expect(screen.getByText("The complete snapshot")).toBeInTheDocument();
		expect(frames.size).toBe(0);
	});

	it("flushes buffered text when streaming completes and restores actions", () => {
		const view = render(<AssistantMessage message={message()} showCopy />);
		view.rerender(<AssistantMessage message={message({ text: "aThe complete answer", streaming: true })} showCopy />);
		runFrame(0);
		view.rerender(
			<AssistantMessage message={message({ text: "aThe complete answer", streaming: false })} showCopy />,
		);

		// The fade spans linger briefly after the stream settles, so match the paragraph.
		expect(document.querySelector("p")?.textContent).toBe("aThe complete answer");
		expect(screen.getByRole("button", { name: "Copy message as markdown" })).toBeInTheDocument();
	});

	it("hides message actions while text is still buffered", () => {
		const view = render(<AssistantMessage message={message()} showCopy />);
		view.rerender(<AssistantMessage message={message({ text: "a buffered answer" })} showCopy />);

		expect(screen.queryByRole("button", { name: "Copy message as markdown" })).not.toBeInTheDocument();
	});

	it("shows the message timestamp on hover", () => {
		render(
			<AssistantMessage
				message={message({ createdAt: new Date().toISOString(), text: "Timestamped answer", streaming: false })}
				showCopy
			/>,
		);

		expect(screen.getByLabelText(/^Sent \d{2}:\d{2}$/)).toBeInTheDocument();
	});

	it("labels yesterday and older messages by date", () => {
		const now = new Date();
		const yesterday = new Date(now.getFullYear(), now.getMonth(), now.getDate() - 1, 12).toISOString();
		const older = new Date(now.getFullYear(), now.getMonth(), now.getDate() - 3, 12).toISOString();
		const view = render(
			<AssistantMessage message={message({ createdAt: yesterday, streaming: false })} showCopy />,
		);

		expect(screen.getByLabelText(/^Sent Yesterday · \d{2}:\d{2}$/)).toBeInTheDocument();
		view.rerender(<AssistantMessage message={message({ createdAt: older, streaming: false })} showCopy />);
		expect(screen.queryByLabelText(/^Sent Yesterday ·/)).toBeNull();
		expect(screen.getByLabelText(/^Sent .+\d{4}$/)).toBeInTheDocument();
	});

	it("survives StrictMode effect cleanup and keeps draining", () => {
		const view = render(
			<StrictMode>
				<AssistantMessage message={message()} />
			</StrictMode>,
		);
		view.rerender(
			<StrictMode>
				<AssistantMessage message={message({ text: "abcdefghij" })} />
			</StrictMode>,
		);
		drain();

		expect(document.querySelector("p")?.textContent).toBe("abcdefghij");
	});
});

describe("ActivityRow", () => {
	const png = "iVBORw0KGgoAAAANSUhEUgAAACAAAAAgCAYAAABzenr0AAAASUlEQVR4nO3OMQ0AMAwDwcApxGIKpnBJGcRDFXn5wePLF1nd086tcb99AAAAwA7YPlA9AAAA/IDtA9UDAADAD9g+UD0AAADsgAePf/6mCWRFdQAAAABJRU5ErkJggg==";
	function tool(content: unknown) {
		return <ActivityRow activity={{
			kind: "activity", id: "read-image", sequence: 1, revision: 1,
			createdAt: "2026-10-26T00:00:00Z", activityKind: "mcp_tool",
			status: "completed", summary: "Read",
			// Historical provider wire shapes are wider than the typed message-content intersection.
			detail: { toolName: "Read", content: content as NonNullable<ConversationActivity["detail"]>["content"] },
		}} />;
	}

	it("expands a direct MCP image without a base64 pre block", () => {
		const { container } = render(tool({ type: "image", data: png, mimeType: "image/png" }));
		fireEvent.click(screen.getByRole("button", { name: /Read/ }));
		expect(screen.getByRole("img")).toBeInTheDocument();
		expect(container.querySelector("pre")).toBeNull();
		expect(container.textContent).not.toContain("iVBOR");
	});

	it("expands mixed MCP content as a chat image and text without binary DOM text", () => {
		const { container } = render(tool({ text: "Synthetic PNG", content: { type: "image", data: png, mimeType: "image/png" } }));
		fireEvent.click(screen.getByRole("button", { name: /Read/ }));
		expect(screen.getByRole("img")).toHaveAttribute("src", `data:image/png;base64,${png}`);
		expect(screen.getByText("Synthetic PNG")).toBeInTheDocument();
		expect(container.textContent).not.toContain("iVBOR");
		expect(container.querySelector("pre")?.textContent).toBe("Synthetic PNG");
	});

	it.each([
		{ type: "image", data: png, mimeType: "image/png" },
		{ type: "image", mimeType: "image/png", bytes: 1800 },
	])("preserves wrapped ACP resources beside detail.content images or placeholders", (image) => {
		const content = [
			{ type: "content", content: image },
			{ type: "content", content: { type: "resource", resource: { uri: "fixture:///report", text: "report body" } } },
			{ type: "content", content: { type: "resource_link", uri: "https://example.test/report", name: "Report link" } },
		];
		const { container } = render(tool(content));
		fireEvent.click(screen.getByRole("button", { name: /Read/ }));
		for (const text of ["fixture:///report", "report body", "https://example.test/report", "Report link"]) {
			expect(container).toHaveTextContent(text);
		}
		if ("data" in image) expect(screen.getByRole("img")).toBeInTheDocument();
		else expect(container).toHaveTextContent("Tool image (image/png, 1800 bytes)");
		expect(container.textContent).not.toContain("iVBOR");
		expect(content[0].content).toEqual(image);
	});

	it("renders a Codex MCP result image without sending its data to JSON text", () => {
		const { container } = render(<ActivityRow activity={{
			kind: "activity", id: "codex-result", sequence: 1, revision: 1,
			createdAt: "2026-10-26T00:00:00Z", activityKind: "mcp_tool", status: "completed",
			summary: "Read", detail: { toolName: "Read", result: { content: [{ type: "image", data: png, mimeType: "image/png" }, { type: "text", text: "Codex caption" }] } },
		}} />);
		fireEvent.click(screen.getByRole("button", { name: /Read/ }));
		expect(screen.getByRole("img")).toBeInTheDocument();
		expect(container.querySelector("pre")).toHaveTextContent("Codex caption");
		expect(container.textContent).not.toContain("iVBOR");
	});

	it("preserves structured content and resource blocks beside MCP result images", () => {
		const result = {
			content: [
				{ type: "image", data: png, mimeType: "image/png" },
				{ type: "resource", resource: { uri: "fixture:///report", mimeType: "text/plain", text: "embedded body" } },
				{ type: "resource_link", uri: "https://example.test/report", name: "Report link" },
			],
			structuredContent: { answer: 42 }, _meta: { trace: "retained" },
		};
		const { container } = render(<ActivityRow activity={{
			kind: "activity", id: "mixed-result", sequence: 1, revision: 1,
			createdAt: "2026-10-26T00:00:00Z", activityKind: "mcp_tool", status: "completed",
			summary: "Read", detail: { toolName: "Read", result },
		}} />);
		fireEvent.click(screen.getByRole("button", { name: /Read/ }));
		expect(screen.getByRole("img")).toBeInTheDocument();
		const json = container.querySelector("pre");
		for (const text of ['"answer": 42', "embedded body", "Report link", "fixture:///report", "retained"]) {
			expect(json).toHaveTextContent(text);
		}
		expect(container.textContent).not.toContain("iVBOR");
		expect(result.content[0]).toHaveProperty("data", png);
	});

	it("keeps non-image structured source text in the original result JSON", () => {
		const result = { content: [{ type: "text", text: "loaded" }], structuredContent: { source: { data: "CSV source text", media_type: "text/plain" }, rows: 2 } };
		const { container } = render(<ActivityRow activity={{
			kind: "activity", id: "text-source-result", sequence: 1, revision: 1,
			createdAt: "2026-10-26T00:00:00Z", activityKind: "mcp_tool", status: "completed",
			summary: "Read", detail: { toolName: "Read", result },
		}} />);
		fireEvent.click(screen.getByRole("button", { name: /Read/ }));
		expect(screen.queryByRole("img")).toBeNull();
		expect(container.querySelector("pre")).toHaveTextContent("CSV source text");
		expect(container.querySelector("pre")).toHaveTextContent("text/plain");
		expect(container.textContent).not.toContain("Tool image");
	});

	it("keeps text-only MCP output in its existing code block", () => {
		const { container } = render(tool({ type: "text", text: "plain result" }));
		fireEvent.click(screen.getByRole("button", { name: /Read/ }));
		expect(container.querySelector("pre code")).toHaveTextContent("plain result");
		expect(screen.queryByRole("img")).toBeNull();
	});

	it("retains unified diff rows alongside images", () => {
		const { container } = render(tool([{ type: "text", text: "-old\n+new" }, { type: "image", data: png, mimeType: "image/png" }]));
		fireEvent.click(screen.getByRole("button", { name: /Read/ }));
		expect(container.querySelectorAll("[data-tool-diff-row]")).toHaveLength(2);
		expect(screen.getByRole("img")).toBeInTheDocument();
	});

	it("renders inline code in provider activity titles without literal backticks", () => {
		const path = "/Users/sachin/.ao/worktrees/murdock/docs/system-design.html";
		const { container } = render(
			<ActivityRow activity={{
				kind: "activity", id: "edit-title", sequence: 1, revision: 0,
				createdAt: "2026-08-23T00:00:00Z", activityKind: "file_change",
				status: "completed", summary: `Edit \`${path}\``,
			}} />,
		);
		expect(screen.getByRole("button")).toHaveTextContent(`Edit ${path}`);
		expect(container.querySelector("code")).toHaveTextContent(path);
		expect(screen.getByRole("button").textContent).not.toContain("`");
	});

	it("shows a finished project setup with its duration and expandable output", () => {
		render(
			<ActivityRow activity={{
				kind: "activity", id: "startup", sequence: 1, revision: 1, createdAt: "2026-10-13T00:00:00Z",
				activityKind: "system", status: "completed", summary: "Setup passed",
				detail: { event: "workspace.setup", setupStatus: "succeeded", exitCode: 0, durationMs: 12_400, output: "compose up\nready" },
			}} />,
		);
		expect(screen.getByText("Setup passed in 12s")).toBeInTheDocument();
		expect(screen.queryByText(/compose up/)).toBeNull();
		act(() => screen.getByRole("button", { name: "Show output" }).click());
		expect(screen.getByRole("button", { name: "Hide output" })).toHaveAttribute("aria-expanded", "true");
		expect(screen.getByText(/compose up/)).toBeInTheDocument();
	});

	it("draws a script run as the step log panel", () => {
		const run = groupScriptRuns([{
			sessionId: "s1", sequence: 1, type: "workspace.script.step", createdAt: "2026-10-13T00:00:00Z",
			payload: { phase: "setup", runId: "r", step: 1, total: 1, command: "make", repository: "", status: "failed", exitCode: 2, durationMs: 1000, output: "boom" },
		}])[0];
		render(
			<ActivityRow activity={{
				kind: "activity", id: "run", sequence: 1, revision: 1, createdAt: "2026-10-13T00:00:00Z",
				activityKind: "system", status: "failed", summary: "Setup failed",
				detail: { event: "workspace.script.run", scriptRun: run },
			}} />,
		);
		expect(screen.getByTestId("script-run-panel")).toHaveAttribute("data-run-status", "failed");
		expect(screen.getByText("Failed at step 1")).toBeInTheDocument();
		expect(screen.getByText("boom")).toBeInTheDocument();
		expect(screen.queryByTestId("project-setup-row")).toBeNull();
	});

	it("shows the failed setup step with its exit code", () => {
		render(
			<ActivityRow activity={{
				kind: "activity", id: "startup", sequence: 1, revision: 1, createdAt: "2026-10-13T00:00:00Z",
				activityKind: "system", status: "failed", summary: "Setup failed",
				detail: { event: "workspace.setup", setupStatus: "failed", setupStep: 2, exitCode: 2, durationMs: 65_000 },
			}} />,
		);
		expect(screen.getByText("Setup step 2 failed (exit 2)")).toBeInTheDocument();
		expect(screen.queryByRole("button", { name: "Show output" })).toBeNull();
	});

	it("shows why a setup step failed and, expanded, who ran it and where its full log is", () => {
		render(
			<ActivityRow activity={{
				kind: "activity", id: "startup", sequence: 1, revision: 1, createdAt: "2026-10-13T00:00:00Z",
				activityKind: "system", status: "failed", summary: "Setup failed",
				detail: {
					event: "workspace.setup", setupStatus: "failed", setupStep: 1, exitCode: 1, output: "permission denied: docker.sock",
					setupHint: "This step ran as ao-worker, which cannot use Docker.",
					setupFailure: {
						command: "docker compose up -d", user: "ao-worker", groups: ["ao-worker"],
						dir: "/home/coder/app", logPath: "/x/setup-logs/step-1.log",
					},
				},
			}} />,
		);
		expect(screen.getByText("This step ran as ao-worker, which cannot use Docker.")).toBeInTheDocument();
		act(() => screen.getByRole("button", { name: "Show output" }).click());
		const output = screen.getByText(/Ran as ao-worker \(groups: ao-worker\)/);
		expect(output).toHaveTextContent("Step 1: docker compose up -d");
		expect(output).toHaveTextContent("Ran in /home/coder/app");
		expect(output).toHaveTextContent("Full output: /x/setup-logs/step-1.log");
		expect(output).toHaveTextContent("permission denied: docker.sock");
	});

	it("does not present a recovered historical activity as failed", () => {
		render(
			<ActivityRow
				activity={{
					kind: "activity",
					id: "activity-1",
					sequence: 1,
					revision: 0,
					createdAt: "2026-08-23T00:00:00Z",
					activityKind: "command",
					status: "recovered",
					summary: "Run tests",
				}}
			/>,
		);
		expect(screen.getByText("outcome unknown")).toBeInTheDocument();
		expect(screen.queryByText("failed")).not.toBeInTheDocument();
	});
});

describe("HumanMessage", () => {
	function human(overrides: Partial<ConversationMessage> = {}): ConversationMessage {
		return message({ id: "human-1", role: "user", origin: "human", text: "hi", streaming: false, ...overrides });
	}

	// Light theme cannot separate a sent bubble from the canvas by fill alone, so it
	// paints an enclosure keyed off this attribute's absence. Losing it makes the
	// message read as unenclosed text again.
	it("marks a queued bubble and leaves a sent one unmarked", () => {
		const { container, rerender } = render(<HumanMessage message={human()} sessionId="s1" />);
		const sent = container.querySelector(".cursor-chat-human-message");
		expect(sent).not.toBeNull();
		expect(sent).not.toHaveAttribute("data-queued");

		rerender(<HumanMessage message={human()} sessionId="s1" queued />);
		expect(container.querySelector(".cursor-chat-human-message")).toHaveAttribute("data-queued");
	});
});

describe("ProviderSwitchMarker", () => {
	it("names a remote session's models from its host's catalog", () => {
		const client = new QueryClient({ defaultOptions: { queries: { staleTime: Infinity, retry: false } } });
		client.setQueryData(agentModelsQueryKey("codex", "", "host-1"), { models: [{ id: "gpt-6", label: "GPT Six" }] });
		client.setQueryData(agentModelsQueryKey("codex", ""), { models: [{ id: "gpt-6", label: "Local name" }] });
		render(
			<QueryClientProvider client={client}>
				<ChatHostId.Provider value="host-1">
					<ProviderSwitchMarker activity={{
						kind: "activity", id: "switch", sequence: 1, revision: 1, activityKind: "system", status: "completed",
						summary: "Switched provider", createdAt: "2026-01-01T00:00:00Z",
						detail: { event: "provider_switch", sourceHarness: "claude-code", targetHarness: "codex", targetModel: "gpt-6" },
					}} />
				</ChatHostId.Provider>
			</QueryClientProvider>,
		);
		expect(screen.getByText("GPT Six")).toBeInTheDocument();
	});
});
