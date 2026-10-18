import { useMutation } from "@tanstack/react-query";
import { useTranslation } from "react-i18next";
import type { components } from "../../../api/schema";
import { apiClient, apiErrorMessage } from "../../lib/api-client";
import { useUiStore } from "../../stores/ui-store";
import { ConfirmDialog } from "../ConfirmDialog";

type Staleness = components["schemas"]["SessionProviderStaleness"];

/**
 * Shown right after a gateway save when running claude-code sessions still
 * carry the previous provider's stamp. Confirming exits and resumes each
 * listed session in place, so the transcript and context survive the switch.
 */
export function GatewayApplySessionsDialog({
	sessions,
	onClose,
}: {
	sessions: Staleness[];
	onClose: () => void;
}) {
	const { t } = useTranslation();
	const displayName = (sessionId: string) =>
		sessions.find((session) => session.sessionId === sessionId)?.displayName || sessionId;
	const apply = useMutation({
		mutationFn: async () => {
			const { data, error } = await apiClient.POST("/api/v1/sessions/apply-provider", {
				body: { sessionIds: sessions.map((session) => session.sessionId) },
			});
			if (error) throw new Error(apiErrorMessage(error));
			return data satisfies components["schemas"]["ApplyProviderResponse"];
		},
		onSuccess: (data) => {
			const toast = useUiStore.getState().showGlobalToast;
			const applied = data.results.filter((result) => result.state === "applied");
			if (applied.length > 0) {
				toast(
					t("settings.gateway.applyAppliedToast", { count: applied.length }),
					undefined,
					"info",
				);
			}
			for (const result of data.results) {
				if (result.state === "skipped") {
					toast(
						t("settings.gateway.applySkippedToast", { name: displayName(result.sessionId) }),
						undefined,
						"info",
					);
				} else if (result.state === "failed") {
					toast(
						t("settings.gateway.applyFailedToast", { name: displayName(result.sessionId) }),
						result.error,
						"error",
					);
				}
			}
			onClose();
		},
	});

	return (
		<ConfirmDialog
			open
			title={t("settings.gateway.applyTitle")}
			description={
				<>
					<p>{t("settings.gateway.applyDescription")}</p>
					<ul className="mt-2 list-disc pl-5">
						{sessions.map((session) => (
							<li key={session.sessionId}>
								{t("settings.gateway.applySessionItem", {
									name: session.displayName || session.sessionId,
									mode: session.mode,
								})}
							</li>
						))}
					</ul>
				</>
			}
			confirmLabel={t("settings.gateway.applyNow")}
			busy={apply.isPending}
			error={apply.error ? apply.error.message : null}
			onConfirm={() => apply.mutate()}
			onOpenChange={(open) => {
				if (!open && !apply.isPending) onClose();
			}}
		/>
	);
}
