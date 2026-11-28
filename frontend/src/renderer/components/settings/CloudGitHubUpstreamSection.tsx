import { useEffect, useRef, useState } from "react";
import { useQuery, useQueryClient } from "@tanstack/react-query";
import { useTranslation } from "react-i18next";
import { useCloudCp } from "../../hooks/useCloudCp";
import { useCloudGate } from "../../hooks/useCloudGate";
import { aoBridge } from "../../lib/bridge";
import { CloudCpError } from "../../lib/cloud-cp";
import { Button } from "../ui/button";
import { SettingsRow } from "./SettingsRow";
import { SettingsSection } from "./SettingsSection";

/** How long Connect waits for the browser to finish before giving up. */
const CONNECT_TIMEOUT_MS = 5 * 60_000;
const CONNECT_POLL_MS = 2500;

export const cloudGitHubUserQueryKey = (baseUrl: string) => ["cloud-github-user", baseUrl] as const;

/**
 * Connecting GitHub for pull requests on upstream repositories: a session's
 * agent can open its pull request on the repository its fork came from, as the
 * person, only once they authorize AO's OAuth App (AO's GitHub App is usually
 * not installed there). Connecting GitHub already asks for it; this row is for
 * people connected before, or who declined. It shows only where the control
 * plane reports it (`upstream` on GET /github/user), so an older control plane
 * shows nothing.
 */
export function CloudGitHubUpstreamSection() {
	const { cloudEnabled } = useCloudGate();
	if (!cloudEnabled) return null;
	return <CloudGitHubUpstreamSectionInner />;
}

function CloudGitHubUpstreamSectionInner() {
	const { t } = useTranslation();
	const { client, ready, baseUrl } = useCloudCp();
	const queryClient = useQueryClient();
	const [waitingSince, setWaitingSince] = useState<number | null>(null);
	const [busy, setBusy] = useState(false);
	const [error, setError] = useState<string | null>(null);
	const mounted = useRef(true);
	useEffect(() => () => {
		mounted.current = false;
	}, []);

	const query = useQuery({
		queryKey: cloudGitHubUserQueryKey(baseUrl),
		enabled: ready,
		queryFn: ({ signal }) => client.getGitHubUser({ signal }),
		refetchInterval: waitingSince === null ? false : CONNECT_POLL_MS,
	});
	const upstream = query.data?.upstream;
	const connected = upstream?.connected === true;

	useEffect(() => {
		if (waitingSince === null) return;
		if (connected) {
			setWaitingSince(null);
			return;
		}
		const remaining = waitingSince + CONNECT_TIMEOUT_MS - Date.now();
		const timer = window.setTimeout(() => {
			setWaitingSince(null);
			setError(t("settings.githubUpstream.timeout"));
		}, Math.max(0, remaining));
		return () => window.clearTimeout(timer);
	}, [connected, t, waitingSince]);

	if (!ready || !upstream) return null;
	const githubConnected = query.data?.connected === true;

	const connect = async () => {
		setBusy(true);
		setError(null);
		try {
			const { authorizeUrl } = await client.startGitHubUpstreamAuthorization();
			await aoBridge.app.openExternal(authorizeUrl);
			if (mounted.current) setWaitingSince(Date.now());
		} catch (err) {
			if (!mounted.current) return;
			setError(
				err instanceof CloudCpError && err.code === "github_not_connected"
					? t("settings.githubUpstream.needsGitHub")
					: t("settings.githubUpstream.failed"),
			);
		} finally {
			if (mounted.current) setBusy(false);
		}
	};

	const disconnect = async () => {
		setBusy(true);
		setError(null);
		setWaitingSince(null);
		try {
			await client.disconnectGitHubUpstream();
			await queryClient.invalidateQueries({ queryKey: cloudGitHubUserQueryKey(baseUrl) });
		} catch {
			if (mounted.current) setError(t("settings.githubUpstream.disconnectFailed"));
		} finally {
			if (mounted.current) setBusy(false);
		}
	};

	let description = t("settings.githubUpstream.description");
	if (connected) {
		description = upstream.login
			? t("settings.githubUpstream.connectedAs", { login: upstream.login })
			: t("settings.githubUpstream.connected");
	} else if (!githubConnected) {
		description = t("settings.githubUpstream.needsGitHub");
	} else if (waitingSince !== null) {
		description = t("settings.githubUpstream.waiting");
	}

	return (
		<SettingsSection title={t("settings.githubUpstream.section")} sectionId="cloudGitHub" grouped>
			<div className="flex w-full flex-col">
				<SettingsRow className="rounded-none" label={t("settings.githubUpstream.label")} description={description}>
					{connected ? (
						<Button variant="ghost" size="sm" disabled={busy} onClick={() => void disconnect()}>
							{t("settings.githubUpstream.disconnect")}
						</Button>
					) : (
						<Button
							variant="secondary"
							size="sm"
							disabled={busy || !githubConnected || waitingSince !== null}
							onClick={() => void connect()}
						>
							{t("settings.githubUpstream.connect")}
						</Button>
					)}
				</SettingsRow>
				{error ? (
					<p role="alert" className="px-3 pt-0 pb-4 text-xs leading-relaxed text-destructive">
						{error}
					</p>
				) : null}
			</div>
		</SettingsSection>
	);
}
