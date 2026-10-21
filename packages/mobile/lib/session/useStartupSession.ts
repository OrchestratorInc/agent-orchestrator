import { useEffect, useMemo, useRef, useState } from "react";
import { AppState } from "react-native";
import { useIsFocused } from "expo-router/react-navigation";

import { getSession, type DashboardSession, type OrchestratorLink, type SessionProvisionStep } from "../api";
import { shouldPoll } from "../appStatePoll";
import type { ServerConfig } from "../config";
import { haptics } from "../haptics";
import { sameJson } from "../keepUnchanged";
import { newlyFinishedSteps } from "./sessionStartup";
import { STARTUP_POLL_MS } from "./startupPoll";

type ProvisionFields = Pick<DashboardSession, "provisionState" | "provisionSteps" | "provisionError" | "branch">;

function provisionFields(session: DashboardSession): ProvisionFields {
	return { provisionState: session.provisionState, provisionSteps: session.provisionSteps, provisionError: session.provisionError, branch: session.branch };
}

/**
 * The session with its provisioning facts kept fresh while it starts. Only
 * those fields are overlaid; everything else stays the board's. A change on the
 * board's side (a Retry, a later poll) drops the overlay, so a restarted
 * provision is never masked by an older "failed" read.
 */
export function useStartupSession<T extends DashboardSession | OrchestratorLink>(config: ServerConfig | null, session: T): T {
	const worker: DashboardSession | undefined = "projectName" in session ? undefined : session;
	const boardFields = worker ? provisionFields(worker) : undefined;
	const boardKey = JSON.stringify(boardFields ?? null);
	const [overlay, setOverlay] = useState<{ id: string; boardKey: string; fields: ProvisionFields }>();
	const current = overlay && worker && overlay.id === worker.id && overlay.boardKey === boardKey ? overlay.fields : undefined;
	const provisioning = (current ?? boardFields)?.provisionState === "provisioning";
	const focused = useIsFocused();
	const [appActive, setAppActive] = useState(() => shouldPoll(AppState.currentState));
	useEffect(() => {
		const sub = AppState.addEventListener("change", (state) => setAppActive(shouldPoll(state)));
		return () => sub.remove();
	}, []);

	const id = worker?.id;
	useEffect(() => {
		if (!config || !id || !provisioning || !focused || !appActive) return;
		let stopped = false;
		let timer: ReturnType<typeof setTimeout> | undefined;
		const tick = async () => {
			try {
				const fresh = await getSession(config, id);
				if (stopped) return;
				const fields = provisionFields(fresh);
				setOverlay((prev) => prev && prev.id === id && prev.boardKey === boardKey && sameJson(prev.fields, fields) ? prev : { id, boardKey, fields });
				if (fields.provisionState !== "provisioning") return;
			} catch {
				// Keep the last confirmed stage; the board's banner owns reporting a lost link.
				if (stopped) return;
			}
			// Paced from completion so a slow link never stacks requests.
			timer = setTimeout(() => void tick(), STARTUP_POLL_MS);
		};
		void tick();
		return () => {
			stopped = true;
			clearTimeout(timer);
		};
	}, [appActive, boardKey, config, focused, id, provisioning]);

	useStartupHaptics(worker?.id, (current ?? boardFields)?.provisionSteps);

	return useMemo(() => (worker && current ? ({ ...worker, ...current } as T) : session), [current, session, worker]);
}

/** A light tap as each stage finishes, and a success when the agent is up. */
function useStartupHaptics(id: string | undefined, steps: readonly SessionProvisionStep[] | undefined) {
	const previous = useRef<{ id?: string; steps?: readonly SessionProvisionStep[] }>({});
	useEffect(() => {
		const before = previous.current.id === id ? previous.current.steps : undefined;
		previous.current = { id, steps };
		if (!steps) return;
		const finished = newlyFinishedSteps(before, steps);
		if (finished.includes("agent")) haptics.success();
		else if (finished.length) haptics.tap();
	}, [id, steps]);
}
