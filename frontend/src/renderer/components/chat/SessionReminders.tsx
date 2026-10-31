import { AlarmClock, X } from "lucide-react";
import { useTranslation } from "react-i18next";
import { useCancelReminderMutation, useSessionRemindersQuery } from "../../hooks/useSessionReminders";
import type { ReminderDTO } from "../../lib/reminders";

const sameDayFormatter = new Intl.DateTimeFormat(undefined, { hour: "numeric", minute: "2-digit" });
const otherDayFormatter = new Intl.DateTimeFormat(undefined, {
	weekday: "short",
	month: "short",
	day: "numeric",
	hour: "numeric",
	minute: "2-digit",
});

export function formatReminderDue(dueAt: string, now = new Date()): string {
	const due = new Date(dueAt);
	return due.toDateString() === now.toDateString() ? sameDayFormatter.format(due) : otherDayFormatter.format(due);
}

/**
 * Follow-ups the agent scheduled into this session with `ao remind`, each with
 * a cancel button. Quiet metadata above the composer, like other thread notices.
 */
export function SessionReminders({ sessionId }: { sessionId: string }) {
	const { t } = useTranslation();
	const { data: reminders } = useSessionRemindersQuery(sessionId);
	const cancel = useCancelReminderMutation(sessionId);
	if (!reminders || reminders.length === 0) return null;
	return (
		<ul aria-label={t("chat.reminders.label")} className="flex flex-col gap-0.5">
			{reminders.map((reminder) => (
				<ReminderRow
					key={reminder.id}
					reminder={reminder}
					cancelling={cancel.isPending && cancel.variables === reminder.id}
					onCancel={() => cancel.mutate(reminder.id)}
				/>
			))}
			{cancel.isError ? (
				<li role="alert" className="text-xs text-destructive">{t("chat.reminders.cancelFailed")}</li>
			) : null}
		</ul>
	);
}

function ReminderRow({ reminder, cancelling, onCancel }: { reminder: ReminderDTO; cancelling: boolean; onCancel: () => void }) {
	const { t } = useTranslation();
	const label = reminder.name || reminder.message;
	return (
		<li className="group flex min-w-0 items-center gap-1.5 text-xs text-muted-foreground">
			<AlarmClock aria-hidden="true" className="size-3 shrink-0" />
			<span className="min-w-0 truncate" title={reminder.message}>
				{t("chat.reminders.item", { time: formatReminderDue(reminder.dueAt), label })}
			</span>
			<button
				type="button"
				disabled={cancelling}
				onClick={onCancel}
				className="flex size-5 shrink-0 items-center justify-center rounded-md text-muted-foreground transition-[scale,background-color,color] duration-150 ease-out hover:bg-interactive-hover hover:text-destructive active:scale-[0.96] disabled:pointer-events-none disabled:opacity-50 motion-reduce:transition-none motion-reduce:active:scale-100"
				aria-label={t("chat.reminders.cancel")}
				title={t("chat.reminders.cancel")}
			>
				<X aria-hidden="true" className="shrink-0" width={12} height={12} strokeWidth={2} />
			</button>
		</li>
	);
}
