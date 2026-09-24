import * as Crypto from "expo-crypto";
import { useRouter } from "expo-router";
import { useCallback, useRef, useState } from "react";
import { Alert, Platform } from "react-native";
import { ApiError } from "./api";
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
	const { environment, config, refresh, launchConductor } = useApp();
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

	const openSession = useCallback((row: OrchestratorProjectRow, id: string) => {
		router.push({ pathname: "/session/[id]", params: { id, projectId: row.project.id } });
	}, [router]);

	const runLaunch = useCallback(async (row: OrchestratorProjectRow, mode: "chat" | "tui" = "chat") => {
		if (launching.current.has(row.project.id)) return;
		launching.current.add(row.project.id);
		setBusy(row.project.id, true);
		try {
			if (environment === "cloud") {
				if (!orgId) throw new Error("Your Cloud workspace is not ready. Please try again.");
				let key = cloudRequestKeys.current.get(row.project.id);
				if (!key) {
					key = Crypto.randomUUID();
					cloudRequestKeys.current.set(row.project.id, key);
				}
				const id = await spawnCloudOrchestrator(client, orgId, row.project.id, key);
				cloudRequestKeys.current.delete(row.project.id);
				await refresh().catch(() => {});
				openSession(row, id);
				return;
			}
			const next = await launchConductor(row.project.id, false, mode);
			if (next?.id) openSession(row, next.id);
			else await refresh();
		} catch (cause) {
			haptics.error();
			if (environment === "cloud") {
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
			const copy = describeConnectionFailure(classifyConnectionFailure(httpStatus), {
				host: config?.host ?? "",
				port: config?.httpPort ?? "",
				platform: Platform.OS,
			});
			Alert.alert(copy.title, copy.message);
		} finally {
			launching.current.delete(row.project.id);
			setBusy(row.project.id, false);
		}
	}, [client, config?.host, config?.httpPort, environment, launchConductor, openSession, orgId, refresh, setBusy]);

	/** Opens a running orchestrator, or starts or resumes one that is not. */
	const openOrchestrator = useCallback((row: OrchestratorProjectRow) => {
		if (row.action === "open" && row.link?.id) {
			haptics.select();
			openSession(row, row.link.id);
			return;
		}
		haptics.tap();
		void runLaunch(row);
	}, [openSession, runLaunch]);

	return { busyProjects, openOrchestrator, openSession };
}
