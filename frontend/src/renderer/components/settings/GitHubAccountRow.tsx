import { useQuery, useQueryClient } from "@tanstack/react-query";
import { useState } from "react";
import { useTranslation } from "react-i18next";
import { githubAuthRequirementQueryOptions } from "../../hooks/useSystemRequirementsGate";
import { apiClient } from "../../lib/api-client";
import { getGitHubStatus } from "../../lib/github-daemon";
import { useUiStore } from "../../stores/ui-store";
import { ConfirmDialog } from "../ConfirmDialog";
import { Button } from "../ui/button";
import { SettingsRow } from "./SettingsRow";

const githubStatusQueryKey = ["github-status"] as const;

/** The GitHub account AO uses. Only AO's own sign-in can be disconnected here;
 * a GitHub CLI login or a token from the environment is managed outside AO. */
export function GitHubAccountRow() {
	const { t } = useTranslation();
	const queryClient = useQueryClient();
	const status = useQuery({ queryKey: githubStatusQueryKey, queryFn: getGitHubStatus });
	const auth = useQuery(githubAuthRequirementQueryOptions);
	const closeSettings = useUiStore((state) => state.closeSettings);
	const [confirmOpen, setConfirmOpen] = useState(false);
	const [busy, setBusy] = useState(false);
	const [error, setError] = useState<string | null>(null);

	const signedInHere = status.data?.connected === true;
	const login = status.data?.login;
	const description = signedInHere
		? login
			? t("settings.github.connectedAs", { login })
			: t("settings.github.connected")
		: auth.data?.satisfied
			? t("settings.github.connectedElsewhere")
			: t("settings.github.notConnected");

	const disconnect = async () => {
		setBusy(true);
		setError(null);
		const { error: deleteError } = await apiClient.DELETE("/api/v1/github/pat");
		setBusy(false);
		if (deleteError) {
			setError(t("settings.github.disconnectFailed"));
			return;
		}
		setConfirmOpen(false);
		void queryClient.invalidateQueries({ queryKey: githubStatusQueryKey });
		// The fresh credential check brings the GitHub sign-in back up when nothing
		// else connects AO to GitHub. Close Settings so it is in front; the second
		// call also closes project settings that General may return to.
		const next = await queryClient.fetchQuery({ ...githubAuthRequirementQueryOptions, staleTime: 0 }).catch(() => null);
		if (next?.satisfied === false) {
			closeSettings();
			closeSettings();
		}
	};

	return (
		<>
			<SettingsRow label={t("settings.github.account")} description={description}>
				{signedInHere ? (
					<Button type="button" size="sm" variant="ghost" className="text-settings-muted hover:text-settings-label" disabled={busy} onClick={() => setConfirmOpen(true)}>
						{t("settings.github.disconnect")}
					</Button>
				) : null}
			</SettingsRow>
			<ConfirmDialog
				open={confirmOpen}
				title={t("settings.github.disconnectTitle")}
				description={t("settings.github.disconnectBody")}
				confirmLabel={t("settings.github.disconnect")}
				destructive
				busy={busy}
				error={error}
				onConfirm={() => void disconnect()}
				onOpenChange={(open) => {
					setConfirmOpen(open);
					if (!open) setError(null);
				}}
			/>
		</>
	);
}
