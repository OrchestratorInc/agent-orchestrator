import * as Crypto from "expo-crypto";
import { useRouter } from "expo-router";
import { useCallback, useRef, useState } from "react";
import { Alert, Platform } from "react-native";
import { ApiError } from "./api";
import { resourceKey, type Scoped, type SourceRef } from "./environment/scopedBoard";
import { chatErrorCopy, isChatPreflightError } from "./chatError";
import { useCloudAuth } from "./cloud/authStore";
import { spawnCloudOrchestrator } from "./cloud/orchestrator";
import { classifyConnectionFailure, describeConnectionFailure } from "./connectionError";
import { haptics } from "./haptics";
import type { OrchestratorProjectRow } from "./orchestratorView";
import { useApp } from "./store";

/**
 * Opening, starting and resuming a project's orchestrator.
 *
 * Lifted out of the Projects screen so the project card and the project page
 * run exactly the same launch: the chat-preflight fallback to Terminal UI, the
 * connection error copy, and the guard against a second tap starting a second
 * orchestrator. Two copies of that would drift the first time either changed.
 */
export function useOrchestratorLauncher() {
	const router = useRouter();
	const { environment, config, configForHost, refreshSource, launchConductorOn } = useApp();
	const { client, orgId } = useCloudAuth();
	const [busyProjects, setBusyProjects] = useState<ReadonlySet<string>>(() => new Set());
	// A ref as well as state: state is a render behind, and a fast double tap must
	// not slip a second launch in before the first one has re-rendered.
	const launching = useRef(new Set<string>());
	const cloudRequestKeys = useRef(new Map<string, string>());

	const setBusy = useCallback((projectId: string, busy: boolean) => {
		setBusyProjects((current) => {
			const next = new Set(current);
			if (busy) next.add(projectId);
			else next.delete(projectId);
			return next;
		});
	}, []);

	const scopedRow = useCallback((row: Scoped<OrchestratorProjectRow> | OrchestratorProjectRow): Scoped<OrchestratorProjectRow> | null => {
		if ("source" in row) return row;
		const rowHostId = "hostId" in row.project && typeof row.project.hostId === "string"
			? row.project.hostId : config?.hostId;
		const source: SourceRef | null = environment === "cloud" && orgId
			? { kind: "cloud", id: orgId }
			: rowHostId && configForHost(rowHostId)
				? { kind: "local", id: rowHostId } : null;
		return source ? { source, value: row } : null;
	}, [environment, orgId, config?.hostId, configForHost]);

	const openSession = useCallback((input: Scoped<OrchestratorProjectRow> | OrchestratorProjectRow, id: string, newlyStarted = false) => {
		const row = scopedRow(input);
		if (!row) return;
		router.push({ pathname: "/session/[id]", params: {
			id, projectId: row.value.project.id, source: row.source.kind, sourceId: row.source.id,
			...(newlyStarted && row.source.kind === "cloud" ? { startup: "spawn" } : {}),
		} });
	}, [router, scopedRow]);

	const runLaunch = useCallback(async (row: Scoped<OrchestratorProjectRow>, mode: "chat" | "tui" = "chat") => {
		const key = resourceKey(row.source, row.value.project.id);
		if (launching.current.has(key)) return;
		launching.current.add(key);
		setBusy(key, true);
		try {
			if (row.source.kind === "cloud") {
				if (row.source.id !== orgId) throw new Error("Your Cloud workspace changed. Please try again.");
				let requestKey = cloudRequestKeys.current.get(key);
				if (!requestKey) {
					requestKey = Crypto.randomUUID();
					cloudRequestKeys.current.set(key, requestKey);
				}
				const id = await spawnCloudOrchestrator(client, orgId, row.value.project.id, requestKey);
				cloudRequestKeys.current.delete(key);
				await refreshSource(row.source).catch(() => {});
				openSession(row, id, true);
				return;
			}
			const next = await launchConductorOn(row.source, row.value.project.id, false, mode);
			if (next?.id) openSession(row, next.id);
			else await refreshSource(row.source);
		} catch (cause) {
			haptics.error();
			if (row.source.kind === "cloud") {
				Alert.alert("Couldn't open orchestrator", cause instanceof Error ? cause.message : "Please try again.");
				return;
			}
			if (mode === "chat" && isChatPreflightError(cause)) {
				Alert.alert("Chat is unavailable", chatErrorCopy(cause), [
					{ text: "Cancel", style: "cancel" },
					{ text: "Start Terminal UI", onPress: () => void runLaunch(row, "tui") },
				]);
				return;
			}
			const httpStatus = cause instanceof ApiError ? cause.status : undefined;
			const target = configForHost(row.source.id);
			const copy = describeConnectionFailure(classifyConnectionFailure(httpStatus), {
				host: target?.host ?? "",
				port: target?.httpPort ?? "",
				platform: Platform.OS,
			});
			Alert.alert(copy.title, copy.message);
		} finally {
			launching.current.delete(key);
			setBusy(key, false);
		}
	}, [client, configForHost, launchConductorOn, openSession, orgId, refreshSource, setBusy]);

	/** Opens a running orchestrator, or starts or resumes one that is not. */
	const openOrchestrator = useCallback((input: Scoped<OrchestratorProjectRow> | OrchestratorProjectRow) => {
		const row = scopedRow(input);
		if (!row) return;
		if (row.value.action === "open" && row.value.link?.id) {
			haptics.select();
			openSession(row, row.value.link.id);
			return;
		}
		haptics.tap();
		void runLaunch(row);
	}, [openSession, runLaunch, scopedRow]);

	return { busyProjects, openOrchestrator, openSession };
}
