import { useTranslation } from "react-i18next";
import { AOMascot } from "./AOMascot";
import { useSystemRequirementsGate } from "../hooks/useSystemRequirementsGate";
import { InstallDependencyDialog } from "./InstallDependencyDialog";
import { useEffect, useState } from "react";
import type { DaemonStatus } from "../../shared/daemon-status";
import { aoBridge } from "../lib/bridge";
import { daemonFailureMessage, daemonFailureTitle } from "../lib/daemon-failure";
import { TriageReportButton } from "./triage/TriageReportButton";

export function DaemonStartupLoader() {
	const { t } = useTranslation();
	const [status, setStatus] = useState<DaemonStatus>();
	useEffect(() => {
		let active = true;
		void aoBridge.daemon.getStatus().then((next) => { if (active) setStatus(next); }).catch(() => undefined);
		const dispose = aoBridge.daemon.onStatus(setStatus);
		return () => { active = false; dispose(); };
	}, []);
	const failed = status?.code && (status.state === "error" || status.state === "stopped");
	const {
		query: requirementsQuery,
		requirements,
		requirementsBlocked,
	} = useSystemRequirementsGate();

	return (
		<div
			aria-busy="true"
			aria-label={t("startup.aria", { brand: "Agent Orchestrator" })}
			className="ao-startup-screen flex h-full w-full flex-col items-center justify-center gap-5 bg-background text-foreground"
			data-testid="daemon-startup-loader"
			role="status"
		>
			<div className="grid h-28 w-32 -translate-y-[3vh] place-items-center" aria-hidden="true">
				<AOMascot className="ao-startup-logo size-24" />
			</div>
			{failed && status ? <section className="w-full max-w-md px-6" aria-label={t("triage.startupFailure")}>
				<p className="text-sm font-medium">{daemonFailureTitle(status, t)}</p>
				<p className="mt-1 text-xs text-muted-foreground">{daemonFailureMessage(status, t)}</p>
				<TriageReportButton className="mt-4" origin={{ surface: "startup-error", daemonState: status.state, daemonCode: status.code, daemonOutput: status.details }} />
			</section> : null}
			{requirementsBlocked ? (
				<InstallDependencyDialog requirements={requirements} onRefetchRequirements={() => requirementsQuery.refetch()} />
			) : null}
		</div>
	);
}
