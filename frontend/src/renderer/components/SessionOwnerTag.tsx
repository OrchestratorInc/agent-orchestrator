import { useTranslation } from "react-i18next";
import type { WorkspaceSession } from "../types/workspace";

/**
 * "Vaanya's session": marks a cloud session someone the project was shared
 * with asked its orchestrator for. Nothing for any other session.
 */
export function SessionOwnerTag({ session }: { session: Pick<WorkspaceSession, "cloud"> }) {
	const { t } = useTranslation();
	const requester = session.cloud?.requestedBy;
	if (!requester) return null;
	const name = requester.name || t("chat.shared.someone");
	return (
		<span
			className="inline-flex min-w-0 max-w-full items-center rounded-full bg-muted px-2 py-0.5 text-2xs font-medium text-muted-foreground"
			data-session-owner-tag
			title={t("share.sessionOf", { name })}
		>
			<span className="min-w-0 truncate">{t("share.sessionOf", { name })}</span>
		</span>
	);
}
