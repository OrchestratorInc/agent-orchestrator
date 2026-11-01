// Standalone shell terminals: shells the user opens by hand from the topbar or
// ⌘T / Ctrl+T, with no agent session behind them. They are deliberately kept out of
// the workspaces query — they are not sessions, never appear on the board, and
// must not invalidate session state when they come and go.

import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { markTerminalHandleFresh } from "../lib/fresh-terminal-handles";
import type { components } from "../../api/schema";
import { apiErrorCode, hasTrustedApiBaseUrl } from "../lib/api-client";
import { clientForSessionHost } from "../lib/host-clients";
import { LOCAL_HOST, type HostId } from "../lib/hosts";
import { mockShellTerminals } from "../lib/mock-data";
import { isWindowsPlatform } from "../lib/platform";
import { terminalShellRequestValue, useTerminalShellStore } from "../stores/terminal-shell-store";
import { useCloudCp } from "./useCloudCp";

export type ShellTerminal = {
	/** Runtime handle the terminal mux attaches to, exactly like a session pane's. */
	handleId: string;
	projectId?: string;
	/** Agent session this shell is scoped to; absent for standalone shells. */
	sessionId?: string;
	workingDir: string;
	title: string;
	createdAt: string;
	/** Owning daemon; absent for local and cloud shells. */
	hostId?: HostId;
	/** Present when the shell lives in a control-plane sandbox, not the local daemon. */
	cloud?: { orgId: string };
	/**
	 * Exists only in the renderer while the daemon is creating the PTY. It lets
	 * the tab strip respond to the click immediately without ever attempting to
	 * attach xterm to a handle that does not exist yet.
	 */
	optimistic?: true;
};

export const shellTerminalsQueryKey = ["shell-terminals"] as const;
export const shellTerminalsQueryKeyForHost = (hostId?: HostId) =>
	hostId && hostId !== LOCAL_HOST ? ["remote-shell-terminals", hostId] as const : shellTerminalsQueryKey;
const usePreviewData = import.meta.env.VITE_NO_ELECTRON === "1";

function isLegacyDirectoryTitle(title: string, workingDir: string): boolean {
	const parts = workingDir.split(/[\\/]/).filter(Boolean);
	return parts.at(-1) === title;
}

export function toShellTerminal(t: components["schemas"]["ShellTerminalResponse"], hostId?: HostId): ShellTerminal {
	const title = isLegacyDirectoryTitle(t.title, t.workingDir) ? "Terminal" : t.title;
	return {
		handleId: t.handleId,
		projectId: t.projectId,
		sessionId: t.sessionId,
		workingDir: t.workingDir,
		// Shell tabs used to be named after their initial directory. Normalize
		// those persisted legacy labels so existing tabs adopt the new idle state.
		title: title === "Terminal" ? "Terminal 1" : title,
		createdAt: t.createdAt,
		...(hostId && hostId !== LOCAL_HOST ? { hostId } : {}),
	};
}

// Preview-only shell list. The browser build has no daemon to spawn a PTY, so
// open/close mutate this array instead — keeping the tab strip fully
// interactive (open, select, close) without a backend, which is what the e2e
// suite drives.
let previewShellTerminals: ShellTerminal[] = [...mockShellTerminals];
let previewShellSeq = 0;
// Cloud workspace shells are connection-scoped rather than daemon-owned. Keep
// their tab metadata for this Electron renderer lifetime; the terminal itself
// is created when its ticketed control-plane WebSocket connects.
let cloudShellTerminals: ShellTerminal[] = [];
// Shells whose close is still in flight, per host. Closing several tabs in a
// row refetches the list as each close settles, and the daemon can answer one
// refetch before it has processed the other pending deletes; without this, a
// tab the user already closed would reappear until its own close settles.
const closingShellHandles = new Map<string, Set<string>>();

const PENDING_SHELL_PREFIX = "pending-shell:";
// Tabs shown while their create request runs. A list refetch that lands in
// that window (another tab opening or closing invalidates the list) must keep
// them, or the tab vanishes and reappears.
const pendingShells = new Map<string, ShellTerminal>();
// Pending tabs the user closed before their create request returned. The shell
// the request creates is destroyed instead of being shown.
const cancelledPendingShells = new Set<string>();
// Shells created for a cancelled tab, hidden from list refetches until the
// daemon has destroyed them.
const discardingShells = new Set<string>();

// The shell each pending tab became, so UI that recorded a pending tab (its
// selection, its place in a reordered strip) carries that over to the shell.
// UI must follow this rather than a one-off callback: a selection can still be
// made on the pending tab from a render that has not seen its shell arrive.
const adoptedShellHandles = new Map<string, string>();

/** The shell handle a pending tab's handle became, once its create returned. */
export function adoptedShellHandle(handleId: string): string | undefined {
	return adoptedShellHandles.get(handleId);
}

const isPendingShellHandle = (handleId: string) => handleId.startsWith(PENDING_SHELL_PREFIX);
const hostKey = (hostId?: HostId) => (hostId && hostId !== LOCAL_HOST ? hostId : LOCAL_HOST);

function trackPendingShell(shell: ShellTerminal) {
	if (!cancelledPendingShells.has(shell.handleId)) pendingShells.set(shell.handleId, shell);
}

function settlePendingShell(handleId: string) {
	pendingShells.delete(handleId);
	cancelledPendingShells.delete(handleId);
}

async function fetchShellTerminals(hostId?: HostId): Promise<ShellTerminal[]> {
	let listed = await fetchListedShellTerminals(hostId);
	if (discardingShells.size) listed = listed.filter((shell) => !discardingShells.has(shell.handleId));
	const pending = [...pendingShells.values()].filter((shell) => hostKey(shell.hostId) === hostKey(hostId));
	return pending.length ? [...listed, ...pending] : listed;
}

async function fetchListedShellTerminals(hostId?: HostId): Promise<ShellTerminal[]> {
	const remote = Boolean(hostId && hostId !== LOCAL_HOST);
	if (usePreviewData && !remote) {
		return previewShellTerminals;
	}
	if (!remote && !hasTrustedApiBaseUrl()) {
		return [];
	}
	const { data, error } = await clientForSessionHost(hostId).GET("/api/v1/shell-terminals");
	if (error) throw error;
	const closing = closingShellHandles.get(hostKey(hostId));
	return [
		...(data?.shellTerminals ?? [])
			.filter((terminal) => !closing?.has(terminal.handleId))
			.map((terminal) => toShellTerminal(terminal, hostId)),
		...(remote ? [] : cloudShellTerminals),
	];
}

// No refetchInterval: shell terminals only change when this client opens or
// closes one, and both mutations invalidate the query. Polling would spend a
// liveness probe per shell per interval for no new information.
export const shellTerminalsQueryOptions = {
	queryKey: shellTerminalsQueryKey,
	queryFn: () => fetchShellTerminals(),
	retry: 1,
};

export function useShellTerminals(hostId?: HostId) {
	return useQuery(hostId && hostId !== LOCAL_HOST ? {
		...shellTerminalsQueryOptions,
		queryKey: shellTerminalsQueryKeyForHost(hostId),
		queryFn: () => fetchShellTerminals(hostId),
	} : shellTerminalsQueryOptions);
}

export type OpenShellTerminalInput = {
	projectId?: string;
	sessionId?: string;
	shell?: string;
	cloud?: { orgId: string };
};

function nextCloudShellTitle(terminals: ShellTerminal[], sessionId: string): string {
	const count = terminals.filter((terminal) => terminal.cloud && terminal.sessionId === sessionId).length;
	return `Terminal ${count + 1}`;
}

type OpenShellTerminalMutationInput = OpenShellTerminalInput & {
	optimisticShell?: ShellTerminal;
	/** The create request open() already started; the mutation adopts it. */
	request?: Promise<ShellTerminal>;
};

/** Destroys a shell this renderer owns: daemon, cloud, or preview. */
async function destroyShellTerminal(handleId: string, hostId?: HostId): Promise<void> {
	const remote = Boolean(hostId && hostId !== LOCAL_HOST);
	if (!remote && cloudShellTerminals.some((shell) => shell.handleId === handleId)) {
		cloudShellTerminals = cloudShellTerminals.filter((shell) => shell.handleId !== handleId);
		return;
	}
	await closeShellTerminal(handleId, hostId);
}

function nextShellTerminalTitle(terminals: ShellTerminal[]): string {
	let maxNumber = 0;
	for (const terminal of terminals) {
		if (terminal.title === "Terminal") {
			maxNumber = Math.max(maxNumber, 1);
			continue;
		}
		const match = /^Terminal (\d+)$/.exec(terminal.title);
		if (match) maxNumber = Math.max(maxNumber, Number(match[1]));
	}
	return `Terminal ${maxNumber + 1}`;
}

function createOptimisticShellTerminal(
	{ projectId, sessionId }: OpenShellTerminalInput,
	terminals: ShellTerminal[],
	hostId?: HostId,
): ShellTerminal {
	const id = globalThis.crypto?.randomUUID?.() ?? `${Date.now()}-${Math.random().toString(36).slice(2)}`;
	return {
		handleId: `${PENDING_SHELL_PREFIX}${id}`,
		projectId,
		sessionId,
		workingDir: "",
		title: nextShellTerminalTitle(terminals),
		createdAt: new Date().toISOString(),
		...(hostId && hostId !== LOCAL_HOST ? { hostId } : {}),
		optimistic: true,
	};
}

function addOptimisticShell(queryClient: ReturnType<typeof useQueryClient>, queryKey: ReturnType<typeof shellTerminalsQueryKeyForHost>, shell: ShellTerminal) {
	queryClient.setQueryData<ShellTerminal[]>(queryKey, (current) =>
		current?.some((candidate) => candidate.handleId === shell.handleId) ? current : [...(current ?? []), shell],
	);
}

/**
 * Opens a shell in the given project's root (or the daemon data dir when
 * omitted). When sessionId is set the shell is scoped to that session and only
 * appears in its tab strip; otherwise it is a standalone shell on /terminals.
 */
export function useOpenShellTerminal(hostId?: HostId) {
	const queryClient = useQueryClient();
	const queryKey = shellTerminalsQueryKeyForHost(hostId);
	const remote = Boolean(hostId && hostId !== LOCAL_HOST);
	const { client: cloudCpClient } = useCloudCp();
	const createShell = async ({
		projectId,
		sessionId,
		shell,
		cloud,
		optimisticShell,
	}: OpenShellTerminalMutationInput): Promise<ShellTerminal> => {
		if (usePreviewData && !remote) {
			previewShellSeq += 1;
			const shell: ShellTerminal = {
				handleId: `shellterm-preview-${previewShellSeq}`,
				projectId,
				sessionId,
				workingDir: `/Users/demo/Projects/${projectId ?? "ao"}`,
				title: optimisticShell?.title ?? `Terminal ${previewShellSeq}`,
				createdAt: new Date().toISOString(),
			};
			previewShellTerminals = [...previewShellTerminals, shell];
			return shell;
		}
		if (cloud) {
			if (!sessionId) throw new Error("A cloud shell terminal must belong to a session");
			// Explicitly resume the cloud session before opening its shell, so a
			// paused sandbox is woken rather than the shell attaching to nothing.
			await cloudCpClient.resumeSession(cloud.orgId, sessionId);
			const current = queryClient.getQueryData<ShellTerminal[]>(queryKey) ?? [];
			const shell: ShellTerminal = {
				handleId: `cloud-shell-${crypto.randomUUID()}`,
				projectId,
				sessionId,
				workingDir: "/workspace/repository",
				title: nextCloudShellTitle(current, sessionId),
				createdAt: new Date().toISOString(),
				cloud,
			};
			cloudShellTerminals = [...cloudShellTerminals, shell];
			return shell;
		}
		// This renderer attaches every shell it opens with its measured grid, so
		// the daemon starts the shell then: its first prompt is laid out for the
		// width this tab shows instead of a guessed default.
		const body: components["schemas"]["OpenShellTerminalRequest"] = { startOnAttach: true };
		// The tab already shows this name; the daemon numbering it again could
		// disagree while tabs the user just closed are still being destroyed.
		if (optimisticShell) body.title = optimisticShell.title;
		if (projectId) body.projectId = projectId;
		if (sessionId) body.sessionId = sessionId;
		if (remote && shell) body.shell = shell;
		if (!remote && isWindowsPlatform()) {
			await useTerminalShellStore.getState().load();
			body.shell = shell ?? terminalShellRequestValue(useTerminalShellStore.getState().preference);
		}
		const { data, error } = await clientForSessionHost(hostId).POST("/api/v1/shell-terminals", { body });
		if (error) throw error;
		if (!data) throw new Error("Daemon returned no shell terminal");
		if (!remote) markTerminalHandleFresh(data.shellTerminal.handleId);
		return toShellTerminal(data.shellTerminal, hostId);
	};

	const mutation = useMutation({
		mutationFn: async (input: OpenShellTerminalMutationInput = {}): Promise<ShellTerminal | null> => {
			const shell = await (input.request ?? createShell(input));
			// The user closed the tab while it was being created: the shell must not
			// appear later, so destroy it rather than adopting it.
			if (input.optimisticShell && cancelledPendingShells.has(input.optimisticShell.handleId)) {
				discardingShells.add(shell.handleId);
				queryClient.setQueryData<ShellTerminal[]>(queryKey, (current) =>
					current?.filter((candidate) => candidate.handleId !== shell.handleId),
				);
				try {
					await destroyShellTerminal(shell.handleId, hostId);
				} finally {
					discardingShells.delete(shell.handleId);
				}
				return null;
			}
			return shell;
		},
		onMutate: (input) => {
			const optimisticShell =
				input.optimisticShell ??
				createOptimisticShellTerminal(input, queryClient.getQueryData<ShellTerminal[]>(queryKey) ?? [], hostId);
			trackPendingShell(optimisticShell);
			addOptimisticShell(queryClient, queryKey, optimisticShell);
			return { optimisticHandleId: optimisticShell.handleId };
		},
		onSuccess: (shell, _input, context) => {
			const optimisticHandleId = context?.optimisticHandleId;
			if (optimisticHandleId) settlePendingShell(optimisticHandleId);
			if (!shell) return;
			if (optimisticHandleId) adoptedShellHandles.set(optimisticHandleId, shell.handleId);
			// Replace, rather than append to, the tab that was visible while the POST
			// ran. This preserves selection and prevents a duplicate tab flash.
			queryClient.setQueryData<ShellTerminal[]>(queryKey, (current) => {
				// A refetch that landed after the daemon created the shell but before
				// this response already lists it next to its pending tab.
				if (current?.some((candidate) => candidate.handleId === shell.handleId)) {
					return current.filter((candidate) => candidate.handleId !== optimisticHandleId);
				}
				const index = current?.findIndex((candidate) => candidate.handleId === optimisticHandleId) ?? -1;
				if (index < 0) return [...(current ?? []), shell];
				return current?.map((candidate, candidateIndex) => (candidateIndex === index ? shell : candidate)) ?? [shell];
			});
			// onSettled refetches the list; a second invalidation here only
			// cancelled and restarted that same request.
		},
		onError: (error, _input, context) => {
			if (context?.optimisticHandleId) settlePendingShell(context.optimisticHandleId);
			queryClient.setQueryData<ShellTerminal[]>(queryKey, (current) =>
				current?.filter((shell) => shell.handleId !== context?.optimisticHandleId),
			);
			console.error("Failed to open shell terminal:", error);
			if (!remote && isWindowsPlatform() && apiErrorCode(error) === "SHELL_TERMINAL_SHELL_UNAVAILABLE") {
				void useTerminalShellStore.getState().setPreference({ kind: "auto" });
			}
		},
		onSettled: (_data, _error, input) => {
			if (!input?.cloud) void queryClient.invalidateQueries({ queryKey });
		},
	});

	// Session topbars need the pending shell synchronously so they can select
	// it in the same click event. Other callers can keep using mutation.mutate;
	// onMutate supplies an optimistic entry for them too. A selection made on
	// the pending tab follows it to its shell through adoptedShellHandle.
	const open = (input: OpenShellTerminalInput = {}) => {
		const optimisticShell = createOptimisticShellTerminal(
			input,
			queryClient.getQueryData<ShellTerminal[]>(queryKey) ?? [],
			hostId,
		);
		// Send the create request before anything renders. The click's state
		// updates render synchronously in a microtask, ahead of the mutation's own
		// async steps, so a request started by mutationFn waited for a full
		// session render. The mutation adopts this request and handles its result.
		const request = createShell({ ...input, optimisticShell });
		request.catch(() => undefined);
		trackPendingShell(optimisticShell);
		addOptimisticShell(queryClient, queryKey, optimisticShell);
		mutation.mutate({ ...input, optimisticShell, request });
		return optimisticShell;
	};

	return { ...mutation, open };
}

/** Closes a shell and destroys its PTY. */
export async function closeShellTerminal(handleId: string, hostId?: HostId): Promise<void> {
	if (usePreviewData && (!hostId || hostId === LOCAL_HOST)) {
		previewShellTerminals = previewShellTerminals.filter((shell) => shell.handleId !== handleId);
		return;
	}
	const { error } = await clientForSessionHost(hostId).DELETE("/api/v1/shell-terminals/{handleId}", {
		params: { path: { handleId } },
	});
	// The desired postcondition is already true when the daemon no longer owns
	// the record. Treat this as confirmed cleanup, not a failed cancellation.
	if (error && apiErrorCode(error) !== "SHELL_TERMINAL_NOT_FOUND") throw error;
}

export function useCloseShellTerminal(hostId?: HostId) {
	const queryClient = useQueryClient();
	const queryKey = shellTerminalsQueryKeyForHost(hostId);
	const remote = Boolean(hostId && hostId !== LOCAL_HOST);
	return useMutation({
		mutationFn: async (handleId: string): Promise<void> => {
			if (usePreviewData && !remote) {
				previewShellTerminals = previewShellTerminals.filter((s) => s.handleId !== handleId);
				return;
			}
			// A tab still being created has no shell to close yet; its create
			// request destroys the shell when it returns (see onMutate).
			if (isPendingShellHandle(handleId)) return;
			await destroyShellTerminal(handleId, hostId);
		},
		onMutate: async (handleId) => {
			if (isPendingShellHandle(handleId) && pendingShells.delete(handleId)) {
				cancelledPendingShells.add(handleId);
			}
			for (const [pending, adopted] of adoptedShellHandles) {
				if (adopted === handleId) adoptedShellHandles.delete(pending);
			}
			const key = hostKey(hostId);
			let closing = closingShellHandles.get(key);
			if (!closing) closingShellHandles.set(key, (closing = new Set()));
			closing.add(handleId);
			const previous = queryClient.getQueryData<ShellTerminal[]>(queryKey);
			const isCloud = Boolean(previous?.find((shell) => shell.handleId === handleId)?.cloud);
			const removeClosedShell = () => {
				queryClient.setQueryData<ShellTerminal[]>(queryKey, (current) =>
					current?.filter((shell) => shell.handleId !== handleId),
				);
			};
			// Remove the pill synchronously. Waiting for cancellation first leaves the
			// closed tab visible for the duration of an in-flight list request.
			removeClosedShell();
			await queryClient.cancelQueries({ queryKey });
			// A request that resolved while cancellation was being scheduled may have
			// restored its stale snapshot; make the optimistic state authoritative.
			removeClosedShell();
			return { previous, isCloud };
		},
		onError: (error, _handleId, context) => {
			// A 404 means the daemon has already removed the shell, so restoring its
			// stale tab would be misleading. Other failures put the tab back so the
			// user can retry instead of losing access to a still-live PTY.
			if (apiErrorCode(error) !== "SHELL_TERMINAL_NOT_FOUND" && context?.previous) {
				queryClient.setQueryData(queryKey, context.previous);
			}
		},
		// Settled, not success: a close that 404s means the daemon already lost
		// the shell, and the stale tab still needs to disappear.
		onSettled: (_data, _error, handleId, context) => {
			const key = hostKey(hostId);
			const closing = closingShellHandles.get(key);
			closing?.delete(handleId);
			if (closing?.size === 0) closingShellHandles.delete(key);
			// A pending tab closed nothing on the daemon; its create request settles
			// the list once the shell it created is destroyed.
			if (!context?.isCloud && !isPendingShellHandle(handleId)) void queryClient.invalidateQueries({ queryKey });
		},
	});
}

export type RenameShellTerminalInput = { handleId: string; title: string };

/** Renames a shell terminal's tab. The new title persists on the daemon. */
export function useRenameShellTerminal(hostId?: HostId) {
	const queryClient = useQueryClient();
	const queryKey = shellTerminalsQueryKeyForHost(hostId);
	const remote = Boolean(hostId && hostId !== LOCAL_HOST);
	return useMutation({
		mutationFn: async ({ handleId, title }: RenameShellTerminalInput): Promise<ShellTerminal> => {
			if (usePreviewData && !remote) {
				previewShellTerminals = previewShellTerminals.map((s) => (s.handleId === handleId ? { ...s, title } : s));
				const shell = previewShellTerminals.find((s) => s.handleId === handleId);
				if (!shell) throw new Error("No such shell terminal");
				return shell;
			}
			const cloudIndex = remote ? -1 : cloudShellTerminals.findIndex((shell) => shell.handleId === handleId);
			if (cloudIndex >= 0) {
				const shell = { ...cloudShellTerminals[cloudIndex], title };
				cloudShellTerminals = cloudShellTerminals.map((candidate, index) =>
					index === cloudIndex ? shell : candidate,
				);
				return shell;
			}
			const { data, error } = await clientForSessionHost(hostId).PATCH("/api/v1/shell-terminals/{handleId}", {
				params: { path: { handleId } },
				body: { title },
			});
			if (error) throw error;
			if (!data) throw new Error("Daemon returned no shell terminal");
			return toShellTerminal(data.shellTerminal, hostId);
		},
		onMutate: async ({ handleId, title }) => {
			await queryClient.cancelQueries({ queryKey });
			const previous = queryClient.getQueryData<ShellTerminal[]>(queryKey);
			queryClient.setQueryData<ShellTerminal[]>(queryKey, (current) =>
				current?.map((shell) => (shell.handleId === handleId ? { ...shell, title } : shell)),
			);
			return { previous };
		},
		onError: (_error, _input, context) => {
			if (context?.previous) queryClient.setQueryData(queryKey, context.previous);
		},
		onSuccess: (shell) => {
			queryClient.setQueryData<ShellTerminal[]>(queryKey, (current) =>
				current?.map((candidate) => (candidate.handleId === shell.handleId ? shell : candidate)),
			);
			if (!shell.cloud) void queryClient.invalidateQueries({ queryKey });
		},
	});
}
