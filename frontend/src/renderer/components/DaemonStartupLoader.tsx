import { useRef } from "react";
import { useTranslation } from "react-i18next";
import { useAncestorFlag } from "../hooks/useElementFlag";
import { AOMascot } from "./AOMascot";
import { useSystemRequirementsGate } from "../hooks/useSystemRequirementsGate";
import { InstallDependencyDialog } from "./InstallDependencyDialog";

export function DaemonStartupLoader() {
	const { t } = useTranslation();
	const {
		query: requirementsQuery,
		requirements,
		requirementsBlocked,
	} = useSystemRequirementsGate();
	const screenRef = useRef<HTMLDivElement>(null);
	useAncestorFlag(screenRef, ".app-shell-root", "data-startup-screen", true);

	return (
		<div
			aria-busy="true"
			aria-label={t("startup.aria", { brand: "Orchestrator" })}
			ref={screenRef}
			className="ao-startup-screen flex h-full w-full items-center justify-center bg-background text-foreground"
			data-testid="daemon-startup-loader"
			role="status"
		>
			<div className="grid h-28 w-32 -translate-y-[3vh] place-items-center" aria-hidden="true">
				<AOMascot className="ao-startup-logo size-24" />
			</div>
			{requirementsBlocked ? (
				<InstallDependencyDialog requirements={requirements} onRefetchRequirements={() => requirementsQuery.refetch()} />
			) : null}
		</div>
	);
}
