import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { fireEvent, render, screen, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
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

it("offers a direct button when only one importable agent is configured", () => {
	renderOffer(agent("claude-code", true, "authorized"), agent("codex", true, "unauthorized"), agent("amp", true, "authorized"));
	expect(screen.queryByRole("button", { name: /Import chats/ })).not.toBeInTheDocument();
	fireEvent.click(screen.getByRole("button", { name: "Import Claude Code chats" }));
	expect(useUiStore.getState().settingsModal).toMatchObject({ scope: "global", section: "harness", focusAgentId: "claude-code", harnessTab: "import" });
});

it("groups several agents under one menu, each opening that agent's Import tab", async () => {
	renderOffer(agent("claude-code", true, "authorized"), agent("pi", true, "authorized"), agent("codex", true, "unauthorized"));
	expect(screen.queryByRole("button", { name: /Claude Code chats/ })).not.toBeInTheDocument();
	await userEvent.click(screen.getByRole("button", { name: "Import chats" }));
	const menu = await screen.findByRole("menu");
	expect(within(menu).getAllByRole("menuitem").map((item) => item.textContent)).toEqual(["Claude Code", "Pi"]);
	await userEvent.click(within(menu).getByRole("menuitem", { name: "Pi" }));
	expect(useUiStore.getState().settingsModal).toMatchObject({ scope: "global", section: "harness", focusAgentId: "pi", harnessTab: "import" });
});

it("shows only logos for harnesses with import enabled", () => {
	renderOffer(...["claude-code", "codex", "droid", "kimi", "opencode", "pi"].map((id) => agent(id, true, "authorized")));
	expect(screen.getByRole("img", { name: "Claude Code, Codex, Pi" })).not.toHaveTextContent("+2");
});

it("does not offer imports when only deferred harnesses are configured", () => {
	renderOffer(agent("opencode", true, "authorized"), agent("kimi", true, "authorized"), agent("droid", true, "authorized"));
	expect(screen.queryByText("Bring your earlier chats into AO")).not.toBeInTheDocument();
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

it("opens Copilot's import tab only when Copilot is installed and signed in", () => {
	const view = renderOffer(agent("copilot", true, "unauthorized"));
	expect(screen.queryByRole("button", { name: "Import GitHub Copilot chats" })).not.toBeInTheDocument();
	view.unmount();
	renderOffer(agent("copilot", true, "authorized"));
	fireEvent.click(screen.getByRole("button", { name: "Import GitHub Copilot chats" }));
	expect(useUiStore.getState().settingsModal).toMatchObject({ scope: "global", section: "harness", focusAgentId: "copilot", harnessTab: "import" });
});
