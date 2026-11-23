import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { act, renderHook, waitFor } from "@testing-library/react";
import type { ReactNode } from "react";
import { beforeEach, describe, expect, it, vi } from "vitest";

const { patchMock } = vi.hoisted(() => ({ patchMock: vi.fn() }));
const shellStoreMock = vi.hoisted(() => ({
	load: vi.fn(async () => undefined),
	setPreference: vi.fn(async () => undefined),
	preference: { kind: "auto" as string, path: undefined as string | undefined },
}));
const { deleteMock, getMock, postMock, isWindowsMock, remoteA, remoteB } = vi.hoisted(() => ({
	deleteMock: vi.fn(),
	getMock: vi.fn(),
	postMock: vi.fn(),
	isWindowsMock: vi.fn(() => false),
	remoteA: { DELETE: vi.fn(), GET: vi.fn(), PATCH: vi.fn(), POST: vi.fn() },
	remoteB: { DELETE: vi.fn(), GET: vi.fn(), PATCH: vi.fn(), POST: vi.fn() },
}));
const cloudResumeMock = vi.hoisted(() => vi.fn());

vi.mock("../lib/api-client", () => ({
	apiClient: { DELETE: deleteMock, GET: getMock, PATCH: patchMock, POST: postMock },
	apiErrorCode: (error: unknown) =>
		typeof error === "object" && error !== null && "code" in error ? (error as { code?: string }).code : undefined,
	hasTrustedApiBaseUrl: () => true,
}));

vi.mock("../lib/host-clients", () => ({
	clientForSessionHost: (hostId?: string) => hostId === "host-a" ? remoteA : hostId === "host-b" ? remoteB : {
		DELETE: deleteMock, GET: getMock, PATCH: patchMock, POST: postMock,
	},
}));

vi.mock("../lib/platform", async (importOriginal) => ({
	...(await importOriginal<typeof import("../lib/platform")>()),
	isWindowsPlatform: isWindowsMock,
}));
vi.mock("./useCloudCp", () => ({
	useCloudCp: () => ({ client: { resumeSession: cloudResumeMock } }),
}));
vi.mock("../stores/terminal-shell-store", () => ({
	terminalShellRequestValue: (preference: { kind: string; path?: string }) =>
		preference.kind === "custom" ? preference.path?.trim() || "auto" : preference.kind,
		useTerminalShellStore: { getState: () => shellStoreMock },
}));

import {
	adoptedShellHandle,
	reloadCloudShellTerminalsForTest,
	type ShellTerminal,
	shellTerminalsQueryKey,
	shellTerminalsQueryKeyForHost,
	useCloseShellTerminal,
	useOpenShellTerminal,
	toShellTerminal,
	useRenameShellTerminal,
	useReopenShellTerminal,
	useShellTerminals,
} from "./useShellTerminals";
import { takeCloudShellInput } from "../lib/cloud-shell-input";
import { resetOptimisticRenamesForTest } from "./optimistic-renames";
import { useUiStore } from "../stores/ui-store";
import { useShellAgentStore } from "../stores/shell-agent-store";

const shells: ShellTerminal[] = [
	{
		createdAt: "2026-08-27T00:00:00Z",
		handleId: "ptyhost-v1:shellterm-one",
		title: "one",
		workingDir: "/tmp",
	},
	{
		createdAt: "2026-08-27T00:01:00Z",
		handleId: "ptyhost-v1:shellterm-two",
		title: "two",
		workingDir: "/tmp",
	},
];

function wrapper(queryClient: QueryClient) {
	return function Wrapper({ children }: { children: ReactNode }) {
		return <QueryClientProvider client={queryClient}>{children}</QueryClientProvider>;
	};
}

function queryClientWithShells() {
	const queryClient = new QueryClient({
		defaultOptions: { mutations: { retry: false }, queries: { retry: false } },
	});
	queryClient.setQueryData(shellTerminalsQueryKey, shells);
	return queryClient;
}

beforeEach(() => {
	resetOptimisticRenamesForTest();
	useUiStore.getState().clearGlobalToast();
	useShellAgentStore.setState({ agents: {}, resuming: {} });
	deleteMock.mockReset();
	getMock.mockReset();
	patchMock.mockReset();
	postMock.mockReset();
	for (const client of [remoteA, remoteB]) {
		client.DELETE.mockReset();
		client.GET.mockReset();
		client.PATCH.mockReset();
		client.POST.mockReset();
	}
	cloudResumeMock.mockReset();
	cloudResumeMock.mockResolvedValue({ session: { desiredState: "running" } });
	isWindowsMock.mockReturnValue(false);
	shellStoreMock.load.mockClear();
	shellStoreMock.setPreference.mockClear();
	shellStoreMock.preference = { kind: "auto", path: undefined };
});

describe("host-scoped shell terminals", () => {
	it("keeps equal daemon IDs in separate local and remote query caches", async () => {
		const queryClient = new QueryClient({ defaultOptions: { queries: { retry: false } } });
		getMock.mockResolvedValue({ data: { shellTerminals: [{ ...shells[0], title: "local" }] } });
		remoteA.GET.mockResolvedValue({ data: { shellTerminals: [{ ...shells[0], title: "A" }] } });
		remoteB.GET.mockResolvedValue({ data: { shellTerminals: [{ ...shells[0], title: "B" }] } });
		const local = renderHook(() => useShellTerminals(), { wrapper: wrapper(queryClient) });
		const a = renderHook(() => useShellTerminals("host-a"), { wrapper: wrapper(queryClient) });
		const b = renderHook(() => useShellTerminals("host-b"), { wrapper: wrapper(queryClient) });

		await waitFor(() => expect([local.result.current.data, a.result.current.data, b.result.current.data]).toEqual([
			[{ ...shells[0], title: "local" }],
			[{ ...shells[0], title: "A", hostId: "host-a" }],
			[{ ...shells[0], title: "B", hostId: "host-b" }],
		]));
		expect(queryClient.getQueryData(shellTerminalsQueryKeyForHost("host-a"))).toEqual(a.result.current.data);
		expect(queryClient.getQueryData(shellTerminalsQueryKeyForHost("host-b"))).toEqual(b.result.current.data);
		expect(queryClient.getQueryData(shellTerminalsQueryKey)).toEqual(local.result.current.data);
		expect(getMock).toHaveBeenCalledWith("/api/v1/shell-terminals");
		expect(remoteA.GET).toHaveBeenCalledWith("/api/v1/shell-terminals");
		expect(remoteB.GET).toHaveBeenCalledWith("/api/v1/shell-terminals");
	});

	it("opens on the owning host without using the local Windows shell preference", async () => {
		isWindowsMock.mockReturnValue(true);
		const queryClient = new QueryClient({ defaultOptions: { mutations: { retry: false }, queries: { retry: false } } });
		queryClient.setQueryData(shellTerminalsQueryKey, [shells[0]]);
		queryClient.setQueryData(shellTerminalsQueryKeyForHost("host-b"), [shells[0]]);
		queryClient.setQueryData(shellTerminalsQueryKeyForHost("host-a"), []);
		remoteA.POST.mockResolvedValue({ data: { shellTerminal: { ...shells[0], sessionId: "same-session" } } });
		const { result } = renderHook(() => useOpenShellTerminal("host-a"), { wrapper: wrapper(queryClient) });

		let pending!: ShellTerminal;
		act(() => { pending = result.current.open({ projectId: "project-a", sessionId: "same-session" }); });
		expect(pending).toMatchObject({ hostId: "host-a", sessionId: "same-session", optimistic: true });
		expect(queryClient.getQueryData(shellTerminalsQueryKeyForHost("host-a"))).toEqual([pending]);
		await waitFor(() => expect(queryClient.getQueryData(shellTerminalsQueryKeyForHost("host-a"))).toEqual([
			{ ...shells[0], sessionId: "same-session", hostId: "host-a" },
		]));

		expect(remoteA.POST).toHaveBeenCalledWith("/api/v1/shell-terminals", {
			body: { startOnAttach: true, title: "Terminal 1", projectId: "project-a", sessionId: "same-session" },
		});
		expect(postMock).not.toHaveBeenCalled();
		expect(shellStoreMock.load).not.toHaveBeenCalled();
		expect(queryClient.getQueryData(shellTerminalsQueryKeyForHost("host-b"))).toEqual([shells[0]]);
		expect(queryClient.getQueryData(shellTerminalsQueryKey)).toEqual([shells[0]]);
	});

	it("renames and closes only the matching host's equal handle", async () => {
		const queryClient = new QueryClient({ defaultOptions: { mutations: { retry: false }, queries: { retry: false } } });
		queryClient.setQueryData(shellTerminalsQueryKey, [shells[0]]);
		queryClient.setQueryData(shellTerminalsQueryKeyForHost("host-a"), [{ ...shells[0], hostId: "host-a" }]);
		queryClient.setQueryData(shellTerminalsQueryKeyForHost("host-b"), [{ ...shells[0], hostId: "host-b" }]);
		remoteA.PATCH.mockResolvedValue({ data: { shellTerminal: { ...shells[0], title: "renamed" } } });
		remoteA.DELETE.mockResolvedValue({});
		const rename = renderHook(() => useRenameShellTerminal("host-a"), { wrapper: wrapper(queryClient) });
		const close = renderHook(() => useCloseShellTerminal("host-a"), { wrapper: wrapper(queryClient) });

		await act(async () => rename.result.current.mutateAsync({ handleId: shells[0].handleId, title: "renamed" }));
		expect(remoteA.PATCH).toHaveBeenCalledWith("/api/v1/shell-terminals/{handleId}", {
			params: { path: { handleId: shells[0].handleId } }, body: { title: "renamed" },
		});
		expect(queryClient.getQueryData<ShellTerminal[]>(shellTerminalsQueryKeyForHost("host-a"))?.[0]?.title).toBe("renamed");
		await act(async () => close.result.current.mutateAsync(shells[0].handleId));
		expect(remoteA.DELETE).toHaveBeenCalledWith("/api/v1/shell-terminals/{handleId}", {
			params: { path: { handleId: shells[0].handleId } },
		});
		expect(patchMock).not.toHaveBeenCalled();
		expect(deleteMock).not.toHaveBeenCalled();
		expect(queryClient.getQueryData(shellTerminalsQueryKeyForHost("host-a"))).toEqual([]);
		expect(queryClient.getQueryData(shellTerminalsQueryKeyForHost("host-b"))).toEqual([{ ...shells[0], hostId: "host-b" }]);
		expect(queryClient.getQueryData(shellTerminalsQueryKey)).toEqual([shells[0]]);
	});
});

describe("useOpenShellTerminal", () => {
	it("publishes the returned shell immediately without waiting for a list refetch", async () => {
		const shell = shells[0];
		postMock.mockResolvedValue({
			data: {
				shellTerminal: {
					createdAt: shell.createdAt,
					handleId: shell.handleId,
					title: shell.title,
					workingDir: shell.workingDir,
				},
			},
		});
		const queryClient = new QueryClient({
			defaultOptions: { mutations: { retry: false }, queries: { retry: false } },
		});
		queryClient.setQueryData(shellTerminalsQueryKey, []);
		const { result } = renderHook(() => useOpenShellTerminal(), { wrapper: wrapper(queryClient) });

		await act(async () => result.current.mutateAsync({}));

		expect(queryClient.getQueryData(shellTerminalsQueryKey)).toEqual([shell]);
	});

	it("sends the saved Windows shell preference to the daemon", async () => {
		isWindowsMock.mockReturnValue(true);
		shellStoreMock.preference = { kind: "git-bash", path: undefined };
		postMock.mockResolvedValue({ data: { shellTerminal: { ...shells[0] } } });
		const queryClient = new QueryClient({ defaultOptions: { mutations: { retry: false } } });
		const { result } = renderHook(() => useOpenShellTerminal(), { wrapper: wrapper(queryClient) });

		await act(async () => result.current.mutateAsync({ projectId: "project-1" }));

		expect(shellStoreMock.load).toHaveBeenCalledOnce();
		expect(postMock).toHaveBeenCalledWith("/api/v1/shell-terminals", {
			body: { startOnAttach: true, projectId: "project-1", shell: "git-bash" },
		});
	});

	it("lets an explicit shell override the saved preference", async () => {
		isWindowsMock.mockReturnValue(true);
		shellStoreMock.preference = { kind: "git-bash", path: undefined };
		postMock.mockResolvedValue({ data: { shellTerminal: { ...shells[0] } } });
		const queryClient = new QueryClient({ defaultOptions: { mutations: { retry: false } } });
		const { result } = renderHook(() => useOpenShellTerminal(), { wrapper: wrapper(queryClient) });

		await act(async () => result.current.mutateAsync({ shell: "C:\\Tools\\bash.exe" }));

		expect(postMock).toHaveBeenCalledWith("/api/v1/shell-terminals", {
			body: { startOnAttach: true, shell: "C:\\Tools\\bash.exe" },
		});
	});

	it("sends the configured shell outside Windows too", async () => {
		shellStoreMock.preference = { kind: "custom", path: "/opt/homebrew/bin/fish" };
		postMock.mockResolvedValue({ data: { shellTerminal: { ...shells[0] } } });
		const queryClient = new QueryClient({ defaultOptions: { mutations: { retry: false } } });
		const { result } = renderHook(() => useOpenShellTerminal(), { wrapper: wrapper(queryClient) });

		await act(async () => result.current.mutateAsync({}));

		expect(shellStoreMock.load).toHaveBeenCalled();
		expect(postMock).toHaveBeenCalledWith("/api/v1/shell-terminals", {
			body: { startOnAttach: true, shell: "/opt/homebrew/bin/fish" },
		});
	});

	it("normalizes an unavailable configured shell back to Automatic", async () => {
		isWindowsMock.mockReturnValue(true);
		shellStoreMock.preference = { kind: "custom", path: "C:\\missing\\shell.exe" };
		postMock.mockResolvedValue({ error: { code: "SHELL_TERMINAL_SHELL_UNAVAILABLE" } });
		const queryClient = new QueryClient({ defaultOptions: { mutations: { retry: false } } });
		const { result } = renderHook(() => useOpenShellTerminal(), { wrapper: wrapper(queryClient) });

		await expect(act(async () => result.current.mutateAsync({}))).rejects.toEqual({
			code: "SHELL_TERMINAL_SHELL_UNAVAILABLE",
		});
		await waitFor(() => expect(shellStoreMock.setPreference).toHaveBeenCalledWith({ kind: "auto" }));
	});

	it("creates cloud shell metadata without asking the local daemon", async () => {
		const queryClient = new QueryClient({
			defaultOptions: { mutations: { retry: false }, queries: { retry: false } },
		});
		queryClient.setQueryData(shellTerminalsQueryKey, []);
		const open = renderHook(() => useOpenShellTerminal(), { wrapper: wrapper(queryClient) });

		const shell = (await act(async () =>
			open.result.current.mutateAsync({
				projectId: "cloud-project",
				sessionId: "cloud-session",
				cloud: { orgId: "cloud-org" },
			}),
		))!;

		expect(postMock).not.toHaveBeenCalled();
		expect(cloudResumeMock).toHaveBeenCalledWith("cloud-org", "cloud-session");
		expect(shell).toMatchObject({
			projectId: "cloud-project",
			sessionId: "cloud-session",
			workingDir: "/workspace/repository",
			title: "Terminal 1",
			cloud: { orgId: "cloud-org" },
		});
		expect(shell.handleId).toMatch(/^cloud-shell-/);
		expect(queryClient.getQueryData(shellTerminalsQueryKey)).toEqual([shell]);

		const close = renderHook(() => useCloseShellTerminal(), { wrapper: wrapper(queryClient) });
		await act(async () => close.result.current.mutateAsync(shell.handleId));
		expect(deleteMock).not.toHaveBeenCalled();
		expect(queryClient.getQueryData(shellTerminalsQueryKey)).toEqual([]);
	});
});

describe("Cloud shell tabs across a restart", () => {
	it("lists a session's open and renamed Cloud shells again after a restart, and forgets closed ones", async () => {
		window.localStorage.removeItem("ao.cloudShellTerminals");
		reloadCloudShellTerminalsForTest();
		getMock.mockResolvedValue({ data: { shellTerminals: [] } });
		const queryClient = new QueryClient({ defaultOptions: { mutations: { retry: false }, queries: { retry: false } } });
		queryClient.setQueryData(shellTerminalsQueryKey, []);
		const open = renderHook(() => useOpenShellTerminal(), { wrapper: wrapper(queryClient) });
		const input = { projectId: "cloud-project", sessionId: "cloud-session", cloud: { orgId: "cloud-org" } };
		const kept = (await act(async () => open.result.current.mutateAsync(input)))!;
		const closed = (await act(async () => open.result.current.mutateAsync(input)))!;
		const rename = renderHook(() => useRenameShellTerminal(), { wrapper: wrapper(queryClient) });
		await act(async () => rename.result.current.mutateAsync({ handleId: kept.handleId, title: "server" }));
		const close = renderHook(() => useCloseShellTerminal(), { wrapper: wrapper(queryClient) });
		await act(async () => close.result.current.mutateAsync(closed.handleId));

		// A restart: a fresh renderer reads the tabs back from storage.
		reloadCloudShellTerminalsForTest();
		const restarted = new QueryClient({ defaultOptions: { queries: { retry: false } } });
		const listed = renderHook(() => useShellTerminals(), { wrapper: wrapper(restarted) });
		await waitFor(() => expect(listed.result.current.data).toEqual([{ ...kept, title: "server" }]));

		await act(async () => close.result.current.mutateAsync(kept.handleId));
		reloadCloudShellTerminalsForTest();
		expect(JSON.parse(window.localStorage.getItem("ao.cloudShellTerminals") ?? "null")).toEqual([]);
	});

	it("ignores stored entries that are not Cloud shells", async () => {
		window.localStorage.setItem("ao.cloudShellTerminals", JSON.stringify([
			{ handleId: "ptyhost-v1:shellterm-one", sessionId: "s", title: "t", workingDir: "/", createdAt: "2026-08-27T00:00:00Z", cloud: { orgId: "o" } },
			{ handleId: "cloud-shell-1", title: "no session", workingDir: "/", createdAt: "2026-08-27T00:00:00Z", cloud: { orgId: "o" } },
			"garbage",
		]));
		reloadCloudShellTerminalsForTest();
		getMock.mockResolvedValue({ data: { shellTerminals: [] } });
		const listed = renderHook(() => useShellTerminals(), { wrapper: wrapper(new QueryClient({ defaultOptions: { queries: { retry: false } } })) });
		await waitFor(() => expect(listed.result.current.data).toEqual([]));
		window.localStorage.removeItem("ao.cloudShellTerminals");
		reloadCloudShellTerminalsForTest();
	});
});

describe("useOpenShellTerminal initial input", () => {
	it("queues a cloud shell's input before the shell is listed, so its terminal always finds it", async () => {
		const queryClient = new QueryClient({
			defaultOptions: { mutations: { retry: false }, queries: { retry: false } },
		});
		queryClient.setQueryData(shellTerminalsQueryKey, []);
		// What a terminal connecting to the shell the moment it is listed would find.
		const foundOnListing = new Map<string, string | undefined>();
		const unsubscribe = queryClient.getQueryCache().subscribe(() => {
			for (const shell of queryClient.getQueryData<ShellTerminal[]>(shellTerminalsQueryKey) ?? []) {
				if (!shell.optimistic && !foundOnListing.has(shell.handleId)) foundOnListing.set(shell.handleId, takeCloudShellInput(shell.handleId));
			}
		});
		const open = renderHook(() => useOpenShellTerminal(), { wrapper: wrapper(queryClient) });

		const shell = (await act(async () =>
			open.result.current.mutateAsync({
				projectId: "cloud-project",
				sessionId: "cloud-session",
				cloud: { orgId: "cloud-org" },
				initialInput: "npm test\r",
			}),
		))!;
		unsubscribe();
		try {
			expect(foundOnListing.get(shell.handleId)).toBe("npm test\r");
			expect(takeCloudShellInput(shell.handleId)).toBeUndefined();
		} finally {
			const close = renderHook(() => useCloseShellTerminal(), { wrapper: wrapper(queryClient) });
			await act(async () => close.result.current.mutateAsync(shell.handleId));
		}
	});
});

describe("useRenameShellTerminal", () => {
	it("updates the visible tab title before the daemon responds", async () => {
		let finishRename!: (result: { data: { shellTerminal: ShellTerminal } }) => void;
		patchMock.mockReturnValue(new Promise((resolve) => (finishRename = resolve)));
		const queryClient = queryClientWithShells();
		const { result } = renderHook(() => useRenameShellTerminal(), { wrapper: wrapper(queryClient) });

		act(() => result.current.mutate({ handleId: shells[0].handleId, title: "server — zsh" }));

		await waitFor(() =>
			expect(queryClient.getQueryData<ShellTerminal[]>(shellTerminalsQueryKey)?.[0]?.title).toBe("server — zsh"),
		);
		act(() => finishRename({ data: { shellTerminal: { ...shells[0], title: "server — zsh" } } }));
		await waitFor(() => expect(result.current.isPending).toBe(false));
	});

	type Deferred<T> = { promise: Promise<T>; resolve: (value: T) => void; reject: (error: unknown) => void };
	const deferred = <T,>(): Deferred<T> => {
		let resolve!: (value: T) => void;
		let reject!: (error: unknown) => void;
		const promise = new Promise<T>((res, rej) => { resolve = res; reject = rej; });
		return { promise, resolve, reject };
	};
	// A program set the first tab's title ("vim"); a given name hides it.
	const titled = [{ ...shells[0], terminalTitle: "vim" }, shells[1]];
	/** The daemon's list, read back after each rename settles. */
	let stored = titled;
	const renderTabs = () => {
		const queryClient = new QueryClient({ defaultOptions: { mutations: { retry: false }, queries: { retry: false } } });
		queryClient.setQueryData(shellTerminalsQueryKey, titled);
		const hook = renderHook(() => ({ list: useShellTerminals(), rename: useRenameShellTerminal() }), { wrapper: wrapper(queryClient) });
		renderedRename = (title) => hook.result.current.rename.mutate({ handleId: shells[0].handleId, title });
		return { queryClient, hook, first: () => hook.result.current.list.data?.[0] };
	};
	const answerWith = (title: string) => ({ data: { shellTerminal: { ...shells[0], title } } });

	beforeEach(() => {
		stored = titled;
		getMock.mockImplementation(async () => ({ data: { shellTerminals: stored } }));
	});

	it("shows the new name, not the program's title, in the commit that closes the editor", async () => {
		patchMock.mockReturnValue(new Promise(() => {}));
		const { first } = renderTabs();
		await waitFor(() => expect(getMock).toHaveBeenCalled());
		expect(first()).toMatchObject({ title: "one", terminalTitle: "vim" });

		// TanStack delivers cache writes on setTimeout(0); hold it.
		vi.useFakeTimers();
		try {
			act(() => renderedRename("server"));
			expect(first()?.title).toBe("server");
			expect(first()?.terminalTitle).toBeUndefined();
		} finally {
			vi.useRealTimers();
		}
	});

	it("keeps the new name through a slow answer and a read that still has the old one", async () => {
		const answer = deferred<ReturnType<typeof answerWith>>();
		patchMock.mockReturnValue(answer.promise);
		const { first, queryClient } = renderTabs();
		await waitFor(() => expect(getMock).toHaveBeenCalled());

		act(() => renderedRename("server"));
		await act(async () => { await queryClient.refetchQueries({ queryKey: shellTerminalsQueryKey }); });
		expect(first()).toMatchObject({ title: "server" });
		expect(first()?.terminalTitle).toBeUndefined();

		stored = [{ ...shells[0], title: "server" }, shells[1]];
		await act(async () => answer.resolve(answerWith("server")));
		await waitFor(() => expect(first()?.title).toBe("server"));
	});

	it("sends a newer name after the older request answers, which never replaces it", async () => {
		const older = deferred<ReturnType<typeof answerWith>>();
		const newer = deferred<ReturnType<typeof answerWith>>();
		patchMock.mockReturnValueOnce(older.promise).mockReturnValueOnce(newer.promise);
		const { first } = renderTabs();
		await waitFor(() => expect(getMock).toHaveBeenCalled());

		act(() => renderedRename("server"));
		act(() => renderedRename("logs"));
		// Hold the reads after each answer, so only the answers decide what shows.
		getMock.mockReturnValue(new Promise(() => {}));
		// The daemon stores renames in arrival order, so the newer one waits.
		await waitFor(() => expect(patchMock).toHaveBeenCalledTimes(1));
		await act(async () => undefined);
		expect(patchMock).toHaveBeenCalledTimes(1);
		await act(async () => older.resolve(answerWith("server")));
		await waitFor(() => expect(patchMock).toHaveBeenCalledTimes(2));
		expect(patchMock).toHaveBeenLastCalledWith("/api/v1/shell-terminals/{handleId}", expect.objectContaining({ body: { title: "logs" } }));
		expect(first()?.title).toBe("logs");
		await act(async () => newer.resolve(answerWith("logs")));
		await act(async () => undefined);
		expect(first()?.title).toBe("logs");
	});

	it("rolls a failed rename back to the program's title and says so", async () => {
		patchMock.mockRejectedValue(new Error("disk full"));
		const { first } = renderTabs();
		await waitFor(() => expect(getMock).toHaveBeenCalled());

		act(() => renderedRename("server"));
		await waitFor(() => expect(first()).toMatchObject({ title: "one", terminalTitle: "vim" }));
		expect(useUiStore.getState().globalToasts).toEqual([
			expect.objectContaining({ title: "Couldn't rename to server", body: "disk full", tone: "error" }),
		]);
	});

	it("rolls a failure back to the stored name, not an old snapshot, after a quiet spell", async () => {
		const { first } = renderTabs();
		await waitFor(() => expect(getMock).toHaveBeenCalled());
		stored = [{ ...shells[0], title: "server" }, shells[1]];
		patchMock.mockResolvedValueOnce(answerWith("server"));
		act(() => renderedRename("server"));
		await waitFor(() => expect(patchMock).toHaveBeenCalledTimes(1));
		await waitFor(() => expect(first()).toMatchObject({ title: "server" }));

		// Minutes pass with no list read, so nothing prunes the settled rename.
		const later = Date.now() + 10 * 60_000;
		const now = vi.spyOn(Date, "now").mockReturnValue(later);
		try {
			getMock.mockReturnValue(new Promise(() => {}));
			patchMock.mockRejectedValueOnce(new Error("disk full"));
			act(() => renderedRename("logs"));
			await waitFor(() => expect(useUiStore.getState().globalToasts).toHaveLength(1));
			expect(first()?.title).toBe("server");
			expect(first()?.terminalTitle).toBeUndefined();
		} finally {
			now.mockRestore();
		}
	});

	let renderedRename: (title: string) => void = () => {};
	beforeEach(() => {
		renderedRename = () => { throw new Error("render the tabs first"); };
	});
});

describe("useCloseShellTerminal", () => {
	it("removes the terminal tab before an in-flight list request finishes cancelling", async () => {
		let finishCancel!: () => void;
		let finishDelete!: (result: { error?: unknown }) => void;
		deleteMock.mockReturnValue(new Promise((resolve) => (finishDelete = resolve)));
		const queryClient = queryClientWithShells();
		vi.spyOn(queryClient, "cancelQueries").mockReturnValue(
			new Promise<void>((resolve) => {
				finishCancel = resolve;
			}),
		);
		const { result } = renderHook(() => useCloseShellTerminal(), { wrapper: wrapper(queryClient) });

		act(() => result.current.mutate(shells[0].handleId));

		await waitFor(() => expect(queryClient.getQueryData(shellTerminalsQueryKey)).toEqual([shells[1]]));
		expect(deleteMock).not.toHaveBeenCalled();
		expect(result.current.isPending).toBe(true);

		act(() => finishCancel());
		await waitFor(() => expect(deleteMock).toHaveBeenCalled());
		act(() => finishDelete({}));
		await waitFor(() => expect(result.current.isPending).toBe(false));
	});

	it("keeps a closing tab hidden when another close's refetch still lists it", async () => {
		// Two tabs closed in quick succession: the first delete settles and
		// refetches the list before the daemon has processed the second delete.
		const finishDeletes = new Map<string, (result: { error?: unknown }) => void>();
		deleteMock.mockImplementation(
			(_path: string, options: { params: { path: { handleId: string } } }) =>
				new Promise((resolve) => finishDeletes.set(options.params.path.handleId, resolve)),
		);
		getMock.mockResolvedValue({ data: { shellTerminals: [shells[1]] } });
		const queryClient = queryClientWithShells();
		const { result } = renderHook(
			() => ({ list: useShellTerminals(), close: useCloseShellTerminal() }),
			{ wrapper: wrapper(queryClient) },
		);

		act(() => result.current.close.mutate(shells[0].handleId));
		act(() => result.current.close.mutate(shells[1].handleId));
		await waitFor(() => expect(finishDeletes.size).toBe(2));
		expect(queryClient.getQueryData(shellTerminalsQueryKey)).toEqual([]);

		act(() => finishDeletes.get(shells[0].handleId)?.({}));
		await waitFor(() => expect(getMock).toHaveBeenCalled());
		await act(async () => undefined);
		expect(queryClient.getQueryData(shellTerminalsQueryKey)).toEqual([]);

		getMock.mockResolvedValue({ data: { shellTerminals: [] } });
		act(() => finishDeletes.get(shells[1].handleId)?.({}));
		await waitFor(() => expect(result.current.close.isPending).toBe(false));
		expect(queryClient.getQueryData(shellTerminalsQueryKey)).toEqual([]);
	});

	it("restores an optimistically removed tab when a live PTY fails to close", async () => {
		let finishDelete!: (result: { error: unknown }) => void;
		deleteMock.mockReturnValue(new Promise((resolve) => (finishDelete = resolve)));
		const queryClient = queryClientWithShells();
		const { result } = renderHook(() => useCloseShellTerminal(), { wrapper: wrapper(queryClient) });

		act(() => result.current.mutate(shells[0].handleId));
		await waitFor(() => expect(queryClient.getQueryData(shellTerminalsQueryKey)).toEqual([shells[1]]));

		act(() => finishDelete({ error: { code: "SHELL_TERMINAL_CLOSE_FAILED" } }));
		await waitFor(() => expect(queryClient.getQueryData(shellTerminalsQueryKey)).toEqual(shells));
	});

	it("does not restore a stale tab when the daemon reports that its PTY is already gone", async () => {
		deleteMock.mockResolvedValue({ error: { code: "SHELL_TERMINAL_NOT_FOUND" } });
		const queryClient = queryClientWithShells();
		const { result } = renderHook(() => useCloseShellTerminal(), { wrapper: wrapper(queryClient) });

		await expect(result.current.mutateAsync(shells[0].handleId)).resolves.toBeUndefined();
		expect(queryClient.getQueryData(shellTerminalsQueryKey)).toEqual([shells[1]]);
	});
});

describe("tabs opened while their shell is being created", () => {
	const created = { ...shells[1], handleId: "ptyhost-v1:shellterm-new", title: "Terminal 3" };

	function deferredPost() {
		let finishPost!: (result: { data: { shellTerminal: ShellTerminal } }) => void;
		postMock.mockReturnValue(new Promise((resolve) => (finishPost = resolve)));
		return (shell: ShellTerminal) => finishPost({ data: { shellTerminal: shell } });
	}

	it("keeps a pending tab when a list refetch lands before its create request returns", async () => {
		const finishPost = deferredPost();
		getMock.mockResolvedValue({ data: { shellTerminals: shells } });
		const queryClient = queryClientWithShells();
		renderHook(() => useShellTerminals(), { wrapper: wrapper(queryClient) });
		const { result } = renderHook(() => useOpenShellTerminal(), { wrapper: wrapper(queryClient) });

		let pending!: ShellTerminal;
		act(() => { pending = result.current.open({}); });
		await act(async () => queryClient.refetchQueries({ queryKey: shellTerminalsQueryKey }));
		expect(queryClient.getQueryData(shellTerminalsQueryKey)).toEqual([...shells, pending]);

		getMock.mockResolvedValue({ data: { shellTerminals: [...shells, created] } });
		act(() => finishPost(created));
		await waitFor(() => expect(queryClient.getQueryData(shellTerminalsQueryKey)).toEqual([...shells, created]));
	});

	it("does not leave a duplicate tab when a refetch already lists the created shell", async () => {
		const finishPost = deferredPost();
		getMock.mockResolvedValue({ data: { shellTerminals: [...shells, created] } });
		const queryClient = queryClientWithShells();
		renderHook(() => useShellTerminals(), { wrapper: wrapper(queryClient) });
		const { result } = renderHook(() => useOpenShellTerminal(), { wrapper: wrapper(queryClient) });

		let pending!: ShellTerminal;
		act(() => { pending = result.current.open({}); });
		await act(async () => queryClient.refetchQueries({ queryKey: shellTerminalsQueryKey }));
		expect(queryClient.getQueryData(shellTerminalsQueryKey)).toEqual([...shells, created, pending]);

		// The pending tab goes as soon as the response arrives, not on the next refetch.
		getMock.mockReturnValue(new Promise(() => {}));
		await act(async () => finishPost(created));
		expect(queryClient.getQueryData(shellTerminalsQueryKey)).toEqual([...shells, created]);
	});

	it("records which shell every tab opened in quick succession became", async () => {
		const first = { ...created, handleId: "ptyhost-v1:shellterm-a", title: "Terminal 3" };
		const second = { ...created, handleId: "ptyhost-v1:shellterm-b", title: "Terminal 4" };
		postMock.mockResolvedValueOnce({ data: { shellTerminal: first } }).mockResolvedValueOnce({ data: { shellTerminal: second } });
		getMock.mockResolvedValue({ data: { shellTerminals: [...shells, first, second] } });
		const queryClient = queryClientWithShells();
		const { result } = renderHook(() => useOpenShellTerminal(), { wrapper: wrapper(queryClient) });
		let pendingFirst!: ShellTerminal;
		let pendingSecond!: ShellTerminal;
		act(() => {
			pendingFirst = result.current.open({});
			pendingSecond = result.current.open({});
		});

		// UI that recorded either pending tab can find the shell it became.
		await waitFor(() => expect(adoptedShellHandle(pendingSecond.handleId)).toBe(second.handleId));
		expect(adoptedShellHandle(pendingFirst.handleId)).toBe(first.handleId);
	});

	it("destroys the shell of a tab closed before its create request returned", async () => {
		const finishPost = deferredPost();
		let finishDelete!: (result: { error?: unknown }) => void;
		deleteMock.mockReturnValue(new Promise((resolve) => (finishDelete = resolve)));
		getMock.mockResolvedValue({ data: { shellTerminals: shells } });
		const queryClient = queryClientWithShells();
		renderHook(() => useShellTerminals(), { wrapper: wrapper(queryClient) });
		const open = renderHook(() => useOpenShellTerminal(), { wrapper: wrapper(queryClient) });
		const close = renderHook(() => useCloseShellTerminal(), { wrapper: wrapper(queryClient) });
		let pending!: ShellTerminal;
		act(() => { pending = open.result.current.open({}); });
		await act(async () => close.result.current.mutateAsync(pending.handleId));
		// Nothing to close yet: the daemon has not returned the shell.
		expect(deleteMock).not.toHaveBeenCalled();
		expect(queryClient.getQueryData(shellTerminalsQueryKey)).toEqual(shells);

		getMock.mockResolvedValue({ data: { shellTerminals: [...shells, created] } });
		act(() => finishPost(created));
		await waitFor(() => expect(deleteMock).toHaveBeenCalledWith("/api/v1/shell-terminals/{handleId}", {
			params: { path: { handleId: created.handleId } },
		}));
		// A refetch while the shell is being destroyed does not show it.
		await act(async () => queryClient.refetchQueries({ queryKey: shellTerminalsQueryKey }));
		expect(queryClient.getQueryData(shellTerminalsQueryKey)).toEqual(shells);

		getMock.mockResolvedValue({ data: { shellTerminals: shells } });
		act(() => finishDelete({}));
		await waitFor(() => expect(open.result.current.isPending).toBe(false));
		expect(queryClient.getQueryData(shellTerminalsQueryKey)).toEqual(shells);
		expect(adoptedShellHandle(pending.handleId)).toBeUndefined();
	});
});

describe("ended shell terminals", () => {
	it("keeps the ended fields the daemon reports", () => {
		expect(
			toShellTerminal({
				createdAt: "2026-11-09T09:00:00Z",
				endedAt: "2026-11-10T20:44:00Z",
				handleId: "shellterm-ended",
				resumeHarness: "claude-code",
				sessionId: "session-1",
				title: "Build",
				workingDir: "/worktree",
			}),
		).toMatchObject({ endedAt: "2026-11-10T20:44:00Z", resumeHarness: "claude-code", title: "Build" });
		expect(toShellTerminal({ createdAt: "x", handleId: "live", title: "Terminal 1", workingDir: "/" })).not.toHaveProperty("endedAt");
	});

	it("reopens only on request and hands the tab to the new shell", async () => {
		const ended: ShellTerminal = { ...shells[0], endedAt: "2026-11-10T20:44:00Z", resumeHarness: "codex" };
		const queryClient = queryClientWithShells();
		queryClient.setQueryData(shellTerminalsQueryKey, [ended, shells[1]]);
		getMock.mockResolvedValue({ data: { shellTerminals: [] } });
		postMock.mockResolvedValue({
			data: { shellTerminal: { createdAt: ended.createdAt, handleId: "shellterm-new", title: "one", workingDir: "/tmp" } },
		});
		const { result } = renderHook(() => useReopenShellTerminal(), { wrapper: wrapper(queryClient) });
		expect(postMock).not.toHaveBeenCalled();

		await act(async () => {
			await result.current.mutateAsync({ handleId: ended.handleId, resumeAgent: true });
		});

		expect(postMock).toHaveBeenCalledWith("/api/v1/shell-terminals/{handleId}/reopen", {
			params: { path: { handleId: ended.handleId } },
			body: expect.objectContaining({ resumeAgent: true, shell: "auto" }),
		});
		expect(adoptedShellHandle(ended.handleId)).toBe("shellterm-new");
		expect(useShellAgentStore.getState().agents["shellterm-new"]).toEqual({ harness: "codex", state: "idle" });
	});
});

describe("closing an ended tab while it reopens", () => {
	const ended: ShellTerminal = { ...shells[0], handleId: "ptyhost-v1:shellterm-closed-while-reopening", endedAt: "2026-11-10T20:44:00Z" };
	const replacement = { createdAt: ended.createdAt, handleId: "ptyhost-v1:shellterm-replacement", title: "one", workingDir: "/tmp" };

	function setup() {
		let finishPost!: (result: { data: { shellTerminal: typeof replacement } }) => void;
		postMock.mockReturnValue(new Promise((resolve) => (finishPost = resolve)));
		const deletes: Array<{ handleId: string; finish: (result: object) => void }> = [];
		deleteMock.mockImplementation((_path: string, init: { params: { path: { handleId: string } } }) =>
			new Promise((resolve) => deletes.push({ handleId: init.params.path.handleId, finish: resolve })),
		);
		getMock.mockResolvedValue({ data: { shellTerminals: [] } });
		const queryClient = queryClientWithShells();
		queryClient.setQueryData(shellTerminalsQueryKey, [ended, shells[1]]);
		const { result } = renderHook(
			() => ({ reopen: useReopenShellTerminal(), close: useCloseShellTerminal() }),
			{ wrapper: wrapper(queryClient) },
		);
		return { result, queryClient, deletes, finishPost: () => finishPost({ data: { shellTerminal: replacement } }) };
	}

	async function expectReplacementDestroyed(deletes: Array<{ handleId: string; finish: (result: object) => void }>, queryClient: QueryClient) {
		await waitFor(() => expect(deletes.map((d) => d.handleId)).toContain(replacement.handleId));
		deletes.find((d) => d.handleId === replacement.handleId)?.finish({});
		await waitFor(() => expect(adoptedShellHandle(ended.handleId)).toBeUndefined());
		const listed = queryClient.getQueryData<ShellTerminal[]>(shellTerminalsQueryKey) ?? [];
		expect(listed.map((shell) => shell.handleId)).not.toContain(replacement.handleId);
		expect(listed.map((shell) => shell.handleId)).not.toContain(ended.handleId);
	}

	it("destroys the replacement when the close answers first", async () => {
		const { result, queryClient, deletes, finishPost } = setup();
		act(() => {
			result.current.reopen.mutate({ handleId: ended.handleId });
		});
		await waitFor(() => expect(postMock).toHaveBeenCalled());
		act(() => {
			result.current.close.mutate(ended.handleId);
		});
		await waitFor(() => expect(deletes).toHaveLength(1));
		deletes[0].finish({ error: { code: "SHELL_TERMINAL_NOT_FOUND" } });
		await waitFor(() => expect(result.current.close.isSuccess).toBe(true));
		finishPost();
		await expectReplacementDestroyed(deletes, queryClient);
	});

	it("destroys the replacement when the reopen answers first", async () => {
		const { result, queryClient, deletes, finishPost } = setup();
		act(() => {
			result.current.reopen.mutate({ handleId: ended.handleId });
		});
		await waitFor(() => expect(postMock).toHaveBeenCalled());
		act(() => {
			result.current.close.mutate(ended.handleId);
		});
		await waitFor(() => expect(deletes).toHaveLength(1));
		finishPost();
		await expectReplacementDestroyed(deletes, queryClient);
		deletes[0].finish({ error: { code: "SHELL_TERMINAL_NOT_FOUND" } });
		await waitFor(() => expect(result.current.close.isSuccess).toBe(true));
	});
});
