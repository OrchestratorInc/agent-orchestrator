import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { useState } from "react";
import { useTranslation } from "react-i18next";
import type { components } from "../../../api/schema";
import { apiClient, apiErrorMessage } from "../../lib/api-client";
import { usesPreviewWorkspaceData } from "../../lib/preview-mode";
import { useUiStore, type GlobalSettingsSection } from "../../stores/ui-store";
import { Button } from "../ui/button";
import { SettingsRow } from "./SettingsRow";
import { SettingsSection } from "./SettingsSection";

type ChromeStatus = components["schemas"]["ControllersBrowserChromeStatusResponse"];

export const browserChromeQueryKey = ["browser-chrome"] as const;

const SECTION_ID = "browserChrome";

/**
 * Open Settings → Browser and bring "Agents in your Chrome" into view. The
 * section renders after the page switches, so wait for it briefly.
 */
export function openChromeAgentsSettings(open: (section?: GlobalSettingsSection) => void): void {
	open("browserProfiles");
	const deadline = Date.now() + 1_500;
	const reveal = () => {
		const section = document.querySelector<HTMLElement>(`[data-section="${SECTION_ID}"]`);
		if (section) {
			section.scrollIntoView?.({ block: "start", behavior: "smooth" });
			return;
		}
		if (Date.now() < deadline) requestAnimationFrame(reveal);
	};
	requestAnimationFrame(reveal);
}

const previewStatus: ChromeStatus = {
	enabled: false,
	connected: false,
	extensionId: "eefgahhblbmjpccfcnpkhkadgcccnibg",
	extensionPath: "~/.ao/data/browser-chrome/extension",
	extensionBundled: true,
	hostPath: "~/.ao/data/browser-chrome/ao-browser-host",
	channels: [{ name: "Google Chrome", available: true, registered: false, manifestPath: "" }],
};

async function fetchChromeStatus(): Promise<ChromeStatus> {
	if (usesPreviewWorkspaceData) return previewStatus;
	const { data, error } = await apiClient.GET("/api/v1/browser/chrome");
	if (error || !data) throw new Error(apiErrorMessage(error));
	return data;
}

// Chrome mode lets agents browse in the user's own Chrome through the AO
// Browser Bridge extension. Setup registers AO's native host with Chrome;
// loading the unpacked extension is the user's own step in chrome://extensions.
export function BrowserChromeSection() {
	const { t } = useTranslation();
	const developerMode = useUiStore((state) => state.developerMode);
	const queryClient = useQueryClient();
	const [copied, setCopied] = useState(false);
	const query = useQuery({ queryKey: browserChromeQueryKey, queryFn: fetchChromeStatus, refetchInterval: 3000 });
	const change = useMutation({
		mutationFn: async (enable: boolean) => {
			if (usesPreviewWorkspaceData) return;
			const { data, error } = enable
				? await apiClient.POST("/api/v1/browser/chrome")
				: await apiClient.DELETE("/api/v1/browser/chrome");
			if (error || !data) throw new Error(apiErrorMessage(error));
			queryClient.setQueryData(browserChromeQueryKey, data);
		},
		onSettled: () => void queryClient.invalidateQueries({ queryKey: browserChromeQueryKey }),
	});

	const status = query.data;
	const registered = status?.channels.filter((channel) => channel.registered).map((channel) => channel.name) ?? [];
	const state = !status
		? ""
		: !status.enabled
			? t("settings.browserChrome.status.notSetUp")
			: status.connected
				? t("settings.browserChrome.status.connected", { version: status.clientVersion ?? "" })
				: t("settings.browserChrome.status.setUp");

	const copyPath = async () => {
		if (!status) return;
		await navigator.clipboard.writeText(status.extensionPath);
		setCopied(true);
		setTimeout(() => setCopied(false), 1500);
	};

	// A developer-mode feature; stays visible once set up so Remove is reachable.
	if (!developerMode && !status?.enabled) return null;

	return (
		<SettingsSection title={t("settings.browserChrome.title")} sectionId={SECTION_ID} grouped>
			<SettingsRow
				label={t("settings.browserChrome.mode")}
				description={
					<>
						<span role="status">{state}</span>
						{change.error ? (
							<span className="mt-1 block text-error">
								{t("settings.browserChrome.setupFailed", { message: change.error.message })}
							</span>
						) : null}
						{query.error ? <span className="mt-1 block text-error">{query.error.message}</span> : null}
					</>
				}
			>
				<Button
					disabled={!status || change.isPending}
					onClick={() => change.mutate(!status?.enabled)}
					size="sm"
					type="button"
					variant={status?.enabled ? "ghost" : "primary"}
				>
					{status?.enabled ? t("settings.browserChrome.remove") : t("settings.browserChrome.setup")}
				</Button>
			</SettingsRow>
			<SettingsRow label={t("settings.browserChrome.about")} description={t("settings.browserChrome.description")} />
			{status?.enabled ? (
				<SettingsRow
					label={t("settings.browserChrome.registeredWith")}
					description={registered.length > 0 ? registered.join(", ") : t("settings.browserChrome.registeredNone")}
				/>
			) : null}
			{status?.enabled && !status.connected ? (
				<SettingsRow
					label={t("settings.browserChrome.loadTitle")}
					description={
						status.extensionBundled ? (
							<>
								{t("settings.browserChrome.loadSteps")}
								<span className="mt-1 block font-mono break-all">{status.extensionPath}</span>
								<span className="mt-1 block">
									{t("settings.browserChrome.extensionId")}: <span className="font-mono">{status.extensionId}</span>
								</span>
							</>
						) : (
							<span className="text-error">{t("settings.browserChrome.bundleMissing")}</span>
						)
					}
				>
					{status.extensionBundled ? (
						<Button onClick={() => void copyPath()} size="sm" type="button" variant="ghost">
							{copied ? t("settings.browserChrome.copied") : t("settings.browserChrome.copyPath")}
						</Button>
					) : null}
				</SettingsRow>
			) : null}
			{status?.enabled ? (
				<SettingsRow label={t("settings.browserChrome.agentsTitle")} description={t("settings.browserChrome.agentsHint")} />
			) : null}
		</SettingsSection>
	);
}
