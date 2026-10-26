import { Users } from "lucide-react";
import { useTranslation } from "react-i18next";
import type { CloudCpSessionLive } from "../../lib/cloud-cp";
import type { ComposerLeaseState } from "../../hooks/useComposerLease";
import { Button } from "../ui/button";

const NAMES_SHOWN = 3;

function nameList(names: string[]): string {
	const shown = names.slice(0, NAMES_SHOWN).join(", ");
	return names.length > NAMES_SHOWN ? `${shown} +${names.length - NAMES_SHOWN}` : shown;
}

/**
 * The strip above a shared cloud session's chat box: who else is here, who is
 * writing to the agent, and the person's own place in line, with the actions
 * that change it. Nothing shows while nobody else has the session open.
 */
export function SharedComposerNotice({ live, lease, userId }: {
	live: CloudCpSessionLive;
	lease: ComposerLeaseState;
	userId: string;
}) {
	const { t } = useTranslation();
	const someone = t("chat.shared.someone");
	const others = live.viewers.filter((viewer) => viewer.userId !== userId);
	const waiting = live.composer.waitlist.filter((waiter) => waiter.userId !== userId);
	const readOnly = live.viewers.find((viewer) => viewer.userId === userId)?.role === "viewer";
	if (others.length === 0 && !lease.holder && waiting.length === 0) return null;

	let status: string;
	if (lease.holding) {
		status = t("chat.shared.holding");
	} else if (lease.holder) {
		status = t("chat.shared.writing", { name: lease.holder.name || someone });
	} else {
		status = t("chat.shared.alsoHere", { names: nameList(others.map((viewer) => viewer.name || someone)) });
	}
	const detail = lease.position
		? t("chat.shared.position", { position: lease.position })
		: waiting.length > 0
			? t("chat.shared.waiting", { names: nameList(waiting.map((waiter) => waiter.name || someone)) })
			: undefined;
	const canAsk = !readOnly && !lease.holding && Boolean(lease.holder) && !lease.position;

	return (
		<div
			data-shared-composer-notice
			data-composer-notice
			className="flex min-w-0 flex-col rounded-t-(--radius-chat-composer) border-b border-border bg-surface text-xs"
		>
			<div role="status" className="flex min-w-0 items-center gap-2 px-3 py-1.5">
				<Users aria-hidden="true" className="size-3.5 shrink-0 text-muted-foreground" />
				<p className="min-w-0 flex-1 truncate">
					<span className="text-foreground">{status}</span>
					{detail ? <span className="text-muted-foreground"> · {detail}</span> : null}
					{lease.error ? <span className="text-destructive"> · {lease.error}</span> : null}
				</p>
				{canAsk ? (
					<Button type="button" variant="ghost" size="sm" onClick={lease.requestTurn}>{t("chat.shared.requestTurn")}</Button>
				) : null}
				{lease.position ? (
					<Button type="button" variant="ghost" size="sm" onClick={lease.leaveLine}>{t("chat.shared.leaveLine")}</Button>
				) : null}
				{lease.holder && lease.canTakeOver ? (
					<Button type="button" variant="ghost" size="sm" onClick={lease.takeOver}>{t("chat.shared.takeOver")}</Button>
				) : null}
			</div>
			{lease.remoteDraft ? (
				<RemoteDraft text={lease.remoteDraft.text} cursor={lease.remoteDraft.cursor} name={lease.remoteDraft.holder.name || someone} />
			) : null}
		</div>
	);
}

/**
 * The lease holder's draft as they type it, read-only, with their caret and
 * name where they are typing, the way a document editor shows a collaborator.
 */
export function RemoteDraft({ text, cursor, name }: { text: string; cursor: number; name: string }) {
	const { t } = useTranslation();
	const at = Math.min(Math.max(cursor, 0), text.length);
	return (
		<div
			role="region"
			aria-label={t("chat.shared.draftOf", { name })}
			data-remote-draft
			className="max-h-40 overflow-y-auto px-3 pt-3 pb-2 text-sm leading-relaxed whitespace-pre-wrap break-words text-foreground"
		>
			{text.slice(0, at)}
			<span className="relative inline-block h-[1.15em] w-0 align-text-bottom" data-remote-caret>
				<span aria-hidden="true" className="absolute inset-y-0 left-0 w-[2px] rounded-full bg-foreground motion-safe:animate-pulse" />
				<span
					aria-hidden="true"
					className="absolute bottom-full left-0 mb-0.5 rounded-xs bg-foreground px-1 text-[10px] leading-4 font-medium whitespace-nowrap text-background"
				>
					{name}
				</span>
			</span>
			{text.slice(at)}
		</div>
	);
}
