import { useEffect, useRef, useState } from "react";
import { useTranslation } from "react-i18next";
import { useAncestorFlag } from "../hooks/useElementFlag";
import { AOMascot } from "./AOMascot";
import { useSystemRequirementsGate } from "../hooks/useSystemRequirementsGate";
import { InstallDependencyDialog } from "./InstallDependencyDialog";
import { aoBridge } from "../lib/bridge";
import { useOnboardingStep } from "../lib/onboarding-telemetry";
import type { OnboardingMissing } from "../lib/onboarding-telemetry-schema";

/** Which blocking requirements are missing, as the funnel's closed names. */
const DEPENDENCY_MISSING: Record<string, OnboardingMissing> = { git: "git", tmux: "tmux", harness: "agent" };

/**
 * Whether first-run onboarding has not been seen yet. The dependency dialog
 * also shows long after onboarding (a tool removed later); only before it
 * does the dialog count toward the onboarding funnel. False until known.
 */
function useOnboardingPending(): boolean {
	const [pending, setPending] = useState(false);
	useEffect(() => {
		let mounted = true;
		void aoBridge.appState.getFirstRunProjectPrompt()
			.then((status) => { if (mounted) setPending(status === "pending"); })
			.catch(() => undefined);
		return () => { mounted = false; };
	}, []);
	return pending;
}

export function DaemonStartupLoader() {
	const { t } = useTranslation();
	const {
		query: requirementsQuery,
		requirements,
		requirementsBlocked,
	} = useSystemRequirementsGate();
	const screenRef = useRef<HTMLDivElement>(null);
	const onboardingPending = useOnboardingPending();
	// The onboarding funnel's dependencies step: viewed while the dialog shows
	// before onboarding has been seen.
	// It is completed from useSetupCheckedTelemetry once nothing blocks, since
	// this screen unmounts in the same render the last requirement is met.
	const missing = requirements
		.filter((requirement) => requirement.required && !requirement.satisfied)
		.map((requirement) => DEPENDENCY_MISSING[requirement.id])
		.filter(Boolean)
		.join(",");
	useOnboardingStep("dependencies", requirementsBlocked && onboardingPending, { autoComplete: false, viewed: { missing } });
	useAncestorFlag(screenRef, ".app-shell-root", "data-startup-screen", true);

	return (
		<div
			aria-busy="true"
			aria-label={t("startup.aria", { brand: "Orchestrator" })}
			ref={screenRef}
			className="ao-startup-screen flex h-full w-full items-center justify-center bg-sidebar text-foreground"
			data-testid="daemon-startup-loader"
			role="status"
		>
			<div className="grid h-28 w-32 -translate-y-[3vh] place-items-center" aria-hidden="true">
				<AOMascot className="ao-startup-logo size-24" />
			</div>
			{requirementsBlocked ? (
				<InstallDependencyDialog
					requirements={requirements}
					onRefetchRequirements={() => requirementsQuery.refetch()}
					reportToOnboarding={onboardingPending}
				/>
			) : null}
		</div>
	);
}
