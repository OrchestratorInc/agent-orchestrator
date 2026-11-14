import "./lib/apply-initial-theme";
import { createRoot } from "react-dom/client";
import { QueryClientProvider } from "@tanstack/react-query";
import { RouterProvider } from "@tanstack/react-router";
import { I18nextProvider } from "react-i18next";
import "@xterm/xterm/css/xterm.css";
import "./styles.css";
import { preloadMarkdownFonts } from "./hooks/useMarkdownFontsReady";
import { queryClient } from "./lib/query-client";
import { mergeUnreadNotification, unreadNotificationsQueryKey } from "./lib/notifications";
import { playNotificationSound } from "./lib/notification-sound-player";
import { createAppRouter } from "./router";
import { TelemetryBoundary } from "./components/TelemetryBoundary";
import { CloudOnboardingGate } from "./components/CloudOnboardingGate";
import { CloudNotificationRuntime } from "./components/CloudNotificationRuntime";
import { TelemetryIdentityRuntime } from "./components/TelemetryIdentityRuntime";
import { applyAnalyticsOptOut, applyRendererTelemetryPolicy, bindRendererErrorHandlers, clearRendererTelemetryQueues, initTelemetry, isDeniedEvent } from "./lib/telemetry";
import { aoBridge } from "./lib/bridge";
import { startDaemonFailureTelemetry } from "./lib/daemon-telemetry";
import { startUpdateTelemetry } from "./lib/update-telemetry";
import { startSessionManagementTelemetry } from "./lib/session-management-telemetry";
import { appI18n } from "./i18n";
import { useLocaleStore } from "./stores/locale-store";
import { useSoundNotificationsStore } from "./stores/sound-notifications-store";
import { useTelemetryPolicyStore } from "./stores/telemetry-policy-store";
import { useAnalyticsOptOutStore } from "./stores/analytics-opt-out-store";

bindRendererErrorHandlers();
void preloadMarkdownFonts();
const router = createAppRouter(queryClient);

aoBridge.telemetry.onClearQueues(clearRendererTelemetryQueues);
aoBridge.notifications.onPlaySound(() => {
	playNotificationSound(() => aoBridge.notifications.reportSoundFailure());
});
aoBridge.telemetry.onPolicy((view) => applyRendererTelemetryPolicy(view.eventsEnabled && view.acknowledged && view.state === "applied"));

void aoBridge.telemetry.getAnalyticsOptOut().then((optedOut) => {
	if (optedOut) void applyAnalyticsOptOut(true);
}).catch(() => undefined);
aoBridge.telemetry.onAnalyticsOptOut((optedOut) => {
	useAnalyticsOptOutStore.setState({ optedOut, loaded: true });
	void applyAnalyticsOptOut(optedOut);
});

if (import.meta.env.DEV) {
	const w = window as never as Record<string, unknown>;
	w.__qc = queryClient;
	const testNotifTitles: Record<string, string> = {
		needs_input: "Agent needs your input",
		ready_to_merge: "Ready to merge",
		pr_merged: "PR merged",
		pr_closed_unmerged: "PR closed",
	};
	w.__testNotif = async (
		type: "needs_input" | "ready_to_merge" | "pr_merged" | "pr_closed_unmerged" = "needs_input",
	) => {
		const key = unreadNotificationsQueryKey;
		const id = `test-${Date.now()}`;
		queryClient.setQueryDefaults(key, { staleTime: 60_000 });
		await queryClient.cancelQueries({ queryKey: key });
		mergeUnreadNotification(queryClient, {
			id,
			type,
			title: testNotifTitles[type] ?? "Notification",
			body: "Test notification",
			createdAt: new Date().toISOString(),
			sessionId: "",
			projectId: "",
			prUrl: "",
			target: { kind: "session", sessionId: "" },
			status: "unread",
		});
		console.log("[testNotif] bell updated - click away from AO now, bounce fires in 3s");
		setTimeout(() => {
			void window.ao?.notifications.devBounce();
			queryClient.setQueryDefaults(key, { staleTime: 0 });
		}, 3000);
	};
}

let sessionManagementTelemetryRetries = 0;
const bootstrapSessionManagementTelemetry = () => {
	void initTelemetry().then((enabled) => {
		if (enabled) {
			startSessionManagementTelemetry();
			return;
		}
		if (!isDeniedEvent("ao.renderer.session_management_summary") && sessionManagementTelemetryRetries++ < 12) {
			window.setTimeout(bootstrapSessionManagementTelemetry, 5_000);
		}
	});
};
bootstrapSessionManagementTelemetry();
startDaemonFailureTelemetry();
startUpdateTelemetry();

declare module "@tanstack/react-router" {
	interface Register {
		router: typeof router;
	}
}

async function renderApp(): Promise<void> {
	void useTelemetryPolicyStore.getState().load();
	void useLocaleStore.getState().load();
	void useSoundNotificationsStore.getState().load();
	createRoot(document.getElementById("root") as HTMLElement).render(
		<I18nextProvider i18n={appI18n}>
			<TelemetryBoundary>
				<QueryClientProvider client={queryClient}>
					<CloudNotificationRuntime />
					<TelemetryIdentityRuntime />
					<RouterProvider router={router} />
					<CloudOnboardingGate />
				</QueryClientProvider>
			</TelemetryBoundary>
		</I18nextProvider>,
	);
}

void renderApp();
