import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { expect, it, vi } from "vitest";
import type { WorkspaceSession } from "../types/workspace";
import { CenterPane } from "./CenterPane";
import { TooltipProvider } from "./ui/tooltip";

const renameSession = vi.hoisted(() => vi.fn().mockResolvedValue(undefined));
vi.mock("../lib/rename-session", () => ({ renameSession }));
vi.mock("./TerminalPane", () => ({ TerminalPane: () => null }));
vi.mock("./NotificationCenter", () => ({ NotificationCenter: () => null }));

it("独立编排器标签和终端无障碍标题隐藏 ID，并保留手动改名", async () => {
	const session: WorkspaceSession = {
		id: "project-independent-orchestrator-42", title: "project-independent-orchestrator-42",
		workspaceId: "project", workspaceName: "项目", provider: "codex",
		kind: "orchestrator", independentOrchestrator: true, status: "idle", prs: [],
		updatedAt: "2026-11-11T00:00:00Z",
	};
	const queryClient = new QueryClient({ defaultOptions: { queries: { retry: false } } });
	render(
		<QueryClientProvider client={queryClient}>
			<TooltipProvider>
				<CenterPane session={session} daemonReady theme="dark" />
			</TooltipProvider>
		</QueryClientProvider>,
	);
	expect(screen.getByRole("tabpanel", { name: "Untitled task terminal" })).toBeInTheDocument();
	const tab = screen.getByRole("tab", { name: /^Untitled task/ });
	expect(tab).not.toHaveTextContent(session.id);
	const user = userEvent.setup();
	await user.dblClick(tab);
	const input = screen.getByRole("textbox");
	await user.clear(input);
	await user.type(input, "修复登录{Enter}");
	await waitFor(() => expect(renameSession).toHaveBeenCalledWith(session.id, "修复登录"));
});
