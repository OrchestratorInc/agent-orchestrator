import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { fireEvent, render, screen } from "@testing-library/react";
import { beforeEach, expect, it } from "vitest";
import { agentReadinessQueryKey } from "../hooks/useAgentReadinessQuery";
import { useUiStore } from "../stores/ui-store";
import { SessionImportOffer } from "./SessionImportOffer";

const agent = (id: string, installed: boolean, auth: string) => ({
	id,
	installation: { state: installed ? "installed" : "not_installed" },
	authentication: { state: auth },
});

function renderOffer(...agents: ReturnType<typeof agent>[]) {
	const client = new QueryClient();
	client.setQueryData(agentReadinessQueryKey, { agents });
	return render(
		<QueryClientProvider client={client}>
			<SessionImportOffer />
		</QueryClientProvider>,
	);
}

beforeEach(() => {
	window.localStorage.clear();
	useUiStore.setState({ settingsModal: null });
});

it("offers one button per configured agent, each opening that agent's Import tab", () => {
	renderOffer(agent("claude-code", true, "authorized"), agent("codex", true, "unauthorized"), agent("cursor", true, "authorized"));
	expect(screen.queryByRole("button", { name: "Import Codex chats" })).not.toBeInTheDocument();
	expect(screen.queryByRole("button", { name: /Cursor/ })).not.toBeInTheDocument();
	fireEvent.click(screen.getByRole("button", { name: "Import Claude Code chats" }));
	expect(useUiStore.getState().settingsModal).toMatchObject({ scope: "global", section: "harness", focusAgentId: "claude-code", harnessTab: "import" });
});

it("shows nothing when no importable agent is configured", () => {
	renderOffer(agent("claude-code", false, "authorized"), agent("codex", true, "unauthorized"));
	expect(screen.queryByText("Bring your earlier chats into AO")).not.toBeInTheDocument();
});

it("stays hidden once dismissed", () => {
	const { unmount } = renderOffer(agent("codex", true, "authorized"));
	fireEvent.click(screen.getByRole("button", { name: "Not now" }));
	expect(screen.queryByText("Bring your earlier chats into AO")).not.toBeInTheDocument();
	unmount();
	renderOffer(agent("codex", true, "authorized"));
	expect(screen.queryByRole("button", { name: "Import Codex chats" })).not.toBeInTheDocument();
});
