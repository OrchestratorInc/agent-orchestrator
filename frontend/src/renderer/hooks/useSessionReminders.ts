import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { cancelReminder, fetchSessionReminders, sessionRemindersQueryKey } from "../lib/reminders";

/** Reminders arrive from the agent's own `ao remind` calls, so poll for them. */
const REMINDER_POLL_MS = 30_000;

export function useSessionRemindersQuery(sessionId: string | undefined) {
	return useQuery({
		queryKey: sessionRemindersQueryKey(sessionId ?? ""),
		queryFn: () => fetchSessionReminders(sessionId ?? ""),
		enabled: Boolean(sessionId),
		refetchInterval: (query) => (query.state.status === "error" ? false : REMINDER_POLL_MS),
		retry: false,
	});
}

export function useCancelReminderMutation(sessionId: string) {
	const queryClient = useQueryClient();
	return useMutation({
		mutationFn: (reminderId: string) => cancelReminder(reminderId),
		onSettled: () => {
			void queryClient.invalidateQueries({ queryKey: sessionRemindersQueryKey(sessionId) });
		},
	});
}
