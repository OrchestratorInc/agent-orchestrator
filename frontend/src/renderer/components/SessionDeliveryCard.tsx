import { useEffect, useRef, useState } from "react";
import { useTranslation } from "react-i18next";
import type { components } from "../../api/schema";
import { Button } from "./ui/button";
import { Input } from "./ui/input";
import { useSessionDelivery } from "../hooks/useSessionDelivery";

export type DeliveryStatus = components["schemas"]["DeliveryStatus"];

function actionLabel(delivery: DeliveryStatus, t: (key: string, options?: Record<string, unknown>) => string): string {
	const pr = delivery.pullRequest?.number;
	switch (delivery.action) {
		case "commit_and_publish_pr": return t("inspector.delivery.commitAndCreate");
		case "publish_pr": return t("inspector.delivery.createPR");
		case "commit_and_push": return t("inspector.delivery.commitAndPush", { number: pr });
		case "push": return t("inspector.delivery.push", { count: delivery.ahead ?? delivery.commitCount, number: pr });
		default: return "";
	}
}

export function DeliveryCardView({ delivery, error, onAdvance, onOpenFiles, pending = false }: { delivery: DeliveryStatus; error?: string; onAdvance: (commitMessage?: string) => void; onOpenFiles?: () => void; pending?: boolean }) {
	const { t } = useTranslation();
	const needsCommit = delivery.action === "commit_and_publish_pr" || delivery.action === "commit_and_push";
	const [confirming, setConfirming] = useState(false);
	const [message, setMessage] = useState(delivery.commitSubject || t("inspector.delivery.defaultCommitMessage"));
	const inputRef = useRef<HTMLInputElement>(null);
	useEffect(() => { if (confirming) inputRef.current?.focus(); }, [confirming]);
	if (delivery.state === "synchronized") return null;
	const noWork = delivery.state === "empty";
	return (
		<article className="rounded-lg border border-border bg-surface px-3 py-3" aria-label={t("inspector.delivery.ariaLabel")}>
			<div className="flex items-start justify-between gap-2">
				<div className="min-w-0">
					<p className="text-sm font-semibold text-foreground">{noWork ? t("inspector.delivery.empty") : delivery.state === "blocked" ? t("inspector.delivery.blocked") : t("inspector.delivery.ready")}</p>
					{delivery.commitSubject ? <p className="mt-1 truncate text-xs text-muted-foreground">{delivery.commitSubject}</p> : null}
					{!noWork ? <p className="mt-1 text-xs text-muted-foreground">{t(delivery.commitCount ? "inspector.delivery.summaryCommitted" : "inspector.delivery.summaryUncommitted", { additions: delivery.additions, commits: delivery.commitCount, deletions: delivery.deletions, files: delivery.changedFiles })}</p> : null}
				</div>
				{onOpenFiles && !noWork ? <Button onClick={onOpenFiles} size="sm" type="button" variant="ghost">{t("inspector.delivery.inspectFiles")}</Button> : null}
			</div>
			{delivery.blockedReason ? <p className="mt-2 text-xs text-warning" role="status">{delivery.blockedReason}</p> : null}
			{confirming ? (
				<div className="mt-3 space-y-2">
					<label className="text-xs font-medium" htmlFor="delivery-commit-message">{t("inspector.delivery.commitMessage")}</label>
					<Input aria-describedby={error ? "delivery-error" : undefined} aria-invalid={!message.trim()} id="delivery-commit-message" onChange={(event) => setMessage(event.target.value)} ref={inputRef} value={message} />
					<div className="flex gap-2"><Button disabled={pending || !message.trim()} onClick={() => onAdvance(message.trim())} size="sm" type="button">{pending ? t("inspector.delivery.committing") : actionLabel(delivery, t)}</Button><Button disabled={pending} onClick={() => setConfirming(false)} size="sm" type="button" variant="ghost">{t("confirm.cancel")}</Button></div>
				</div>
			) : delivery.action ? <Button aria-label={actionLabel(delivery, t)} className="mt-3 w-full" disabled={pending} onClick={() => needsCommit ? setConfirming(true) : onAdvance()} size="sm" type="button">{pending ? t("inspector.delivery.working") : actionLabel(delivery, t)}</Button> : null}
			{error ? <p className="mt-2 text-xs text-error" id="delivery-error" role="alert">{error}</p> : null}
			<p aria-live="polite" className="sr-only" role="status">{pending ? t("inspector.delivery.pending") : error ? t("inspector.delivery.failed") : ""}</p>
		</article>
	);
}

export function SessionDeliveryCard({ delivery, onOpenFiles, sessionId }: { delivery: DeliveryStatus; onOpenFiles?: () => void; sessionId: string }) {
	const mutation = useSessionDelivery(sessionId);
	return <DeliveryCardView delivery={delivery} error={mutation.error instanceof Error ? mutation.error.message : undefined} onAdvance={(commitMessage) => mutation.mutate({ action: delivery.action!, expectedWorkspaceVersion: delivery.workspaceVersion, commitMessage })} onOpenFiles={onOpenFiles} pending={mutation.isPending} />;
}
