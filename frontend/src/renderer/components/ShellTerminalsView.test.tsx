import { render as rtlRender, screen } from "@testing-library/react";
import type { ReactElement } from "react";
import { beforeEach, describe, expect, it, vi } from "vitest";
import { useUiStore } from "../stores/ui-store";
import { ShellTerminalsView } from "./ShellTerminalsView";
import { TooltipProvider } from "./ui/tooltip";

function render(ui: ReactElement) {
	return rtlRender(<TooltipProvider>{ui}</TooltipProvider>);
}

const terminalMocks = vi.hoisted(() => ({ data: [] as unknown[], adopted: new Map<string, string>() }));

vi.mock("../hooks/useShellTerminals", () => ({
	adoptedShellHandle: (handleId: string) => terminalMocks.adopted.get(handleId),
	useCloseShellTerminal: () => ({ mutate: vi.fn() }),
	useRenameShellTerminal: () => ({ mutate: vi.fn() }),
	useShellTerminals: () => ({ data: terminalMocks.data }),
}));

vi.mock("../lib/shell-context", () => ({
	useShell: () => ({ daemonStatus: { state: "ready" } }),
}));

vi.mock("./TerminalPane", () => ({ TerminalPane: () => <div>terminal body</div> }));

describe("ShellTerminalsView", () => {
	beforeEach(() => {
		terminalMocks.data = [];
		terminalMocks.adopted.clear();
		useUiStore.setState({ activeShellTerminalHandleId: null });
	});

	it("points the empty state at the visible plus tab-strip control", () => {
		render(<ShellTerminalsView />);

		expect(screen.getByText("No terminals open")).toBeInTheDocument();
		expect(screen.getByText(/use the \+ button/i)).toBeInTheDocument();
		expect(screen.queryByText(/terminal button/i)).not.toBeInTheDocument();
	});

	it("keeps a tab selected while it was pending selected once it becomes its shell", () => {
		const shell = (handleId: string, title: string) => ({ handleId, title, workingDir: "/tmp", createdAt: "2026-08-31T00:00:00Z" });
		terminalMocks.data = [shell("ptyhost-v1:shellterm-one", "Terminal 1"), shell("ptyhost-v1:shellterm-two", "Terminal 2")];
		terminalMocks.adopted.set("pending-shell:two", "ptyhost-v1:shellterm-two");
		useUiStore.setState({ activeShellTerminalHandleId: "pending-shell:two" });

		render(<ShellTerminalsView />);

		expect(useUiStore.getState().activeShellTerminalHandleId).toBe("ptyhost-v1:shellterm-two");
	});

});
