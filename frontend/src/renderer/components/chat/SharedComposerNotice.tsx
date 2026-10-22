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
			role="status"
			data-shared-composer-notice
			className="-mb-2 flex min-w-0 items-center gap-2 rounded-t-(--radius-chat-composer) border-b border-border bg-muted/60 px-3 py-1.5 text-xs"
		>
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
	);
}
