import { render, screen, waitFor } from "@testing-library/react";
import { useEffect } from "react";
import { beforeEach, expect, it, vi } from "vitest";
import { SessionViewportContext } from "../../lib/session-viewport";
import type { WorkspaceSession } from "../../types/workspace";
import { RetainedSessionChatSurface, SessionChatCacheProvider } from "./SessionChatCache";

const state = vi.hoisted(() => ({ mounts: 0, unmounts: 0, sessions: [] as WorkspaceSession[],
	remote: [] as Array<{ hostId: string; isSuccess: boolean; data: Array<{ sessions: WorkspaceSession[] }> }>,
}));
vi.mock("../../hooks/useWorkspaceQuery", () => ({
	useWorkspaceQuery: () => ({ isSuccess: true, data: [{ sessions: state.sessions }] }),
	useRemoteWorkspaceSnapshots: () => state.remote,
}));
vi.mock("./SessionChatSurface", () => ({
	SessionChatSurface: ({ session }: { session: WorkspaceSession }) => {
		useEffect(() => {
			state.mounts++;
			return () => {
				state.unmounts++;
			};
		}, []);
		return (
			<div data-testid={`chat-${session.id}`}>
				<span>{session.title}</span>
				<textarea aria-label={`input-${session.id}`} />
			</div>
		);
	},
}));
const session = (id: string): WorkspaceSession => ({
	id,
	title: id,
	workspaceId: "test",
	workspaceName: "test",
	provider: "codex",
	status: "working",
	mode: "chat",
	updatedAt: "now",
	prs: [],
});
beforeEach(() => {
	state.mounts = 0;
	state.unmounts = 0;
	state.remote = [];
	state.sessions = [session("a"), session("b")];
});
const tree = (ids: string[], embedded = false) => (
	<SessionChatCacheProvider>
		<SessionViewportContext.Provider value={{ visible: true, embedded }}>
			{ids.map((id) => (
				<RetainedSessionChatSurface key={id} session={state.sessions.find((entry) => entry.id === id)!} />
			))}
		</SessionViewportContext.Provider>
	</SessionChatCacheProvider>
);

it("keeps two live Chat interfaces mounted and preserves draft DOM through Grid, Kanban and single view", async () => {
	const view = render(tree(["a", "b"], true));
	const input = await screen.findByRole("textbox", { name: "input-a" });
	(input as HTMLTextAreaElement).value = "unsent draft";
	view.rerender(tree([]));
	expect(input.isConnected).toBe(true);
	expect(input.closest("[data-chat-cache-key]")).toHaveAttribute("aria-hidden", "true");
	view.rerender(tree(["a"]));
	expect(screen.getByRole("textbox", { name: "input-a" })).toBe(input);
	expect(input).toHaveValue("unsent draft");
	view.rerender(tree(["b", "a"], true));
	expect(screen.getByRole("textbox", { name: "input-a" })).toBe(input);
	expect(state.mounts).toBe(2);
	expect(state.unmounts).toBe(0);
	view.unmount();
	expect(state.unmounts).toBe(2);
	expect(document.querySelector("[data-chat-cache-key]")).toBeNull();
});

it("updates parked Chat metadata without remounting its history", async () => {
	const view = render(tree(["a"]));
	const node = await screen.findByTestId("chat-a");
	view.rerender(tree([]));
	state.sessions = [{ ...state.sessions[0], title: "Updated task" }, state.sessions[1]];
	view.rerender(tree([]));
	await waitFor(() => expect(node).toHaveTextContent("Updated task"));
	expect(state.mounts).toBe(1);
});

it("updates visible metadata without moving the focused composer or its selection", async () => {
	const view = render(tree(["a"]));
	const node = await screen.findByTestId("chat-a");
	const input = screen.getByRole("textbox", { name: "input-a" }) as HTMLTextAreaElement;
	input.value = "keep selected text";
	input.focus(); input.setSelectionRange(5, 13);
	const container = node.parentElement!;
	const parent = container.parentElement!;
	const append = vi.spyOn(parent, "appendChild");
	state.sessions = [{ ...state.sessions[0], title: "Stream update" }, state.sessions[1]];
	view.rerender(tree(["a"]));
	await waitFor(() => expect(node).toHaveTextContent("Stream update"));
	expect(append).not.toHaveBeenCalled();
	expect(document.activeElement).toBe(input);
	expect([input.selectionStart, input.selectionEnd]).toEqual([5, 13]);
	append.mockRestore();
});

it("disposes UI only after an authoritative session removal or interface change", async () => {
	const view = render(tree(["a"]));
	await screen.findByTestId("chat-a");
	view.rerender(tree([]));
	state.sessions = [state.sessions[1]];
	view.rerender(tree([]));
	await waitFor(() => expect(state.unmounts).toBe(1));
	expect(document.querySelector("[data-chat-cache-key]")).toBeNull();
});

it("preserves host-qualified Chat drafts, ignores local and failed remote snapshots, and disposes only the owning host", async () => {
	const a = { ...session("same"), hostId: "box-a", title: "A" };
	const b = { ...session("same"), hostId: "box-b", title: "B" };
	state.sessions = [];
	state.remote = [a, b].map((entry) => ({ hostId: entry.hostId, isSuccess: true, data: [{ sessions: [entry] }] }));
	const remoteTree = (visible: WorkspaceSession[]) => <SessionChatCacheProvider>
		{visible.map((entry) => <RetainedSessionChatSurface key={entry.hostId} hostId={entry.hostId} session={entry} />)}
	</SessionChatCacheProvider>;
	const view = render(remoteTree([a, b]));
	await waitFor(() => expect(state.mounts).toBe(2));
	const inputs = screen.getAllByRole("textbox", { name: "input-same" });
	(inputs[0] as HTMLTextAreaElement).value = "A draft";
	(inputs[1] as HTMLTextAreaElement).value = "B draft";
	view.rerender(remoteTree([]));
	state.remote = [{ hostId: "box-a", isSuccess: false, data: [] }, state.remote[1]];
	view.rerender(remoteTree([]));
	expect(state.unmounts).toBe(0);
	view.rerender(remoteTree([a, b]));
	expect(screen.getAllByRole("textbox", { name: "input-same" })).toEqual(inputs);
	expect(inputs[0]).toHaveValue("A draft"); expect(inputs[1]).toHaveValue("B draft");
	view.rerender(remoteTree([]));
	state.remote = [{ hostId: "box-a", isSuccess: true, data: [] }, state.remote[1]];
	view.rerender(remoteTree([]));
	await waitFor(() => expect(state.unmounts).toBe(1));
	view.rerender(remoteTree([b]));
	expect(screen.getByRole("textbox", { name: "input-same" })).toBe(inputs[1]);
	expect(state.mounts).toBe(2);
});
