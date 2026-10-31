import type { components } from "../../api/schema";
import { apiClient, apiErrorMessage } from "./api-client";

export type ReminderDTO = components["schemas"]["ReminderResponse"];

export const sessionRemindersQueryKey = (sessionId: string) => ["reminders", sessionId] as const;

/** A session's pending follow-ups (ao remind), soonest first. */
export async function fetchSessionReminders(sessionId: string): Promise<ReminderDTO[]> {
	const { data, error } = await apiClient.GET("/api/v1/reminders", {
		params: { query: { sessionId } },
	});
	if (error) throw new Error(apiErrorMessage(error, "Could not load follow-ups"));
	return data?.reminders ?? [];
}

export async function cancelReminder(reminderId: string): Promise<ReminderDTO> {
	const { data, error } = await apiClient.DELETE("/api/v1/reminders/{reminderId}", {
		params: { path: { reminderId } },
	});
	if (error) throw new Error(apiErrorMessage(error, "Could not cancel the follow-up"));
	return data.reminder;
}
