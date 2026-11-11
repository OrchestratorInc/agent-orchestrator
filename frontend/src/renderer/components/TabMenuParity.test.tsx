import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { act, cleanup, fireEvent, render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { afterEach, describe, expect, it, vi } from "vitest";
import { editorHandoffQueryKey } from "../hooks/useEditorHandoff";
import type { WorkspaceSession } from "../types/workspace";
import {
	SessionTabAgentMenuContext,
	TabContextMenu,
	TabDropdownMenu,
	TabSplitMenuContext,
	type SessionTabAgentMenu,
	type TabMenuSpec,
} from "./TabContextMenu";
import { TooltipProvider } from "./ui/tooltip";

vi.mock("../lib/bridge", () => ({
	aoBridge: {
		editorHandoff: { getState: async () => ({ workspaceAvailable: true, targets: [] }) },
		clipboard: { writeText: async () => undefined },
		app: { openExternal: async () => undefined },
	},
}));
// The picker is a menu of its own; stand in for its row.
vi.mock("./TerminalAgentControls", () => ({
	TerminalAgentPickerRow: ({ session }: { session: WorkspaceSession }) => <button data-menu-row type="button">{`agent ${session.provider}`}</button>,
}));

afterEach(cleanup);

const session = {
	id: "sess-1",
	workspaceId: "proj-1",
	workspaceName: "my-app",
	title: "do the thing",
	provider: "claude-code",
	kind: "worker",
	branch: "feat/menus",
	status: "working",
	updatedAt: "2026-06-10T00:00:00Z",
	activity: { state: "idle", lastActivityAt: "2026-06-10T00:00:00Z" },
	prs: [{ number: 7, url: "https://github.com/acme/app/pull/7", title: "Menus", state: "open" }],
} as unknown as WorkspaceSession;

const editorState = {
	workspaceAvailable: true,
	preferredEditorId: "vscode",
	targets: [
		{ id: "vscode", name: "VS Code", kind: "editor" },
		{ id: "finder", name: "Finder", kind: "file_manager" },
	],
};

const file = (path: string, source?: Extract<TabMenuSpec, { kind: "file" }>["file"]["source"]) =>
	({ kind: "file", tabKey: `file:${path}`, onClose: vi.fn(), file: { path, sessionId: "sess-1", remote: false, source } }) satisfies TabMenuSpec;

const sessionSpec = { kind: "session", tabKey: "session", session, onRename: vi.fn(), canArchive: true } satisfies TabMenuSpec;

const terminalAgent: SessionTabAgentMenu = { session, blocked: false, picksAgent: true, switchTarget: "chat", switchAction: { disabled: false, run: vi.fn() } };
const chatAgent: SessionTabAgentMenu = { session, blocked: true, picksAgent: false, switchTarget: "tui", switchAction: { disabled: true, reason: "Busy", run: vi.fn() } };

// Every tab type with a menu, and the session tab on both surfaces.
const cases: Array<[string, TabMenuSpec, SessionTabAgentMenu?]> = [
	["session tab over a terminal", sessionSpec, terminalAgent],
	["session tab over a chat", sessionSpec, chatAgent],
	["session tab with no interface switch", { ...sessionSpec, onRename: undefined, canArchive: false }],
	["reviewer tab", { kind: "plain", tabKey: "reviewer:1" }],
	["terminal tab", { kind: "shell", tabKey: "sh-1", title: "Terminal 1", onRename: vi.fn(), onClose: vi.fn(), closeShortcut: true }],
	["file tab", file("src/app.ts")],
	["HTML file tab", file("site/index.html")],
	["diff tab", file("src/app.ts", { kind: "snapshot", label: "abc123" })],
	["artifact tab", file("report.md", { kind: "artifact" } as never)],
	["split group tab on a session", { kind: "group", onUngroup: vi.fn(), member: { ...sessionSpec, onRename: undefined } }, terminalAgent],
	["split group tab on a file", { kind: "group", onUngroup: vi.fn(), member: { ...file("src/app.ts"), closeShortcut: true } }],
];

/** Each row of the open menu: its role, text and whether it is disabled. */
function menuRows() {
	const menu = screen.getByRole("menu");
	return Array.from(menu.querySelectorAll("[role=menuitem], [role=separator], [data-menu-row]")).map((row) => {
		const role = row.getAttribute("role") ?? "custom";
		const disabled = row.getAttribute("aria-disabled") === "true" || row.hasAttribute("data-disabled");
		return `${role}|${row.textContent ?? ""}|${disabled ? "disabled" : "enabled"}`;
	});
}

/** The open menu's rows once its lookups (editor targets, workspace) have settled. */
async function settledMenuRows() {
	let rows = menuRows();
	for (let attempt = 0; attempt < 10; attempt += 1) {
		await act(async () => {});
		const next = menuRows();
		if (next.join("\n") === rows.join("\n")) return next;
		rows = next;
	}
	return rows;
}

async function renderBoth(spec: TabMenuSpec, agent: SessionTabAgentMenu | undefined, splitOn: boolean) {
	const queryClient = new QueryClient({ defaultOptions: { queries: { retry: false } } });
	queryClient.setQueryData(editorHandoffQueryKey("sess-1"), editorState);
	const split = splitOn ? (key: string) => (key === "session" ? { right: vi.fn(), down: vi.fn(), newTerminal: true } : { right: vi.fn() }) : undefined;
	render(
		<QueryClientProvider client={queryClient}>
			<TooltipProvider>
				<TabSplitMenuContext.Provider value={split}>
					<SessionTabAgentMenuContext.Provider value={agent}>
						<TabContextMenu spec={spec}>
							<button type="button">tab</button>
						</TabContextMenu>
						<TabDropdownMenu label="Tab actions" spec={spec} />
					</SessionTabAgentMenuContext.Provider>
				</TabSplitMenuContext.Provider>
			</TooltipProvider>
		</QueryClientProvider>,
	);
	// A closing modal menu can leave pointer-events off the body for a frame.
	const user = userEvent.setup({ pointerEventsCheck: 0 });

	fireEvent.contextMenu(screen.getByRole("button", { name: "tab" }));
	const contextRows = await settledMenuRows();
	fireEvent.keyDown(screen.getByRole("menu"), { key: "Escape" });
	expect(screen.queryByRole("menu")).not.toBeInTheDocument();

	await user.click(screen.getByRole("button", { name: "Tab actions" }));
	const dropdownRows = await settledMenuRows();
	return { contextRows, dropdownRows };
}

describe("tab menus", () => {
	describe.each([false, true])("with split panes %s", (splitOn) => {
		it.each(cases)("%s: the ⋮ menu and the right-click menu list the same rows", async (_name, spec, agent) => {
			const { contextRows, dropdownRows } = await renderBoth(spec, agent, splitOn);
			expect(contextRows.length).toBeGreaterThan(0);
			expect(dropdownRows).toEqual(contextRows);
		});
	});

	it("puts the terminal session tab's agent rows first and Archive last", async () => {
		const { dropdownRows } = await renderBoth(sessionSpec, terminalAgent, false);
		const labels = dropdownRows.filter((row) => !row.startsWith("separator")).map((row) => row.split("|")[1]);
		expect(labels[0]).toBe("agent claude-code");
		expect(labels[1]).toBe("Switch to chat UI");
		expect(labels).toContain("Open PR #7");
		expect(labels.at(-1)).toMatch(/^Archive session/);
	});

	it("keeps Split rows out of both menus while split panes are off", async () => {
		const { contextRows, dropdownRows } = await renderBoth(file("src/app.ts"), undefined, false);
		expect([...contextRows, ...dropdownRows].some((row) => row.includes("Split"))).toBe(false);
	});
});
