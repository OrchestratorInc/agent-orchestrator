/**
 * Whether the machine serves devices: the desktop's Developer mode is on, the
 * user turned device support on, and it has started.
 */
export function deviceSupportOn(status: { available?: boolean; enabled: boolean; state: string }): boolean {
	return status.available === true && status.enabled && status.state !== "off";
}

/** How often whether device support is on is checked again. */
export const DEVICE_SUPPORT_CHECK_MS = 30_000;
/** How often the lease is read while the session holds a device. */
export const LEASE_HELD_MS = 3_000;
/** How often it is read while the session holds none (an agent may open one). */
export const LEASE_IDLE_MS = 10_000;
/** How long to wait after a failed read: a dead machine is not hammered. */
export const LEASE_FAILED_MS = 30_000;

/**
 * Polls a session's device lease the way the desktop does: only while device
 * support is on (checked rarely), quickly while a device is held, slowly
 * otherwise, and slowest after a failure. Each read waits for the last to
 * settle, so a slow relay never stacks requests. Returns the stop function.
 */
export function startSessionDevicePoll<Lease>(
	read: { supportOn: () => Promise<boolean>; lease: () => Promise<Lease | null> },
	onLease: (lease: Lease | null) => void,
	now: () => number = Date.now,
): () => void {
	let stopped = false;
	let timer: ReturnType<typeof setTimeout> | undefined;
	let supportOn = false;
	let supportCheckedAt = -Infinity;
	const tick = async () => {
		let wait: number;
		try {
			if (now() - supportCheckedAt >= DEVICE_SUPPORT_CHECK_MS) {
				supportOn = await read.supportOn();
				supportCheckedAt = now();
			}
			if (!supportOn) {
				if (!stopped) onLease(null);
				wait = DEVICE_SUPPORT_CHECK_MS;
			} else {
				const lease = await read.lease();
				if (!stopped) onLease(lease);
				wait = lease ? LEASE_HELD_MS : LEASE_IDLE_MS;
			}
		} catch {
			wait = LEASE_FAILED_MS;
		}
		if (!stopped) timer = setTimeout(() => void tick(), wait);
	};
	void tick();
	return () => {
		stopped = true;
		if (timer !== undefined) clearTimeout(timer);
	};
}
