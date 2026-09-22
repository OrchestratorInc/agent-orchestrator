import { useEffect, useRef, useState } from "react";
import type { components } from "../../api/schema";
import { Button } from "./ui/button";
import { Input } from "./ui/input";
import { useSessionDelivery } from "../hooks/useSessionDelivery";

export type DeliveryStatus = components["schemas"]["DeliveryStatus"];

function actionLabel(delivery: DeliveryStatus): string {
	const pr = delivery.pullRequest?.number;
	switch (delivery.action) {
		case "commit_and_publish_pr": return "Commit & create pull request";
		case "publish_pr": return "Create pull request";
		case "commit_and_push": return `Commit & push to PR #${pr}`;
		case "push": return `Push ${delivery.ahead ?? delivery.commitCount} commits to PR #${pr}`;
		default: return "";
	}
}

export function DeliveryCardView({ delivery, error, onAdvance, onOpenFiles, pending = false }: { delivery: DeliveryStatus; error?: string; onAdvance: (commitMessage?: string) => void; onOpenFiles?: () => void; pending?: boolean }) {
	const needsCommit = delivery.action === "commit_and_publish_pr" || delivery.action === "commit_and_push";
	const [confirming, setConfirming] = useState(false);
	const [message, setMessage] = useState(delivery.commitSubject || "Publish session changes");
	const inputRef = useRef<HTMLInputElement>(null);
	useEffect(() => { if (confirming) inputRef.current?.focus(); }, [confirming]);
	if (delivery.state === "synchronized") return null;
	const noWork = delivery.state === "empty";
	return (
		<article className="rounded-lg border border-border bg-surface px-3 py-3" aria-label="Session delivery">
			<div className="flex items-start justify-between gap-2">
				<div className="min-w-0">
					<p className="text-sm font-semibold text-foreground">{noWork ? "No local work to publish" : delivery.state === "blocked" ? "Delivery needs attention" : "Ready to deliver"}</p>
					{delivery.commitSubject ? <p className="mt-1 truncate text-xs text-muted-foreground">{delivery.commitSubject}</p> : null}
					{!noWork ? <p className="mt-1 text-xs text-muted-foreground">{delivery.changedFiles} files · <span className="text-success">+{delivery.additions}</span> <span className="text-error">−{delivery.deletions}</span>{delivery.commitCount ? ` · ${delivery.commitCount} commits` : " · uncommitted"}</p> : null}
				</div>
				{onOpenFiles && !noWork ? <Button onClick={onOpenFiles} size="sm" type="button" variant="ghost">Inspect files</Button> : null}
			</div>
			{delivery.blockedReason ? <p className="mt-2 text-xs text-warning" role="status">{delivery.blockedReason}</p> : null}
			{confirming ? (
				<div className="mt-3 space-y-2">
					<label className="text-xs font-medium" htmlFor="delivery-commit-message">Commit message</label>
					<Input aria-describedby={error ? "delivery-error" : undefined} aria-invalid={!message.trim()} id="delivery-commit-message" onChange={(event) => setMessage(event.target.value)} ref={inputRef} value={message} />
					<div className="flex gap-2"><Button disabled={pending || !message.trim()} onClick={() => onAdvance(message.trim())} size="sm" type="button">{pending ? "Committing…" : actionLabel(delivery)}</Button><Button disabled={pending} onClick={() => setConfirming(false)} size="sm" type="button" variant="ghost">Cancel</Button></div>
				</div>
			) : delivery.action ? <Button aria-label={actionLabel(delivery)} className="mt-3 w-full" disabled={pending} onClick={() => needsCommit ? setConfirming(true) : onAdvance()} size="sm" type="button">{pending ? "Working…" : actionLabel(delivery)}</Button> : null}
			{error ? <p className="mt-2 text-xs text-error" id="delivery-error" role="alert">{error}</p> : null}
			<p aria-live="polite" className="sr-only" role="status">{pending ? "Delivery action in progress" : error ? "Delivery action failed" : ""}</p>
		</article>
	);
}

export function SessionDeliveryCard({ delivery, onOpenFiles, sessionId }: { delivery: DeliveryStatus; onOpenFiles?: () => void; sessionId: string }) {
	const mutation = useSessionDelivery(sessionId);
	return <DeliveryCardView delivery={delivery} error={mutation.error instanceof Error ? mutation.error.message : undefined} onAdvance={(commitMessage) => mutation.mutate({ action: delivery.action!, expectedWorkspaceVersion: delivery.workspaceVersion, commitMessage })} onOpenFiles={onOpenFiles} pending={mutation.isPending} />;
}
