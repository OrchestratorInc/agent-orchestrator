import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { render, fireEvent } from "@testing-library/react";
import { afterEach, expect, it, vi } from "vitest";
import { SplitDivider, SplitGroupTab } from "./SplitView";
import { TooltipProvider } from "./ui/tooltip";
import { useShellAgentStore } from "../stores/shell-agent-store";

vi.mock("./TerminalPane", () => ({ TerminalPane: () => null }));
const { useShellAgentStream } = vi.hoisted(() => ({ useShellAgentStream: vi.fn() }));
vi.mock("../stores/shell-agent-store", async (importOriginal) => ({
	...(await importOriginal<typeof import("../stores/shell-agent-store")>()),
	useShellAgentStream,
}));

afterEach(() => {
	vi.restoreAllMocks();
	vi.useRealTimers();
});

it("clears resize state and cancels the pending frame when a divider unmounts mid-drag", () => {
	vi.useFakeTimers();
	const screen = document.createElement("div");
	screen.style.setProperty("--split-r", "0.5");
	vi.spyOn(screen, "getBoundingClientRect").mockReturnValue(new DOMRect(0, 0, 800, 600));
	const onResize = vi.fn();
	const { getByRole, unmount } = render(
		<SplitDivider
			screenRef={{ current: screen }}
			divider={{
				path: "r", dir: "column", ratio: 0.5,
				box: { x: 0, y: 0, w: 1, h: 1 },
				style: { left: "0%", top: "50%", width: "100%", height: "" },
			}}
			onResize={onResize}
		/>,
	);
	const divider = getByRole("separator");
	fireEvent.pointerDown(divider, { button: 0, pointerId: 1, clientX: 400, clientY: 300 });
	fireEvent.pointerMove(divider, { pointerId: 1, clientX: 400, clientY: 400 });
	expect(document.body).toHaveClass("is-resizing-x");
	expect(document.body.style.cursor).toBe("row-resize");

	unmount();
	vi.runAllTimers();

	expect(document.body).not.toHaveClass("is-resizing-x");
	expect(document.body.style.cursor).toBe("");
	expect(screen.style.getPropertyValue("--split-r")).toBe("0.5");
	expect(onResize).not.toHaveBeenCalled();
});

it.each([true, false])("marks the group tab's frame as the selected tab only while it is on screen (active: %s)", (active) => {
	const member = (key: string, label: string) => ({ key, label, icon: null, onSelect: vi.fn(), onClose: vi.fn() });
	const { container } = render(
		<TooltipProvider>
			<SplitGroupTab active={active} focusedKey="session" members={[member("session", "Greeting"), member("sh-1", "Terminal 1")]} menu={{ kind: "group", onUngroup: vi.fn() }} />
		</TooltipProvider>,
	);
	const frame = container.querySelector("[data-terminal-tab-frame]");
	// The floating top bar draws the selected pill from data-active.
	expect(frame?.hasAttribute("data-active")).toBe(active);
});

it("names a grouped shell by the agent running in it and keeps the agent watch open", () => {
	useShellAgentStore.setState({ agents: { "sh-1": { harness: "claude-code", state: "working" } } });
	const member = (key: string, label: string, agentHandleId?: string) => ({ key, label, icon: null, onSelect: vi.fn(), onClose: vi.fn(), agentHandleId });
	const { getByText, getByRole, container } = render(
		<TooltipProvider>
			<SplitGroupTab active focusedKey="sh-1" members={[member("session", "Greeting"), member("sh-1", "Terminal 1", "sh-1")]} menu={{ kind: "group", onUngroup: vi.fn() }} />
		</TooltipProvider>,
	);
	expect(getByText("Claude Code")).toBeInTheDocument();
	expect(container.querySelector("[data-shell-agent-state='working']")).not.toBeNull();
	// The member's aria-label would hide the badge's text, so it carries the state.
	expect(getByRole("tab", { name: "Claude Code · Claude Code is working" })).toBeInTheDocument();
	expect(useShellAgentStream).toHaveBeenLastCalledWith(true);
	useShellAgentStore.setState({ agents: {} });
});

it("closes the focused member's pane on a middle-click on its close button", () => {
	const onClose = vi.fn();
	const { getAllByRole } = render(
		<TooltipProvider>
			<SplitGroupTab
				active
				focusedKey="sh-1"
				members={[
					{ key: "session", label: "Greeting", icon: null, onSelect: vi.fn(), onClose: vi.fn() },
					{ key: "sh-1", label: "Terminal 1", icon: null, onSelect: vi.fn(), onClose },
				]}
				menu={{ kind: "group", onUngroup: vi.fn() }}
			/>
		</TooltipProvider>,
	);
	const close = getAllByRole("button").find((button) => button.closest("[data-terminal-tab-action]"))!;
	fireEvent(close, new MouseEvent("auxclick", { bubbles: true, button: 1 }));
	expect(onClose).toHaveBeenCalledOnce();
});

it("lists the focused member's tab menu below Ungroup", () => {
	const member = (key: string, label: string) => ({ key, label, icon: null, onSelect: vi.fn(), onClose: vi.fn() });
	const { container, getAllByRole } = render(
		<QueryClientProvider client={new QueryClient()}>
			<TooltipProvider>
				<SplitGroupTab
					active
					focusedKey="sh-1"
					members={[member("session", "Greeting"), member("sh-1", "Terminal 1")]}
					menu={{ kind: "group", onUngroup: vi.fn(), member: { kind: "plain", tabKey: "sh-1", onClose: vi.fn() } }}
				/>
			</TooltipProvider>
		</QueryClientProvider>,
	);
	fireEvent.contextMenu(container.querySelector("[data-terminal-tab-frame]")!);

	expect(getAllByRole("menuitem").map((item) => item.textContent)).toEqual(["Ungroup", "Close tab"]);
});
