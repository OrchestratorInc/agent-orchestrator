import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { fireEvent, render, screen, waitFor, within } from "@testing-library/react";
import { beforeEach, expect, it } from "vitest";
import { agentReadinessQueryKey } from "../hooks/useAgentReadinessQuery";
import { useSessionImportStore } from "../stores/session-import-store";
import { useUiStore } from "../stores/ui-store";
import { NoticeViewport } from "./Notice";
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
			<NoticeViewport />
			<SessionImportOffer />
		</QueryClientProvider>,
	);
}

beforeEach(() => {
	window.localStorage.clear();
	useUiStore.setState({ settingsModal: null, sessionImportOfferClosed: false, firstRunPromptActive: false });
	useSessionImportStore.setState({ dialog: null });
});

it("opens the folder picker for every configured agent at once", () => {
	renderOffer(agent("claude-code", true, "authorized"), agent("codex", true, "authorized"), agent("amp", true, "authorized"));
	expect(screen.getByRole("img", { name: "Claude Code, Codex" })).toBeInTheDocument();
	fireEvent.click(screen.getByRole("button", { name: "Choose folders" }));
	expect(useSessionImportStore.getState().dialog).toEqual({ kind: "folders" });
	expect(useUiStore.getState().settingsModal).toBeNull();
});

it("shows only logos for harnesses with import enabled", () => {
	renderOffer(...["claude-code", "codex", "droid", "kimi", "opencode", "pi"].map((id) => agent(id, true, "authorized")));
	expect(screen.getByRole("img", { name: "Claude Code, Codex, OpenCode, Pi" })).toHaveTextContent("+1");
});

it("shows as a notice in the corner, like an agent update", () => {
	renderOffer(agent("codex", true, "authorized"));
	const region = screen.getByRole("region", { name: "Import earlier chats" });
	expect(region).toHaveClass("absolute", "top-3", "right-3");
	expect(within(region).getByRole("article", { name: "Import earlier chats" })).toBeInTheDocument();
});

it("does not offer imports when only deferred harnesses are configured", () => {
	renderOffer(agent("kimi", true, "authorized"), agent("droid", true, "authorized"));
	expect(screen.queryByText("Import earlier chats")).not.toBeInTheDocument();
});

it("counts an agent whose sign-in check was inconclusive", () => {
	renderOffer(agent("claude-code", true, "unauthorized"), agent("codex", true, "unknown"));
	expect(screen.getByRole("img", { name: "Codex" })).toBeInTheDocument();
});

it("shows nothing when no importable agent is configured", () => {
	renderOffer(agent("claude-code", false, "authorized"), agent("codex", true, "unauthorized"));
	expect(screen.queryByText("Import earlier chats")).not.toBeInTheDocument();
});

it("stays hidden once dismissed", async () => {
	const { unmount } = renderOffer(agent("codex", true, "authorized"));
	fireEvent.click(screen.getByRole("button", { name: "Not now" }));
	await waitFor(() => expect(screen.queryByText("Import earlier chats")).not.toBeInTheDocument());
	expect(useUiStore.getState().sessionImportOfferClosed).toBe(true);
	expect(window.localStorage.getItem("ao.sessionImport.offerClosed")).toBe("true");
	unmount();
	renderOffer(agent("codex", true, "authorized"));
	expect(screen.queryByRole("button", { name: "Choose folders" })).not.toBeInTheDocument();
});

it("dismisses from the close button the same way", async () => {
	renderOffer(agent("codex", true, "authorized"));
	fireEvent.click(screen.getByRole("button", { name: "Dismiss import suggestion" }));
	await waitFor(() => expect(screen.queryByRole("region")).not.toBeInTheDocument());
	expect(window.localStorage.getItem("ao.sessionImport.offerClosed")).toBe("true");
});

it("waits for the first-run prompt, like agent updates", () => {
	useUiStore.setState({ firstRunPromptActive: true });
	renderOffer(agent("codex", true, "authorized"));
	expect(screen.queryByRole("region")).not.toBeInTheDocument();
});
