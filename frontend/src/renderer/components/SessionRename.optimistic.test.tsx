import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { act, fireEvent, render, screen, waitFor } from "@testing-library/react";
import type { ComponentProps, ReactNode } from "react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { resetOptimisticRenamesForTest } from "../hooks/optimistic-renames";
import { useWorkspaceQuery, workspaceQueryKey } from "../hooks/useWorkspaceQuery";
import { useUiStore } from "../stores/ui-store";
import { CenterPane } from "./CenterPane";
import { TooltipProvider } from "./ui/tooltip";

// The whole optimistic path is real: the rename hook, the shared title store,
// the workspace query and its fetcher, and the tab that renders the title. Only
// the daemon (its list reads and the PATCH) is faked, so each test controls when
// a read or an acknowledgement lands.

const h = vi.hoisted(() => ({
	getMock: vi.fn(),
	renameSessionMock: vi.fn(),
}));

vi.mock("../lib/api-client", async (importOriginal) => ({
	...(await importOriginal<typeof import("../lib/api-client")>()),
	apiClient: { GET: h.getMock },
	hasTrustedApiBaseUrl: () => true,
}));
vi.mock("../lib/rename-session", () => ({ renameSession: h.renameSessionMock, renameCloudSession: vi.fn() }));
vi.mock("../lib/telemetry", () => ({ captureRendererEvent: vi.fn().mockResolvedValue(undefined) }));
vi.mock("../hooks/useCloudCp", () => ({ useCloudCp: () => ({ client: {}, ready: false, baseUrl: "", userId: "" }) }));
vi.mock("../hooks/useCloudOrg", () => ({ useCloudOrg: () => ({ org: undefined, isLoading: false, error: undefined, ready: false }) }));
vi.mock("./NotificationCenter", () => ({ NotificationCenter: () => null }));
vi.mock("./TerminalPane", () => ({ TerminalPane: () => <div>terminal body</div> }));
vi.mock("motion/react", () => ({
	motion: { div: ({ layoutScroll: _layoutScroll, ...props }: ComponentProps<"div"> & { layoutScroll?: boolean }) => <div {...props} /> },
	Reorder: {
		Group: ({ children }: { children: ReactNode }) => <div>{children}</div>,
		Item: ({ children }: { children: ReactNode }) => <div>{children}</div>,
	},
	useDragControls: () => ({ start: vi.fn() }),
	useMotionValue: () => ({ get: () => 0, set: vi.fn(), on: () => () => {} }),
}));
vi.mock("../lib/bridge", () => ({
	aoBridge: {
		editorHandoff: { getState: async () => ({ workspaceAvailable: false, targets: [] }) },
		app: {
			setCloseShellTerminalShortcutEnabled: () => {},
			onCloseShellTerminalShortcut: () => () => {},
			onPreviousTabShortcut: () => () => {},
			onNextTabShortcut: () => () => {},
		},
	},
}));

type Deferred<T> = { promise: Promise<T>; resolve: (value: T) => void; reject: (error: unknown) => void };
function deferred<T>(): Deferred<T> {
	let resolve!: (value: T) => void;
	let reject!: (error: unknown) => void;
	const promise = new Promise<T>((res, rej) => {
		resolve = res;
		reject = rej;
	});
	return { promise, resolve, reject };
}

const sessionsResponse = (displayName: string) => ({
	data: {
		sessions: [{
			id: "sess-1",
			projectId: "proj-1",
			displayName,
			harness: "claude-code",
			kind: "worker",
			mode: "tui",
			status: "working",
			activity: { state: "active", lastActivityAt: "2026-06-10T00:00:00Z" },
			isTerminated: false,
			updatedAt: "2026-06-10T00:00:00Z",
		}],
	},
	error: undefined,
});

/** The name the fake daemon has stored. */
let storedName = "do the thing";
/** Answers queued for the next session list reads, in order; then the stored name. */
let sessionReads: Array<Promise<ReturnType<typeof sessionsResponse>> | ReturnType<typeof sessionsResponse>> = [];

function serveSessions(...reads: typeof sessionReads) {
	sessionReads = reads;
}

/**
 * The next PATCH: the daemon stores the name as the request arrives (in send
 * order) unless it fails, and its answer comes back when the test settles it.
 */
function nextRename(answer: Deferred<void>, stores = true) {
	h.renameSessionMock.mockImplementationOnce((_id: string, name: string) => {
		if (stores) storedName = name;
		return answer.promise;
	});
}

let queryClient: QueryClient;

function Harness() {
	const { data } = useWorkspaceQuery({ includeCloud: false });
	const session = data?.flatMap((workspace) => workspace.sessions).find((candidate) => candidate.id === "sess-1");
	if (!session) return null;
	return (
		<>
			{/* A second surface mirroring the same title, as the sidebar does. */}
			<span data-testid="mirror">{session.title}</span>
			<CenterPane daemonReady session={session} theme="dark" />
		</>
	);
}

async function renderLoaded() {
	queryClient = new QueryClient({ defaultOptions: { queries: { retry: false } } });
	render(
		<QueryClientProvider client={queryClient}>
			<TooltipProvider>
				<Harness />
			</TooltipProvider>
		</QueryClientProvider>,
	);
	await screen.findByTestId("mirror");
}

function tabLabel(): string {
	return screen.getByRole("tab", { name: /claude code/i }).getAttribute("aria-label")?.split(" · ")[0] ?? "";
}

function shown() {
	return { tab: tabLabel(), mirror: screen.getByTestId("mirror").textContent };
}

/** Double-click the session tab, type, and press Enter — synchronously, as one gesture. */
function renameTo(name: string) {
	fireEvent.doubleClick(screen.getByRole("tab", { name: /claude code/i }), { detail: 2 });
	const input = screen.getByRole("textbox", { name: /^Rename / });
	input.focus();
	fireEvent.change(input, { target: { value: name } });
	fireEvent.keyDown(input, { key: "Enter" });
}

/** Re-read the session list and let the read land. */
async function refetch() {
	await act(async () => {
		await queryClient.refetchQueries({ queryKey: workspaceQueryKey });
	});
}

beforeEach(() => {
	resetOptimisticRenamesForTest();
	useUiStore.getState().clearGlobalToast();
	h.renameSessionMock.mockReset();
	h.getMock.mockReset().mockImplementation(async (url: string) => {
		if (url === "/api/v1/projects") return { data: { projects: [{ id: "proj-1", name: "my-app", path: "/tmp/my-app" }] }, error: undefined };
		if (url === "/api/v1/sessions") return sessionReads.shift() ?? sessionsResponse(storedName);
		throw new Error(`unexpected GET ${url}`);
	});
	storedName = "do the thing";
	serveSessions();
});

afterEach(() => {
	vi.useRealTimers();
});

describe("optimistic session rename", () => {
	it("shows the new title in the tab and its mirrors on the frame the editor closes", async () => {
		h.renameSessionMock.mockReturnValue(new Promise(() => {}));
		await renderLoaded();
		expect(shown()).toEqual({ tab: "do the thing", mirror: "do the thing" });

		// Hold every timer: TanStack delivers cache writes on setTimeout(0), so
		// anything that shows now came in the same commit as the editor closing.
		vi.useFakeTimers();
		renameTo("clearer task");

		expect(screen.queryByRole("textbox", { name: /^Rename / })).not.toBeInTheDocument();
		expect(shown()).toEqual({ tab: "clearer task", mirror: "clearer task" });
		expect(screen.queryByText("do the thing")).not.toBeInTheDocument();
		expect(h.renameSessionMock).toHaveBeenCalledWith("sess-1", "clearer task");
	});

	it("keeps the new title through a slow acknowledgement and stale reads", async () => {
		const ack = deferred<void>();
		h.renameSessionMock.mockReturnValue(ack.promise);
		await renderLoaded();
		renameTo("clearer task");

		// A read that lands while the PATCH is in flight still has the old name.
		serveSessions(sessionsResponse("do the thing"));
		await refetch();
		expect(shown()).toEqual({ tab: "clearer task", mirror: "clearer task" });

		// A read that began before the acknowledgement and lands after it.
		const staleRead = deferred<ReturnType<typeof sessionsResponse>>();
		storedName = "clearer task";
		serveSessions(staleRead.promise);
		let staleRefetch!: Promise<unknown>;
		act(() => {
			staleRefetch = queryClient.refetchQueries({ queryKey: workspaceQueryKey });
		});
		await act(async () => ack.resolve());
		await act(async () => {
			staleRead.resolve(sessionsResponse("do the thing"));
			await staleRefetch;
		});
		expect(shown()).toEqual({ tab: "clearer task", mirror: "clearer task" });

		// Reads begun after the acknowledgement are the server's word again.
		storedName = "renamed elsewhere";
		await refetch();
		await waitFor(() => expect(shown()).toEqual({ tab: "renamed elsewhere", mirror: "renamed elsewhere" }));
	});

	it("keeps the newest of repeated renames whatever order the answers land in", async () => {
		const first = deferred<void>();
		const second = deferred<void>();
		nextRename(first);
		nextRename(second);
		await renderLoaded();

		renameTo("first try");
		expect(shown().tab).toBe("first try");
		renameTo("second try");
		expect(shown()).toEqual({ tab: "second try", mirror: "second try" });

		// The second answer lands first, then the older one: neither brings back "first try".
		await act(async () => second.resolve());
		await act(async () => first.resolve());
		await act(async () => undefined);
		expect(shown()).toEqual({ tab: "second try", mirror: "second try" });
		expect(queryClient.getQueryData<{ sessions: { title: string }[] }[]>(workspaceQueryKey)?.[0]?.sessions[0]?.title).toBe("second try");
	});

	it("rolls back only the failed rename and says so", async () => {
		const first = deferred<void>();
		const second = deferred<void>();
		nextRename(first);
		nextRename(second, false);
		await renderLoaded();

		renameTo("first try");
		renameTo("second try");
		await act(async () => second.reject(new Error("Display name taken")));
		// The failed rename goes; the one still in flight shows.
		await waitFor(() => expect(shown()).toEqual({ tab: "first try", mirror: "first try" }));
		expect(useUiStore.getState().globalToasts).toEqual([
			expect.objectContaining({ title: "Couldn't rename to second try", body: "Display name taken", tone: "error" }),
		]);

		await act(async () => first.resolve());
		expect(shown()).toEqual({ tab: "first try", mirror: "first try" });
	});

	it("rolls a lone failed rename back to the old title", async () => {
		const ack = deferred<void>();
		nextRename(ack, false);
		await renderLoaded();

		renameTo("clearer task");
		expect(shown().tab).toBe("clearer task");
		await act(async () => ack.reject(new Error("boom")));

		await waitFor(() => expect(shown()).toEqual({ tab: "do the thing", mirror: "do the thing" }));
		expect(useUiStore.getState().globalToasts).toHaveLength(1);
	});

	it("does not report a superseded rename's failure", async () => {
		const first = deferred<void>();
		const second = deferred<void>();
		nextRename(first, false);
		nextRename(second);
		await renderLoaded();

		renameTo("first try");
		renameTo("second try");
		await act(async () => first.reject(new Error("boom")));
		expect(shown()).toEqual({ tab: "second try", mirror: "second try" });
		expect(useUiStore.getState().globalToasts).toHaveLength(0);

		await act(async () => second.resolve());
		expect(shown()).toEqual({ tab: "second try", mirror: "second try" });
	});

	it("leaves the title alone on Escape, an unchanged name, or a cleared field", async () => {
		await renderLoaded();

		fireEvent.doubleClick(screen.getByRole("tab", { name: /claude code/i }), { detail: 2 });
		const input = screen.getByRole("textbox", { name: /^Rename / });
		fireEvent.change(input, { target: { value: "never sent" } });
		fireEvent.keyDown(input, { key: "Escape" });
		renameTo("do the thing");
		renameTo("   ");

		expect(shown()).toEqual({ tab: "do the thing", mirror: "do the thing" });
		expect(h.renameSessionMock).not.toHaveBeenCalled();
	});
});
