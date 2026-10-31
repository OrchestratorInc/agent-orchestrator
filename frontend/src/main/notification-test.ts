import type { NotificationPermissionState, TestNotificationCopy } from "../shared/notification-permission";

/** The slice of Electron's Notification the probe uses. */
export interface ProbeNotification {
	once(event: "show", listener: () => void): unknown;
	once(event: "failed", listener: (event: unknown, error: string) => void): unknown;
	on(event: "click", listener: () => void): unknown;
	show(): void;
}

export interface NotificationProbeDeps {
	isSupported: () => boolean;
	create: (copy: TestNotificationCopy) => ProbeNotification;
	onClick: () => void;
	/** How long to wait for the OS to show or reject the toast; the macOS prompt can take a while. */
	timeoutMs?: number;
}

const MAX_COPY_LENGTH = 200;

function clean(value: unknown): string {
	return typeof value === "string" ? value.trim().slice(0, MAX_COPY_LENGTH) : "";
}

/**
 * Shows one test notification and reports what the OS did with it. On macOS
 * the first toast also triggers the system permission prompt, so this is how
 * onboarding requests permission. Never rejects.
 */
export function sendTestNotification(
	copy: Partial<TestNotificationCopy> | undefined,
	deps: NotificationProbeDeps,
): Promise<NotificationPermissionState> {
	const title = clean(copy?.title);
	if (!title) return Promise.resolve("unknown");
	if (!deps.isSupported()) return Promise.resolve("unsupported");
	return new Promise((resolve) => {
		let settled = false;
		const settle = (state: NotificationPermissionState) => {
			if (settled) return;
			settled = true;
			clearTimeout(timer);
			resolve(state);
		};
		const timer = setTimeout(() => settle("unknown"), deps.timeoutMs ?? 30_000);
		try {
			const toast = deps.create({ title, body: clean(copy?.body) });
			toast.once("show", () => settle("granted"));
			toast.once("failed", () => settle("denied"));
			toast.on("click", deps.onClick);
			toast.show();
		} catch {
			settle("denied");
		}
	});
}
