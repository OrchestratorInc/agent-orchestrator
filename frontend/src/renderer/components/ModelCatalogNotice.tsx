import { useId, useState } from "react";
import { useTranslation } from "react-i18next";
import { Download, LoaderCircle, LogIn, RotateCw } from "lucide-react";
import type { ModelCatalogIssue } from "../hooks/useAgentModelsQuery";
import { cn } from "../lib/utils";
import { Button } from "./ui/button";

export type ModelCatalogNoticeProps = {
	agentLabel: string;
	/** The daemon's own wording, kept out of the headline but one click away. */
	detail?: string;
	onRetry: () => Promise<void>;
} & (
	| { issue: "not_installed"; onInstall: () => void; onLogin?: never }
	| { issue: Exclude<ModelCatalogIssue, "not_installed">; onLogin: () => void; onInstall?: never }
);

/**
 * The model picker could not list an agent's models because of its installation or login.
 * Says so in plain words and offers installation or login and a retry in place,
 * instead of a raw provider error and a retry buried in the model menu.
 */
export function ModelCatalogNotice(props: ModelCatalogNoticeProps) {
	const { agentLabel, issue, detail, onRetry } = props;
	const { t } = useTranslation();
	const detailId = useId();
	const [showDetail, setShowDetail] = useState(false);
	const [retrying, setRetrying] = useState(false);
	const action = props.issue === "not_installed"
		? { headline: "newTask.modelNotice.missingCLI", label: "newTask.modelNotice.install", run: props.onInstall, Icon: Download } as const
		: { headline: issue === "expired" ? "newTask.modelNotice.expired" : "newTask.modelNotice.signedOut", label: issue === "expired" ? "newTask.modelNotice.loginAgain" : "newTask.modelNotice.login", run: props.onLogin, Icon: LogIn } as const;
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
			// A missing CLI or signed-out agent needs action. An expired token
			// renews on its own.
			className={cn("flex flex-col gap-1 text-caption", issue === "expired" ? "text-muted-foreground" : "text-warning")}
			data-testid="model-catalog-notice"
			role="status"
		>
			<div className="flex flex-wrap items-center gap-x-2 gap-y-1">
				<span className="min-w-0">{t(action.headline, { agent: agentLabel })}</span>
				<span className="inline-flex shrink-0 items-center gap-1">
					<Button type="button" size="sm" variant="outline" className="h-6 px-2" onClick={action.run}>
						<action.Icon aria-hidden="true" className="size-3.5" />
						{t(action.label, { agent: agentLabel })}
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
