import { useId, useState } from "react";
import { useTranslation } from "react-i18next";
import { LoaderCircle, LogIn, RotateCw } from "lucide-react";
import type { ModelCatalogAuthIssue } from "../hooks/useAgentModelsQuery";
import { cn } from "../lib/utils";
import { Button } from "./ui/button";

export type ModelCatalogNoticeProps = {
	agentLabel: string;
	issue: ModelCatalogAuthIssue;
	/** The daemon's own wording, kept out of the headline but one click away. */
	detail?: string;
	/**
	 * A cloud task runs with the user's cloud connection, so this computer's
	 * login only affects which models are listed: say so, quietly.
	 */
	cloud?: boolean;
	onLogin: () => void;
	onRetry: () => Promise<void>;
};

/**
 * The model picker could not list an agent's models because of its login.
 * Says so in plain words and offers the fix (log in) and a retry in place,
 * instead of a raw provider error and a retry buried in the model menu.
 */
export function ModelCatalogNotice({ agentLabel, issue, detail, cloud = false, onLogin, onRetry }: ModelCatalogNoticeProps) {
	const { t } = useTranslation();
	const detailId = useId();
	const [showDetail, setShowDetail] = useState(false);
	const [retrying, setRetrying] = useState(false);
	const headline = cloud
		? t("newTask.modelNotice.cloud", { agent: agentLabel })
		: issue === "expired"
			? t("newTask.modelNotice.expired", { agent: agentLabel })
			: t("newTask.modelNotice.signedOut", { agent: agentLabel });
	const retry = async () => {
		if (retrying) return;
		setRetrying(true);
		try {
			await onRetry();
		} catch {
			// The refreshed catalog carries any new failure as its own warning.
		} finally {
			setRetrying(false);
		}
	};
	return (
		<div
			// Only a signed-out agent is a problem to fix here. An expired token
			// renews on its own, and a cloud task does not use this login at all.
			className={cn("flex flex-col gap-1 text-caption", cloud || issue === "expired" ? "text-muted-foreground" : "text-warning")}
			data-testid="model-catalog-notice"
			role="status"
		>
			<div className="flex flex-wrap items-center gap-x-2 gap-y-1">
				<span className="min-w-0">{headline}</span>
				<span className="inline-flex shrink-0 items-center gap-1">
					<Button type="button" size="sm" variant="outline" className="h-6 px-2" onClick={onLogin}>
						<LogIn aria-hidden="true" className="size-3.5" />
						{issue === "expired" ? t("newTask.modelNotice.loginAgain") : t("newTask.modelNotice.login")}
					</Button>
					<Button type="button" size="sm" variant="ghost" className="h-6 px-2" disabled={retrying} onClick={() => void retry()}>
						{retrying
							? <LoaderCircle aria-hidden="true" className="size-3.5 animate-spin" />
							: <RotateCw aria-hidden="true" className="size-3.5" />}
						{retrying ? t("newTask.modelNotice.retrying") : t("newTask.modelNotice.retry")}
					</Button>
					{detail ? (
						<Button
							type="button"
							size="sm"
							variant="ghost"
							className="h-6 px-2 text-muted-foreground"
							aria-controls={detailId}
							aria-expanded={showDetail}
							onClick={() => setShowDetail((current) => !current)}
						>
							{showDetail ? t("newTask.modelNotice.hideDetails") : t("newTask.modelNotice.details")}
						</Button>
					) : null}
				</span>
			</div>
			{detail && showDetail ? (
				<p id={detailId} className="select-text break-words text-muted-foreground">{detail}</p>
			) : null}
		</div>
	);
}
