export const cloudNotificationsQueryKey = (baseUrl: string, orgId: string, userId: string, status: "all" | "unread" | "read" = "all") =>
	["cloud-notifications", baseUrl.replace(/\/+$/, ""), orgId, userId, status] as const;
