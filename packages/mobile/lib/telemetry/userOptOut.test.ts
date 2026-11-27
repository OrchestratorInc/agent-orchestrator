import { describe, expect, it } from "vitest";
import { createUserOptOut } from "./userOptOut";

function deferred() {
	let resolve!: () => void;
	const promise = new Promise<void>((done) => { resolve = done; });
	return { promise, resolve };
}

function harness() {
	const applied: boolean[] = [];
	const writes: string[] = [];
	let clearGate: ReturnType<typeof deferred> | null = null;
	const optOut = createUserOptOut({
		apply: (v) => applied.push(v),
		save: async () => { writes.push("save"); },
		clear: async () => { writes.push("clear"); if (clearGate) await clearGate.promise; },
	});
	return { optOut, applied, writes, gateClear: () => (clearGate = deferred()) };
}

describe("the phone's analytics switch ordering", () => {
	it("opts out at once and saves; opts in only after clearing the saved opt-out", async () => {
		const { optOut, applied, writes } = harness();
		await optOut.set(true);
		expect(applied).toEqual([true]);
		await optOut.set(false);
		expect(writes).toEqual(["save", "clear"]);
		expect(applied).toEqual([true, false]);
	});

	it("ignores an opt-in that finishes after a later opt-out started", async () => {
		const { optOut, applied, gateClear } = harness();
		const gate = gateClear();
		const optIn = optOut.set(false);
		const optOutAgain = optOut.set(true);
		gate.resolve();
		await Promise.all([optIn, optOutAgain]);
		// Capture stays off: the stale opt-in never applied.
		expect(applied).toEqual([true]);
	});

	it("drops a startup restore when the switch changed after its read began", async () => {
		const { optOut, applied } = harness();
		const startedAt = optOut.generation();
		await optOut.set(true); // the user turns analytics off while storage is read
		expect(optOut.restore(false, startedAt)).toBe(false);
		expect(applied).toEqual([true]);
	});

	it("drops a stale restored opt-out the same way once the user opted back in", async () => {
		const { optOut, applied } = harness();
		const startedAt = optOut.generation();
		await optOut.set(false);
		expect(optOut.restore(true, startedAt)).toBe(false);
		expect(applied).toEqual([false]);
	});

	it("applies a restore when nothing changed meanwhile", () => {
		const { optOut, applied } = harness();
		expect(optOut.restore(true, optOut.generation())).toBe(true);
		expect(applied).toEqual([true]);
	});
});
