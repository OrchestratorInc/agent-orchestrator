import { Bell, BellOff, Check } from "lucide-react";
import { useEffect, useState } from "react";
import { useTranslation } from "react-i18next";
import { notificationSettingsUrl, type NotificationPermissionState } from "../../../shared/notification-permission";
import { aoBridge } from "../../lib/bridge";
import { reportOnboardingStep, useOnboardingStep } from "../../lib/onboarding-telemetry";
import { isMacPlatform, isWindowsPlatform } from "../../lib/platform";
import { captureRendererEvent } from "../../lib/telemetry";
import { TopbarButton } from "../TopbarButton";

/**
 * "offered" once the step was shown during first-run onboarding, then "done" or
 * "skipped". An offered step that the user left by adding a project comes back
 * on Home until they finish it; installs that never saw onboarding never see it.
 */
export const NOTIFICATIONS_STEP_STORAGE_KEY = "ao.onboarding.notifications";
type StepState = "offered" | "done" | "skipped" | null;

function readStepState(): StepState {
	try {
		const value = window.localStorage.getItem(NOTIFICATIONS_STEP_STORAGE_KEY);
		return value === "offered" || value === "done" || value === "skipped" ? value : null;
	} catch {
		return null;
	}
}

function writeStepState(value: Exclude<StepState, null>): void {
	try {
		window.localStorage.setItem(NOTIFICATIONS_STEP_STORAGE_KEY, value);
	} catch {
		// The step simply shows again next launch.
	}
}

const platform = isMacPlatform() ? "darwin" : isWindowsPlatform() ? "win32" : "linux";
const settingsUrl = notificationSettingsUrl(platform);

/**
 * Onboarding step after GitHub sign-in: explains what Orchestrator notifies
 * about, requests permission by sending a test notification (the first toast
 * raises the macOS prompt), and diagnoses a blocked state with a link to the
 * system's notification settings.
 */
export function NotificationsStep({ onboarding }: { onboarding: boolean }) {
	const { t } = useTranslation();
	const [stepState, setStepState] = useState<StepState>(readStepState);
	const [result, setResult] = useState<NotificationPermissionState | null>(null);
	const [pending, setPending] = useState(false);
	const finished = stepState === "done" || stepState === "skipped";
	// Keep a just-granted card on screen so the user sees the confirmation.
	const visible = (!finished && (onboarding || stepState === "offered")) || (stepState === "done" && result !== null);
	useOnboardingStep("notifications", visible && !finished);
	useEffect(() => {
		if (!visible || stepState !== null) return;
		writeStepState("offered");
		setStepState("offered");
	}, [stepState, visible]);
	if (!visible) return null;

	const sendTest = async () => {
		setPending(true);
		const state = await aoBridge.notifications
			.test({ title: t("onboarding.notifications.testTitle"), body: t("onboarding.notifications.testBody") })
			.catch((): NotificationPermissionState => "unknown");
		setPending(false);
		setResult(state);
		void captureRendererEvent("ao.notifications.permission", { state });
		if (state === "granted" && stepState !== "done") {
			reportOnboardingStep("notifications", "completed");
			writeStepState("done");
			setStepState("done");
		}
	};
	const finish = (value: "done" | "skipped") => {
		reportOnboardingStep("notifications", value === "done" ? "completed" : "skipped");
		writeStepState(value);
		setStepState(value);
		setResult(null);
	};

	const denied = result === "denied";
	const message = pending
		? t("onboarding.notifications.waiting")
		: result === "granted"
			? t("onboarding.notifications.granted")
			: result === "unknown"
				? t("onboarding.notifications.unknown")
				: result === "unsupported"
					? t("onboarding.notifications.unsupported")
					: denied
						? t(platform === "darwin" ? "onboarding.notifications.deniedMac" : platform === "win32" ? "onboarding.notifications.deniedWindows" : "onboarding.notifications.deniedOther")
						: t("onboarding.notifications.body");
	const Icon = result === "granted" ? Check : denied || result === "unsupported" ? BellOff : Bell;
	return (
		<div className="flex w-full justify-center px-3">
			<div
				className="w-full max-w-[620px] rounded-welcome-panel border border-[var(--color-border-import-modal)] bg-[var(--color-bg-import-card)] px-4 py-4"
				data-testid="notifications-step"
			>
				<div className="flex items-start gap-3">
					<span className="grid size-9 shrink-0 place-items-center rounded-lg bg-[var(--color-bg-import-chip)] text-[var(--color-text-import-muted)]">
						<Icon className="size-4" aria-hidden="true" />
					</span>
					<div className="min-w-0 flex-1">
						<p className="text-[14px] font-semibold text-[var(--color-text-import-title)]">{t("onboarding.notifications.title")}</p>
						<p aria-live="polite" className={denied ? "mt-0.5 text-[12px] leading-5 text-warning" : "mt-0.5 text-[12px] leading-5 text-[var(--color-text-import-muted)]"} role="status">
							{message}
						</p>
						<div className="mt-2 flex flex-wrap items-center gap-2">
							{result === "granted" ? (
								<>
									<TopbarButton onClick={() => finish("done")} variant="primary">
										{t("onboarding.notifications.done")}
									</TopbarButton>
									<TopbarButton disabled={pending} onClick={() => void sendTest()} variant="accent">
										{t("onboarding.notifications.sendTest")}
									</TopbarButton>
								</>
							) : (
								<>
									{denied && settingsUrl ? (
										<TopbarButton onClick={() => void aoBridge.app.openExternal(settingsUrl)} variant="primary">
											{t("onboarding.notifications.openSettings")}
										</TopbarButton>
									) : null}
									{result !== "unsupported" ? (
										<TopbarButton disabled={pending} onClick={() => void sendTest()} variant={denied && settingsUrl ? "accent" : "primary"}>
											{result === null ? t("onboarding.notifications.allow") : t("onboarding.notifications.sendTest")}
										</TopbarButton>
									) : null}
									{result === "unknown" ? (
										<TopbarButton onClick={() => finish("done")} variant="accent">
											{t("onboarding.notifications.done")}
										</TopbarButton>
									) : null}
									<TopbarButton onClick={() => finish("skipped")} variant="accent">
										{t("onboarding.notifications.skip")}
									</TopbarButton>
								</>
							)}
						</div>
					</div>
				</div>
			</div>
		</div>
	);
}
