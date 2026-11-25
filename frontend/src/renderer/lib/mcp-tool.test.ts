import { describe, expect, it } from "vitest";
import type { ConversationActivity } from "../types/conversation";
import { aoToolView, isShownAORender, mcpResultTexts, mcpToolArguments, mcpToolName } from "./mcp-tool";

// The shapes below are copied from rows the two harnesses recorded in a dev app.

function activity(overrides: Partial<ConversationActivity> = {}): ConversationActivity {
	return {
		kind: "activity",
		id: "a1",
		sequence: 1,
		revision: 1,
		createdAt: "2026-11-08T00:00:00Z",
		activityKind: "mcp_tool",
		status: "completed",
		summary: "",
		...overrides,
	};
}

/** Claude Code over ACP: internal name as the title, arguments under `input`. */
function claude(tool: string, input: unknown, content: string[], status: ConversationActivity["status"] = "completed") {
	return activity({
		status,
		summary: `mcp__ao__${tool}`,
		detail: {
			protocol: "acp",
			toolKind: "other",
			providerToolName: `mcp__ao__${tool}`,
			input,
			content: content.map((text) => ({ type: "content", content: { type: "text", text } })),
		} as ConversationActivity["detail"],
	});
}

/** Codex: server and tool sent apart, the MCP result under `result`. A tool error still arrives completed. */
function codex(tool: string, args: unknown, texts: string[]) {
	return activity({
		summary: `Called ao/${tool}`,
		detail: {
			server: "ao",
			toolName: tool,
			arguments: args,
			result: { content: texts.map((text) => ({ type: "text", text })), structuredContent: null, _meta: null },
		},
	});
}

const REPORT = JSON.stringify({
	width: 720,
	contentHeight: 87,
	consoleMessages: [
		{ level: "error", text: "test error" },
		{ level: "warning", text: "Electron Security Warning (Insecure Content-Security-Policy)" },
	],
	note: "The check ran without network access, as your sandbox has none. Remote resources did not load.",
});
const CLEAN_REPORT = JSON.stringify({ width: 720, contentHeight: 87, consoleMessages: [] });

describe("mcpToolName", () => {
	it("reads Claude Code's internal name and Codex's summary the same way", () => {
		expect(mcpToolName(claude("html_preview", {}, []))).toEqual({ server: "ao", tool: "html_preview" });
		expect(mcpToolName(codex("html_preview", {}, []))).toEqual({ server: "ao", tool: "html_preview" });
		expect(mcpToolName(activity({ summary: "Called linear/create_issue" }))).toEqual({
			server: "linear",
			tool: "create_issue",
		});
		expect(mcpToolName(activity({ summary: "mcp__claude_ai_Claude_Docs__guide" }))).toEqual({
			server: "claude_ai_Claude_Docs",
			tool: "guide",
		});
	});

	it("leaves a built-in tool with its own name and no server", () => {
		expect(mcpToolName(activity({ summary: "ToolSearch", detail: { providerToolName: "ToolSearch" } }))).toEqual({
			tool: "ToolSearch",
			server: undefined,
		});
	});
});

describe("mcpToolArguments", () => {
	it("reads Codex's arguments and Claude Code's input", () => {
		expect(mcpToolArguments(codex("html_render", { title: "A" }, []))).toEqual({ title: "A" });
		expect(mcpToolArguments(claude("html_render", { title: "B" }, []))).toEqual({ title: "B" });
	});
});

describe("mcpResultTexts", () => {
	it("drops Claude Code's saved-image line and unfences its error text", () => {
		const call = claude("html_preview", {}, [
			"[Image: source: /Users/x/.claude/projects/p/tool-results/mcp-ao-blob-1.png]",
			"```\ninvalid render: the page is empty (RENDER_INVALID)\n```",
		]);
		expect(mcpResultTexts(call)).toEqual(["invalid render: the page is empty (RENDER_INVALID)"]);
	});
});

describe("aoToolView: pages", () => {
	it("names a clean preview and nothing else", () => {
		const view = aoToolView(claude("html_preview", { html: "<p>x</p>" }, [CLEAN_REPORT]));
		expect(view).toMatchObject({ label: "Previewed the page", state: "completed" });
		expect(view?.note).toBeUndefined();
		expect(view?.lines).toBeUndefined();
	});

	it("counts console errors, lists them, and leaves out sizes, warnings and the note to the agent", () => {
		const view = aoToolView(codex("html_preview", { width: 720 }, [REPORT]));
		expect(view).toMatchObject({ label: "Previewed the page", note: "1 console error" });
		expect(view?.lines).toEqual([{ tone: "error", text: "test error" }]);
		expect(JSON.stringify(view)).not.toMatch(/720|87|sandbox|Electron/);
	});

	it("treats a Codex preview that came back without its report as failed", () => {
		const view = aoToolView(codex("html_preview", {}, ["UnknownVizError (RENDER_CHECK_FAILED) [request host/abc-000927]"]));
		expect(view).toMatchObject({ label: "Couldn't preview the page", state: "failed", error: "UnknownVizError" });
	});

	it("hides a shown page and keeps a failed one, in both harnesses", () => {
		const shown = "Shown above your reply (render 75b1). Do not describe the page; add only what it does not say.";
		expect(isShownAORender(claude("html_render", {}, [shown]))).toBe(true);
		expect(isShownAORender(codex("html_render", {}, [shown]))).toBe(true);

		const claudeFailed = claude("html_render", { html: "" }, ["```\ninvalid render: the page is empty (RENDER_INVALID) [request h/x-1]\n```"], "failed");
		const codexFailed = codex("html_render", { html: "" }, ["invalid render: the page is empty (RENDER_INVALID) [request h/x-2]"]);
		for (const call of [claudeFailed, codexFailed]) {
			expect(isShownAORender(call)).toBe(false);
			expect(aoToolView(call)).toMatchObject({
				label: "Couldn't show the page",
				state: "failed",
				error: "Invalid render: the page is empty",
			});
		}
	});

	it("says what is happening while a page call runs", () => {
		expect(aoToolView(claude("html_preview", {}, [], "running"))?.label).toBe("Previewing the page");
		const rendering = claude("html_render", {}, [], "running");
		expect(aoToolView(rendering)?.label).toBe("Showing the page");
		expect(isShownAORender(rendering)).toBe(false);
	});
});

describe("aoToolView: computer use", () => {
	it.each([
		["launch_app", { name: "Calculator" }, "Opened Calculator"],
		["launch_app", { name: "/Applications/Xcode.app" }, "Opened Xcode"],
		["launch_app", { bundle_id: "com.apple.calculator" }, "Opened com.apple.calculator"],
		["list_windows", { app: "Calculator" }, "Looked at Calculator windows"],
		["list_windows", {}, "Looked at open windows"],
		["get_window_state", { pid: 1, window_id: 2 }, "Looked at a window"],
		["bring_to_front", { pid: 1 }, "Brought an app to the front"],
		["click", { element_token: "s00000001:5" }, "Clicked"],
		["click", { x: 1, y: 2, button: "right" }, "Right-clicked"],
		["click", { x: 1, y: 2, count: 2 }, "Double-clicked"],
		["click", { x: 1, y: 2, modifier: ["cmd"] }, "⌘-clicked"],
		["type_text", { text: "12" }, 'Typed "12"'],
		["press_key", { key: "return" }, "Pressed Return"],
		["press_key", { key: "s", modifiers: ["cmd", "shift"] }, "Pressed ⌘⇧S"],
	])("%s %j reads %s", (tool, input, label) => {
		expect(aoToolView(claude(tool, input, ["✅ Performed AXPress on [5] AXButton \"\".\n\nwindow_id=1634 pid=92562"]))?.label).toBe(label);
	});

	it("never opens onto the agent's accessibility tree", () => {
		const view = aoToolView(claude("get_window_state", { pid: 1, window_id: 2 }, ["window_id=1634 pid=92562 size=460x816\n- [0] AXWindow"]));
		expect(view?.text).toBeUndefined();
		expect(view?.lines).toBeUndefined();
	});

	it("shows the whole of a typed text only when the label had to cut it", () => {
		const long = "The quick brown fox jumps over the lazy dog, twice over";
		const view = aoToolView(claude("type_text", { text: long }, []));
		expect(view?.label).toBe('Typed "The quick brown fox jumps over the lazy…"');
		expect(view?.text).toBe(long);
		expect(aoToolView(claude("type_text", { text: "12" }, []))?.text).toBeUndefined();
	});

	it("keeps a tool's name as written when stripping the code and request from its error", () => {
		const call = claude(
			"type_text",
			{ pid: 1, text: "12" },
			["type_text needs the element_token of the field to type into. (COMPUTER_USE_TEXT_TARGET_REQUIRED) [request h/2z80-001806]"],
			"failed",
		);
		expect(aoToolView(call)).toMatchObject({
			label: "Couldn't type",
			error: "type_text needs the element_token of the field to type into.",
		});
	});

	it("lists run_steps in plain words and says where a batch stopped", () => {
		const steps = [
			{ click: { name: "All Clear", role: "button" } },
			{ tool: "press_key", args: { key: "s", modifiers: ["cmd"] } },
			{ type_text: { text: "hello" } },
		];
		const done = aoToolView(claude("run_steps", { app: "Calculator", steps }, ["run_steps: 3/3 steps ok\n1. click ok"]));
		expect(done).toMatchObject({ label: "Took 3 actions in Calculator", state: "completed" });
		expect(done?.lines?.map((line) => line.text)).toEqual(['1. Clicked "All Clear"', "2. Pressed ⌘S", '3. Typed "hello"']);

		const stopped = aoToolView(claude("run_steps", { app: "Calculator", steps }, ["run_steps: 1/3 steps ok\n1. click ok\n2. press_key failed"]));
		expect(stopped).toMatchObject({ label: "Stopped in Calculator at step 2 of 3", state: "failed" });
		expect(stopped?.lines?.[1]).toEqual({ tone: "error", text: "2. Pressed ⌘S" });
	});

	it("leaves an ao tool this build does not know to the generic row", () => {
		expect(aoToolView(claude("zoom", {}, []))).toBeUndefined();
		expect(aoToolView(activity({ summary: "Called linear/create_issue" }))).toBeUndefined();
	});
});
