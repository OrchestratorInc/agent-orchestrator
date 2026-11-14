import { act, fireEvent, render as rtlRender, screen, waitFor } from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { StrictMode, type ReactElement, type ReactNode } from "react";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import {
	ActivityRow,
	AdaptiveStreamPacingContext,
	AssistantMessage,
	ChatHostId,
	HumanMessage,
	LiveResponseStatus,
	liveTaskLabel,
	ProviderSwitchMarker,
	TurnOutcome,
} from "./ChatTimelineItems";
import claudeCodeLogo from "../../assets/agents/claude-code.svg";
import codexLogo from "../../assets/agents/codex.svg";
import { agentModelsQueryKey } from "../../hooks/useAgentModelsQuery";
import { groupScriptRuns } from "../../lib/cloud-script-runs";
import type { ConversationMessage } from "../../types/conversation";
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

function Cloud({ children }: { children: ReactNode }) {
	return <AdaptiveStreamPacingContext.Provider value>{children}</AdaptiveStreamPacingContext.Provider>;
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

	it("shows missing-key recovery while keeping retry and technical details", () => {
		const onRetry = vi.fn();
		const onConfigure = vi.fn();
		render(<TurnOutcome state="failed" failureCode="provider_key_missing" error="ACP internal detail" retry={{ onRetry }} onConfigure={onConfigure} />);
		expect(screen.getByText("DeepSeek needs an API key for this model route")).toBeInTheDocument();
		fireEvent.click(screen.getByRole("button", { name: "Set up DeepSeek" }));
		expect(onConfigure).toHaveBeenCalledOnce();
		fireEvent.click(screen.getByRole("button", { name: "Retry this turn" }));
		expect(onRetry).toHaveBeenCalledOnce();
		expect(screen.getByText("ACP internal detail")).toBeInTheDocument();
		expect(screen.getByText("ACP internal detail").closest("details")).toBeInTheDocument();
	});

	it("ends a refused login on one quiet line under the message, and never the CLI's advice", () => {
		render(<TurnOutcome state="failed" failureCode="auth_required" error="Not logged in · Please run /login" retry={{ onRetry: vi.fn() }}
			onRollback={vi.fn()} login={{ status: "queued", align: "end" }} />);

		expect(screen.getByTestId("login-status")).toHaveTextContent("Queued");
		expect(screen.queryByText("The agent ran into a problem")).not.toBeInTheDocument();
		expect(screen.queryByText(/Please run \/login/)).not.toBeInTheDocument();
		// The login is offered on the chat box, so the turn has no action of its own.
		expect(screen.queryAllByRole("button")).toHaveLength(0);
		expect(document.querySelector("[data-turn-actions]")).toBeNull();
	});

	it("ends a refused turn that did work on its own last line, keeping copy and undo", () => {
		const onRollback = vi.fn();
		render(<TurnOutcome state="failed" failureCode="auth_required" error="Not logged in · Please run /login" login={{ status: "resend", align: "start" }}
			retry={{ onRetry: vi.fn() }} copyText="Edited the file" onRollback={onRollback} />);
		expect(screen.getByTestId("login-status").closest("[data-turn-outcome]")).toBeInTheDocument();
		expect(screen.getByText("Stopped · send it again once you’re in")).toBeInTheDocument();
		// Its answer hands copy and undo down to this row, as any stopped turn's does.
		expect(screen.getByRole("button", { name: "Copy message as markdown" })).toBeInTheDocument();
		fireEvent.click(screen.getByRole("button", { name: "Roll back to here" }));
		expect(onRollback).toHaveBeenCalledOnce();
		// Sending it again is the band's, so the row has no Retry.
		expect(screen.queryByTestId("retry-turn")).toBeNull();
	});

	it("keeps Retry on an earlier refusal the band no longer offers", () => {
		const onRetry = vi.fn();
		const { unmount } = render(<TurnOutcome state="failed" failureCode="auth_required" login={{ status: "stopped", align: "start", ownRetry: true }}
			retry={{ onRetry }} copyText="Edited the file" onRollback={vi.fn()} />);
		// A turn that did work: Retry leads its shared row.
		fireEvent.click(screen.getByTestId("retry-turn"));
		expect(onRetry).toHaveBeenCalledOnce();
		unmount();

		// One that did nothing else: on the line, which sits in the message's row.
		render(<TurnOutcome state="failed" failureCode="auth_required" login={{ status: "stopped", align: "end", ownRetry: true }}
			retry={{ onRetry, error: "Stop the current turn before retrying this one" }} />);
		expect(screen.getByTestId("login-status")).toHaveTextContent(/^Stopped$/);
		fireEvent.click(screen.getByTestId("retry-turn"));
		expect(onRetry).toHaveBeenCalledTimes(2);
		expect(screen.getByRole("alert")).toHaveTextContent("Stop the current turn before retrying this one");
	});

	it("shows a refused login with no way to log in as the plain failure", () => {
		render(<TurnOutcome state="failed" failureCode="auth_required" error="Not logged in · Please run /login" />);
		expect(screen.getByText("The agent ran into a problem")).toBeInTheDocument();
		expect(screen.queryByRole("button", { name: "Log in" })).not.toBeInTheDocument();
	});

	it("leaves generic ACP failures without configuration guidance", () => {
		render(<TurnOutcome state="failed" error="ACP internal detail" />);
		expect(screen.getByText("ACP internal detail")).toBeInTheDocument();
		expect(screen.queryByRole("button", { name: "Set up DeepSeek" })).not.toBeInTheDocument();
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

	it("smooths received bursts up to 4,000 characters", () => {
		const view = render(<AssistantMessage message={message()} />);
		const text = "a".padEnd(4000, "x");
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

	it("coalesces long snapshots without extra segmentation after flushing pending smoothing", () => {
		vi.useFakeTimers({ toFake: ["setTimeout", "clearTimeout"] });
		const segment = vi.spyOn(Intl.Segmenter.prototype, "segment");
		const view = render(<AssistantMessage message={message()} />);
		const prefix = "a".padEnd(4000, "x");
		view.rerender(<AssistantMessage message={message({ text: prefix })} />);
		runFrame(0);
		expect(frames.size).toBeGreaterThan(0);

		const text = prefix + "👨‍👩‍👧‍👦e\u0301";
		view.rerender(<AssistantMessage message={message({ text })} />);
		expect(document.querySelector("p")?.textContent).toBe(text);
		expect(frames.size).toBe(0);
		// Markdown still segments emoji for typography; smoothing must not add
		// another pass over the entire long snapshot.
		expect(segment.mock.calls.filter(([input]) => input === text).length).toBeLessThanOrEqual(1);

		const latest = text + " latest";
		view.rerender(<AssistantMessage message={message({ text: latest })} />);
		view.rerender(<AssistantMessage message={message({ text: latest + " snapshot" })} />);
		act(() => vi.advanceTimersByTime(99));
		expect(document.querySelector("p")?.textContent).toBe(text);
		act(() => vi.advanceTimersByTime(1));
		expect(document.querySelector("p")?.textContent).toBe(latest + " snapshot");
		expect(frames.size).toBe(0);
		expect(segment.mock.calls.filter(([input]) => input === latest + " snapshot").length).toBeLessThanOrEqual(1);

		const final = latest + " snapshot completed";
		view.rerender(<AssistantMessage message={message({ text: final })} />);
		expect(document.querySelector("p")?.textContent).not.toBe(final);
		view.rerender(<AssistantMessage message={message({ text: final, streaming: false })} />);
		expect(document.querySelector("p")?.textContent).toBe(final);
		act(() => vi.advanceTimersByTime(200));
		expect(document.querySelector("p")?.textContent).toBe(final);
	});

	it("cancels a queued long snapshot when the message is replaced", () => {
		vi.useFakeTimers({ toFake: ["setTimeout", "clearTimeout"] });
		const text = "x".repeat(4001);
		const view = render(<AssistantMessage message={message({ text })} />);
		view.rerender(<AssistantMessage message={message({ text: text + " pending" })} />);
		view.rerender(<AssistantMessage message={message({ id: "assistant-2", text: "New message" })} />);
		act(() => vi.advanceTimersByTime(200));
		expect(document.querySelector("p")?.textContent).toBe("New message");
	});

	it("cancels a queued long snapshot on unmount", () => {
		vi.useFakeTimers({ toFake: ["setTimeout", "clearTimeout"] });
		const text = "x".repeat(4001);
		const view = render(<AssistantMessage message={message({ text })} />);
		view.rerender(<AssistantMessage message={message({ text: text + " pending" })} />);
		expect(vi.getTimerCount()).toBeGreaterThan(0);
		view.unmount();
		expect(vi.getTimerCount()).toBe(0);
	});

	it("resumes smoothing after a long message is corrected to a short snapshot", () => {
		const view = render(<AssistantMessage message={message({ text: "x".repeat(4001) })} />);
		view.rerender(<AssistantMessage message={message({ text: "Corrected" })} />);
		expect(document.querySelector("p")?.textContent).toBe("Corrected");

		view.rerender(<AssistantMessage message={message({ text: "Corrected text" })} />);
		expect(frames.size).toBeGreaterThan(0);
		drain();
		expect(document.querySelector("p")?.textContent).toBe("Corrected text");
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

	it("in Cloud chat, plays out a finished reply's buffered text quickly", () => {
		const view = render(<Cloud><AssistantMessage message={message()} showCopy /></Cloud>);
		view.rerender(<Cloud><AssistantMessage message={message({ text: "aThe complete answer", streaming: true })} showCopy /></Cloud>);
		runFrame(0);
		view.rerender(<Cloud><AssistantMessage message={message({ text: "aThe complete answer", streaming: false })} showCopy /></Cloud>);
		// The remainder plays out over a few frames rather than landing at once.
		expect(document.querySelector("p")?.textContent).not.toBe("aThe complete answer");
		drain(32);
		expect(document.querySelector("p")?.textContent).toBe("aThe complete answer");
		expect(screen.getByRole("button", { name: "Copy message as markdown" })).toBeInTheDocument();
	});

	it("in Cloud chat, shows a finished reply whole when its unrevealed remainder is too long to play out", () => {
		const view = render(<Cloud><AssistantMessage message={message()} /></Cloud>);
		// Long enough to leave more than a playout's worth after one frame, short
		// enough to be smoothed at all.
		const text = "a".padEnd(4_000, "x");
		view.rerender(<Cloud><AssistantMessage message={message({ text, streaming: true })} /></Cloud>);
		runFrame(0);
		expect(document.querySelector("p")?.textContent).not.toBe(text);
		view.rerender(<Cloud><AssistantMessage message={message({ text, streaming: false })} /></Cloud>);
		expect(document.querySelector("p")?.textContent).toBe(text);
		expect(frames.size).toBe(0);
	});

	it("in Cloud chat, shows a finished long reply whole instead of waiting on its queued snapshot", () => {
		vi.useFakeTimers({ toFake: ["setTimeout", "clearTimeout"] });
		const text = "x".repeat(4001);
		const view = render(<Cloud><AssistantMessage message={message({ text })} /></Cloud>);
		view.rerender(<Cloud><AssistantMessage message={message({ text: text + " pending" })} /></Cloud>);
		expect(document.querySelector("p")?.textContent).toBe(text);
		expect(vi.getTimerCount()).toBeGreaterThan(0);
		view.rerender(<Cloud><AssistantMessage message={message({ text: text + " pending", streaming: false })} /></Cloud>);
		expect(document.querySelector("p")?.textContent).toBe(text + " pending");
		expect(frames.size).toBe(0);
		act(() => vi.advanceTimersByTime(200));
		expect(document.querySelector("p")?.textContent).toBe(text + " pending");
		expect(frames.size).toBe(0);
	});

	it("in Cloud chat, spreads bursts that arrive far apart over most of the gap between them", () => {
		const view = render(<Cloud><AssistantMessage message={message()} /></Cloud>);
		view.rerender(<Cloud><AssistantMessage message={message({ text: "a".padEnd(200, "x") })} /></Cloud>);
		drain(0);
		frameTime = 3_000;
		const text = "a".padEnd(400, "x");
		view.rerender(<Cloud><AssistantMessage message={message({ text })} /></Cloud>);
		runFrame(3_000);
		runFrame(3_032);
		// A 140ms settle would reveal about a fifth of the burst in one frame; a
		// second-apart stream reveals only a few percent.
		const shown = document.querySelector("p")?.textContent?.length ?? 0;
		expect(shown - 200).toBeLessThan(20);
		drain(3_032);
		expect(document.querySelector("p")?.textContent).toBe(text);
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

	describe("attachments", () => {
		const attached = (paths: string[], body = "") =>
			`${body ? `${body}\n\n` : ""}Attached files (read these files in the workspace):\n${paths.map((path) => `- ${path}`).join("\n")}`;
		const apiBaseUrl = "http://127.0.0.1:3002";

		// jsdom has no layout: every element reports a height far over the fold.
		beforeEach(() => {
			vi.spyOn(HTMLElement.prototype, "offsetHeight", "get").mockReturnValue(900);
		});

		// A normal screenshot is taller than the fold, so inside it every image was cut off.
		it("keeps them above the bubble and outside a long message's fold", () => {
			const { container } = render(
				<HumanMessage message={human({ text: attached([".ao/attachments/attachment-ab12.png"], "a long folded message") })} sessionId="s1" apiBaseUrl={apiBaseUrl} />,
			);
			const toggle = screen.getByRole("button", { name: "Show more" });
			const fold = document.getElementById(toggle.getAttribute("aria-controls")!)!;
			const list = screen.getByRole("list", { name: "Attached files" });
			const bubble = container.querySelector(".cursor-chat-human-message")!;
			expect(fold).toContainElement(screen.getByText("a long folded message"));
			expect(fold).not.toContainElement(list);
			expect(bubble).not.toContainElement(list);
			expect(list.compareDocumentPosition(bubble) & Node.DOCUMENT_POSITION_FOLLOWING).toBeTruthy();
		});

		it("draws no bubble or fold for a message that is only attachments, and keeps its actions", () => {
			const { container } = render(
				<HumanMessage message={human({ text: attached([".ao/attachments/attachment-only.png"]) })} sessionId="s1" apiBaseUrl={apiBaseUrl} />,
			);
			expect(screen.getByRole("img", { name: "Image 1" })).toBeInTheDocument();
			expect(container.querySelector(".cursor-chat-human-message")).toBeNull();
			expect(screen.queryByRole("button", { name: "Show more" })).not.toBeInTheDocument();
			expect(screen.getByRole("button", { name: "Copy user message" })).toBeInTheDocument();
			expect(screen.getByLabelText(/^Sent /)).toBeInTheDocument();
		});

		it("shows every image and file at the one tile height", () => {
			render(
				<HumanMessage message={human({ text: attached([".ao/attachments/attachment-a.png", ".ao/attachments/attachment-b.pdf"]) })} sessionId="s1" apiBaseUrl={apiBaseUrl} />,
			);
			expect(screen.getByRole("img", { name: "Image 1" })).toHaveClass("h-16");
			expect(screen.getByText("attachment-b.pdf").closest("li")).toHaveClass("h-16");
		});
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

	const switched = (sourceHarness: string, targetHarness: string, extra: Record<string, string> = {}) => {
		// A seeded catalog keeps the recorded-model case off the network.
		const client = new QueryClient({ defaultOptions: { queries: { staleTime: Infinity, retry: false } } });
		client.setQueryData(agentModelsQueryKey("codex", ""), { models: [{ id: "gpt-6", label: "GPT Six" }] });
		return (
			<QueryClientProvider client={client}>
				<ProviderSwitchMarker activity={{
					kind: "activity", id: "switch", sequence: 1, revision: 1, activityKind: "system", status: "completed",
					summary: "Switched provider", createdAt: "2026-01-01T00:00:00Z",
					detail: { event: "provider_switch", sourceHarness, targetHarness, ...extra },
				}} />
			</QueryClientProvider>
		);
	};
	// The decorative mark drawn just before a side's name.
	const markBefore = (name: string) => screen.getByText(name).previousElementSibling;

	it("draws each side's agent mark before its name", () => {
		const { container } = render(switched("claude-code", "codex"));
		const from = markBefore("Claude Code");
		const to = markBefore("Codex");
		expect(from).toHaveAttribute("aria-hidden", "true");
		expect(to).toHaveAttribute("aria-hidden", "true");
		expect(from?.tagName).toBe("IMG");
		expect(from).toHaveAttribute("src", claudeCodeLogo);
		expect(to).toHaveAttribute("src", codexLogo);
		// Marks are decorative: the separator still names the switch in words.
		expect(screen.getByRole("separator", { name: "Switched from Claude Code to Codex" })).toBeInTheDocument();
		expect(container.querySelectorAll('[role="separator"] [aria-hidden="true"].size-icon-base')).toHaveLength(2);
	});

	it("keeps the mark beside a recorded model's name", () => {
		const { container } = render(switched("claude-code", "codex", { targetModel: "gpt-6", targetEffort: "high" }));
		const marks = container.querySelectorAll('[role="separator"] [aria-hidden="true"].size-icon-base');
		expect(marks).toHaveLength(2);
		expect(marks[1]).toHaveAttribute("src", codexLogo);
		expect(marks[1]?.nextElementSibling).toHaveTextContent("GPT Six");
	});

	it("gives an agent with no logo the app's lettered mark", () => {
		render(switched("claude-code", "zeta-agent"));
		const mark = markBefore("zeta-agent");
		expect(mark).toHaveAttribute("aria-hidden", "true");
		expect(mark?.tagName).toBe("SPAN");
		expect(mark).toHaveTextContent("Z");
	});
});

describe("MCP tool rows", () => {
	const base = { kind: "activity", sequence: 1, revision: 1, createdAt: "2026-11-08T00:00:00Z", activityKind: "mcp_tool" } as const;
	const content = (text: string) => [{ type: "content", content: { type: "text", text } }];

	it("names a Claude Code page preview in plain words and opens onto its console errors", () => {
		const report = JSON.stringify({ width: 720, contentHeight: 87, consoleMessages: [{ level: "error", text: "test error" }] });
		render(
			<ActivityRow activity={{
				...base, id: "preview", status: "completed", summary: "mcp__ao__html_preview",
				detail: { providerToolName: "mcp__ao__html_preview", input: { html: "<p>x</p>" }, content: content(report) },
			}} />,
		);
		const row = screen.getByRole("button", { name: /Previewed the page/ });
		expect(row).toHaveTextContent("1 console error");
		expect(screen.queryByText(/mcp__ao__/)).toBeNull();
		fireEvent.click(row);
		expect(screen.getByText("test error")).toBeInTheDocument();
		expect(screen.queryByText(/720/)).toBeNull();
	});

	it("draws nothing for a shown page and keeps a failed one", () => {
		const { container } = render(
			<ActivityRow activity={{
				...base, id: "render", status: "completed", summary: "Called ao/html_render",
				detail: { server: "ao", toolName: "html_render", result: { content: [{ type: "text", text: "Shown above your reply (render r1)." }] } },
			}} />,
		);
		expect(container.textContent).toBe("");

		render(
			<ActivityRow activity={{
				...base, id: "render-failed", status: "completed", summary: "Called ao/html_render",
				detail: {
					server: "ao", toolName: "html_render",
					result: { content: [{ type: "text", text: "invalid render: the page is empty (RENDER_INVALID) [request h/x-1]" }] },
				},
			}} />,
		);
		fireEvent.click(screen.getByRole("button", { name: "Couldn't show the page" }));
		expect(screen.getByText("Invalid render: the page is empty")).toBeInTheDocument();
	});

	it("titles another server's tool as tool · server and shows Claude Code's arguments", () => {
		render(
			<ActivityRow activity={{
				...base, id: "guide", status: "completed", summary: "mcp__claude_ai_Claude_Docs__guide",
				detail: { providerToolName: "mcp__claude_ai_Claude_Docs__guide", input: { items: ["topic.index"] }, content: content("index") },
			}} />,
		);
		const row = screen.getByRole("button", { name: /guide/ });
		expect(row).toHaveTextContent("guide· claude_ai_Claude_Docs");
		fireEvent.click(row);
		expect(screen.getByText("Arguments")).toBeInTheDocument();
		expect(screen.getByText(/topic\.index/)).toBeInTheDocument();
	});

	it("says what a running AO call is doing on the live line", () => {
		expect(liveTaskLabel({ ...base, id: "p", status: "running", summary: "mcp__ao__html_preview", detail: { providerToolName: "mcp__ao__html_preview" } })).toBe("Previewing the page");
		expect(liveTaskLabel({ ...base, id: "g", status: "running", summary: "Called linear/create_issue" })).toBe("create_issue");
	});
});
