import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { beforeEach, describe, expect, it, vi } from "vitest";
import { ChatLinkProvider, ChatMarkdown } from "../components/chat/ChatMarkdown";
import type { WorkspaceSession } from "../types/workspace";
import { useSessionBrowserLink } from "./useSessionBrowserLink";

const { getMock, postMock } = vi.hoisted(() => ({ getMock: vi.fn(), postMock: vi.fn() }));

vi.mock("../lib/host-clients", () => ({
	clientForSessionHost: () => ({ GET: getMock, POST: postMock }),
}));

const session = { id: "sess-1", status: "working" } as WorkspaceSession;
const catalog = ["reports/index.html"];

function Chat({ text, target = session, openInBrowser, onUnownedFile }: { text: string; target?: WorkspaceSession; openInBrowser: (uri: string) => Promise<void>; onUnownedFile: (reference: string) => void }) {
	const openLink = useSessionBrowserLink(target, openInBrowser, catalog, onUnownedFile);
	return (
		<ChatLinkProvider onLinkOpen={openLink} onFileOpen={onUnownedFile} workspacePaths={catalog}>
			<ChatMarkdown text={text} />
		</ChatLinkProvider>
	);
}

function renderChat(text: string, target?: WorkspaceSession) {
	const openInBrowser = vi.fn(async (_uri: string) => undefined);
	const onUnownedFile = vi.fn();
	render(
		<QueryClientProvider client={new QueryClient()}>
			<Chat text={text} target={target} openInBrowser={openInBrowser} onUnownedFile={onUnownedFile} />
		</QueryClientProvider>,
	);
	return { openInBrowser, onUnownedFile };
}

function resolveRequests() {
	return getMock.mock.calls.filter(([path]) => path === "/api/v1/sessions/{sessionId}/workspace/resolve");
}

describe("useSessionBrowserLink", () => {
	beforeEach(() => {
		getMock.mockReset();
		postMock.mockReset();
		postMock.mockResolvedValue({ data: {}, error: undefined });
	});

	it("does not preview a same-suffix workspace page for an absolute HTML link outside the session", async () => {
		getMock.mockResolvedValue({ data: { sessionId: "sess-1", kind: "outside", path: "" }, error: undefined });
		const { openInBrowser, onUnownedFile } = renderChat("Open [preview](/other-repo/reports/index.html).");

		await userEvent.click(screen.getByRole("link", { name: "preview" }));

		await waitFor(() => expect(onUnownedFile).toHaveBeenCalledWith("/other-repo/reports/index.html"));
		expect(resolveRequests()).toEqual([[
			"/api/v1/sessions/{sessionId}/workspace/resolve",
			{ params: { path: { sessionId: "sess-1" }, query: { path: "/other-repo/reports/index.html" } } },
		]]);
		expect(openInBrowser).not.toHaveBeenCalled();
		expect(postMock).not.toHaveBeenCalled();
	});

	it("previews the workspace page the daemon resolves an absolute HTML link to", async () => {
		getMock.mockResolvedValue({ data: { sessionId: "sess-1", kind: "workspace", path: "reports/index.html" }, error: undefined });
		const { openInBrowser, onUnownedFile } = renderChat("Open [preview](/Users/me/worktrees/s/reports/index.html).");

		await userEvent.click(screen.getByRole("link", { name: "preview" }));

		await waitFor(() => expect(openInBrowser).toHaveBeenCalledTimes(1));
		expect(openInBrowser.mock.calls[0]?.[0]).toMatch(/\/sessions\/sess-1\/preview\/files\/reports\/index\.html$/);
		expect(onUnownedFile).not.toHaveBeenCalled();
	});

	it("leaves an unclassified absolute HTML link to the daemon preview instead of the file list", async () => {
		getMock.mockResolvedValue({ data: undefined, error: { code: "NOT_FOUND" } });
		const { openInBrowser, onUnownedFile } = renderChat("Open [preview](/other-repo/reports/index.html).");

		await userEvent.click(screen.getByRole("link", { name: "preview" }));

		await waitFor(() => expect(postMock).toHaveBeenCalledWith("/api/v1/sessions/{sessionId}/preview", {
			params: { path: { sessionId: "sess-1" } },
			body: { url: "/other-repo/reports/index.html", requireWorkspaceFile: true },
		}));
		expect(openInBrowser).not.toHaveBeenCalled();
		expect(onUnownedFile).not.toHaveBeenCalled();
	});

	it("previews a relative HTML link without asking the daemon to classify it", async () => {
		const { openInBrowser } = renderChat("Open [preview](reports/index.html).");

		await userEvent.click(screen.getByRole("link", { name: "preview" }));

		await waitFor(() => expect(openInBrowser).toHaveBeenCalledTimes(1));
		expect(openInBrowser.mock.calls[0]?.[0]).toMatch(/\/sessions\/sess-1\/preview\/files\/reports\/index\.html$/);
		expect(resolveRequests()).toEqual([]);
	});

	it("opens a cloud session's web link in the Browser tab and never asks the local daemon about its files", async () => {
		const cloudSession = { ...session, cloud: { orgId: "org-1" } } as WorkspaceSession;
		const { openInBrowser } = renderChat("See [the PR](https://github.com/o/r/pull/1) and [report](reports/index.html).", cloudSession);

		await userEvent.click(screen.getByRole("link", { name: "the PR" }));
		await userEvent.click(screen.getByRole("link", { name: "report" }));

		await waitFor(() => expect(openInBrowser).toHaveBeenCalledExactlyOnceWith("https://github.com/o/r/pull/1"));
		expect(getMock).not.toHaveBeenCalled();
		expect(postMock).not.toHaveBeenCalled();
	});
});
