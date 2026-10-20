import { useEffect, useState, type ReactNode } from "react";
import type { DaemonStatus } from "../../../shared/daemon-status";
import { aoBridge } from "../../lib/bridge";
import { TriageWorkspaceView } from "./TriageWorkspaceView";

export function TriageWorkspaceBoundary({ runId, children }: { runId?: string; children: ReactNode }) {
	const [status, setStatus] = useState<DaemonStatus>();
	useEffect(() => {
		let active = true;
		void aoBridge.daemon.getStatus().then((next) => { if (active) setStatus(next); }).catch(() => undefined);
		const dispose = aoBridge.daemon.onStatus(setStatus);
		return () => { active = false; dispose(); };
	}, []);
	return status?.state === "ready" ? children : <TriageWorkspaceView runId={runId} recovery />;
}
