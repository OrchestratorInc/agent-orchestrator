import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { queueCloudShellInput } from "../lib/cloud-shell-input";
import { markTerminalHandleFresh } from "../lib/fresh-terminal-handles";
import { paneTerminalGridHint } from "../lib/terminal-grid-hint";
import type { components } from "../../api/schema";
import { apiErrorCode, hasTrustedApiBaseUrl } from "../lib/api-client";
import { clientForSessionHost } from "../lib/host-clients";
import { LOCAL_HOST, type HostId } from "../lib/hosts";
import { mockShellTerminals } from "../lib/mock-data";
import { terminalShellRequestValue, useTerminalShellStore } from "../stores/terminal-shell-store";
import { useCloudCp } from "./useCloudCp";

export type ShellTerminal = {
	handleId: string;
	projectId?: string;
	sessionId?: string;
	workingDir: string;
	title: string;
	terminalTitle?: string;
	createdAt: string;
	hostId?: HostId;
	cloud?: { orgId: string };
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
		title: title === "Terminal" ? "Terminal 1" : title,
		...(t.terminalTitle ? { terminalTitle: t.terminalTitle } : {}),
		createdAt: t.createdAt,
		...(hostId && hostId !== LOCAL_HOST ? { hostId } : {}),
	};
}

let previewShellTerminals: ShellTerminal[] = [...mockShellTerminals];
let previewShellSeq = 0;
const CLOUD_SHELLS_STORAGE_KEY = "ao.cloudShellTerminals";

function isStoredCloudShell(value: unknown): value is ShellTerminal & { cloud: { orgId: string }; sessionId: string } {
	if (!value || typeof value !== "object") return false;
	const shell = value as Partial<ShellTerminal>;
	return typeof shell.handleId === "string" && shell.handleId.startsWith("cloud-shell-") &&
		typeof shell.sessionId === "string" && typeof shell.title === "string" &&
		typeof shell.workingDir === "string" && typeof shell.createdAt === "string" &&
		typeof shell.cloud?.orgId === "string";
}

function readStoredCloudShells(): ShellTerminal[] {
	try {
		const parsed: unknown = JSON.parse(window.localStorage.getItem(CLOUD_SHELLS_STORAGE_KEY) ?? "[]");
		if (!Array.isArray(parsed)) return [];
		return parsed.filter(isStoredCloudShell).map((shell) => ({
			handleId: shell.handleId,
			...(shell.projectId ? { projectId: shell.projectId } : {}),
			sessionId: shell.sessionId,
			workingDir: shell.workingDir,
			title: shell.title,
			createdAt: shell.createdAt,
			cloud: { orgId: shell.cloud.orgId },
		}));
	} catch {
		return [];
	}
}

let cloudShellTerminals: ShellTerminal[] = readStoredCloudShells();

function setCloudShellTerminals(next: ShellTerminal[]) {
	cloudShellTerminals = next;
	try {
		window.localStorage.setItem(CLOUD_SHELLS_STORAGE_KEY, JSON.stringify(next));
	} catch {
	}
}

export function reloadCloudShellTerminalsForTest() {
	cloudShellTerminals = readStoredCloudShells();
}
const closingShellHandles = new Map<string, Set<string>>();

const PENDING_SHELL_PREFIX = "pending-shell:";
const pendingShells = new Map<string, ShellTerminal>();
const cancelledPendingShells = new Set<string>();
const discardingShells = new Set<string>();

const adoptedShellHandles = new Map<string, string>();

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
	initialInput?: string;
};

function nextCloudShellTitle(terminals: ShellTerminal[], sessionId: string): string {
	const count = terminals.filter((terminal) => terminal.cloud && terminal.sessionId === sessionId).length;
	return `Terminal ${count + 1}`;
}

type OpenShellTerminalMutationInput = OpenShellTerminalInput & {
	optimisticShell?: ShellTerminal;
	request?: Promise<ShellTerminal>;
};

async function destroyShellTerminal(handleId: string, hostId?: HostId): Promise<void> {
	const remote = Boolean(hostId && hostId !== LOCAL_HOST);
	if (!remote && cloudShellTerminals.some((shell) => shell.handleId === handleId)) {
		setCloudShellTerminals(cloudShellTerminals.filter((shell) => shell.handleId !== handleId));
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
		initialInput,
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
			if (initialInput) queueCloudShellInput(shell.handleId, initialInput);
			setCloudShellTerminals([...cloudShellTerminals, shell]);
			return shell;
		}
		const body: components["schemas"]["OpenShellTerminalRequest"] = { startOnAttach: true };
		const grid = paneTerminalGridHint();
		if (grid) {
			body.cols = grid.cols;
			body.rows = grid.rows;
		}
		if (optimisticShell) body.title = optimisticShell.title;
		if (projectId) body.projectId = projectId;
		if (sessionId) body.sessionId = sessionId;
		if (remote && shell) body.shell = shell;
		if (!remote) {
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
			queryClient.setQueryData<ShellTerminal[]>(queryKey, (current) => {
				if (current?.some((candidate) => candidate.handleId === shell.handleId)) {
					return current.filter((candidate) => candidate.handleId !== optimisticHandleId);
				}
				const index = current?.findIndex((candidate) => candidate.handleId === optimisticHandleId) ?? -1;
				if (index < 0) return [...(current ?? []), shell];
				return current?.map((candidate, candidateIndex) => (candidateIndex === index ? shell : candidate)) ?? [shell];
			});
		},
		onError: (error, _input, context) => {
			if (context?.optimisticHandleId) settlePendingShell(context.optimisticHandleId);
			queryClient.setQueryData<ShellTerminal[]>(queryKey, (current) =>
				current?.filter((shell) => shell.handleId !== context?.optimisticHandleId),
			);
			console.error("Failed to open shell terminal:", error);
			if (!remote && apiErrorCode(error) === "SHELL_TERMINAL_SHELL_UNAVAILABLE") {
				void useTerminalShellStore.getState().setPreference({ kind: "auto" });
			}
		},
		onSettled: (_data, _error, input) => {
			if (!input?.cloud) void queryClient.invalidateQueries({ queryKey });
		},
	});

	const open = (input: OpenShellTerminalInput = {}) => {
		const optimisticShell = createOptimisticShellTerminal(
			input,
			queryClient.getQueryData<ShellTerminal[]>(queryKey) ?? [],
			hostId,
		);
		const request = createShell({ ...input, optimisticShell });
		request.catch(() => undefined);
		trackPendingShell(optimisticShell);
		addOptimisticShell(queryClient, queryKey, optimisticShell);
		mutation.mutate({ ...input, optimisticShell, request });
		return optimisticShell;
	};

	return { ...mutation, open };
}

export async function closeShellTerminal(handleId: string, hostId?: HostId): Promise<void> {
	if (usePreviewData && (!hostId || hostId === LOCAL_HOST)) {
		previewShellTerminals = previewShellTerminals.filter((shell) => shell.handleId !== handleId);
		return;
	}
	const { error } = await clientForSessionHost(hostId).DELETE("/api/v1/shell-terminals/{handleId}", {
		params: { path: { handleId } },
	});
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
			removeClosedShell();
			await queryClient.cancelQueries({ queryKey });
			removeClosedShell();
			return { previous, isCloud };
		},
		onError: (error, _handleId, context) => {
			if (apiErrorCode(error) !== "SHELL_TERMINAL_NOT_FOUND" && context?.previous) {
				queryClient.setQueryData(queryKey, context.previous);
			}
		},
		onSettled: (_data, _error, handleId, context) => {
			const key = hostKey(hostId);
			const closing = closingShellHandles.get(key);
			closing?.delete(handleId);
			if (closing?.size === 0) closingShellHandles.delete(key);
			if (!context?.isCloud && !isPendingShellHandle(handleId)) void queryClient.invalidateQueries({ queryKey });
		},
	});
}

export type RenameShellTerminalInput = { handleId: string; title: string };

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
				setCloudShellTerminals(cloudShellTerminals.map((candidate, index) =>
					index === cloudIndex ? shell : candidate,
				));
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
