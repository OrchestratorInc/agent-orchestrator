// Ordering for the phone's own "Share usage analytics" switch. Pure, so the
// races between the startup restore and Settings changes are unit-tested here;
// runtime.ts supplies the real telemetry facade and AsyncStorage.

export type UserOptOutDeps = {
	/** Applies the switch to the telemetry facade. */
	apply(optedOut: boolean): void;
	/** Saves the opt-out. */
	save(): Promise<void>;
	/** Clears the saved opt-out. */
	clear(): Promise<void>;
};

export type UserOptOut = {
	/** The change counter; read before a restore starts reading storage. */
	generation(): number;
	/**
	 * A Settings change. Opting out stops capture before saving; opting in clears
	 * the saved opt-out first and applies only if no later change has started.
	 */
	set(optedOut: boolean): Promise<void>;
	/**
	 * Applies a value read from storage when the read began at `startedAt`.
	 * Returns false, applying nothing, when a Settings change has started since:
	 * that newer choice wins over the stale read.
	 */
	restore(optedOut: boolean, startedAt: number): boolean;
};

export function createUserOptOut(deps: UserOptOutDeps): UserOptOut {
	let generation = 0;
	return {
		generation: () => generation,
		async set(optedOut) {
			const mine = ++generation;
			if (optedOut) {
				deps.apply(true);
				await deps.save();
				return;
			}
			await deps.clear();
			if (mine === generation) deps.apply(false);
		},
		restore(optedOut, startedAt) {
			if (startedAt !== generation) return false;
			deps.apply(optedOut);
			return true;
		},
	};
}
