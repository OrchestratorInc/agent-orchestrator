import { act, create, type ReactTestRenderer } from "react-test-renderer";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

const store = vi.hoisted(() => new Map<string, string>());
vi.mock("@react-native-async-storage/async-storage", () => ({
	default: {
		getItem: async (key: string) => store.get(key) ?? null,
		setItem: async (key: string, value: string) => { store.set(key, value); },
		removeItem: async (key: string) => { store.delete(key); },
	},
}));

import { fastModelName, fastPendingKey, rememberFastModel, useLearnedFastModels, usePendingFast } from "./learnedFast";

let renderer: ReactTestRenderer | undefined;
beforeEach(() => { store.clear(); vi.stubGlobal("IS_REACT_ACT_ENVIRONMENT", true); });
afterEach(() => { act(() => renderer?.unmount()); renderer = undefined; vi.unstubAllGlobals(); });
const flush = () => act(async () => { await Promise.resolve(); await Promise.resolve(); });

describe("learned Fast mode", () => {
	// The chat names Claude's model "Opus 5.5", New task's catalog "Claude Opus 5.5".
	it("names a model the same way on both screens", () => {
		expect(fastModelName("Claude Opus 5.5")).toBe(fastModelName(" Opus  5.5 "));
		expect(fastModelName("Sonnet 5.5")).not.toBe(fastModelName("Opus 5.5"));
	});

	it("remembers Claude's models with Fast, per machine, and no other agent's", async () => {
		await rememberFastModel("mac", "claude-code", "Opus 5.5");
		await rememberFastModel("mac", "claude-code", "Claude Opus 5.5");
		await rememberFastModel("mac", "codex", "GPT-6.2");
		let seen: ReadonlySet<string> = new Set();
		function Harness({ machine, harness }: { machine: string; harness: string }) { seen = useLearnedFastModels(machine, harness); return null; }
		act(() => { renderer = create(<Harness machine="mac" harness="claude-code" />); });
		await flush();
		expect([...seen]).toEqual(["opus 5.5"]);
		act(() => renderer!.update(<Harness machine="other" harness="claude-code" />));
		await flush();
		expect(seen.size).toBe(0);
		act(() => renderer!.update(<Harness machine="mac" harness="codex" />));
		await flush();
		expect(seen.size).toBe(0);
	});

	// The chat a Fast task opens turns Claude's Fast on once it shows, once.
	it("turns the chat's Fast on from New task's note, once, and drops a note with no Fast to turn on", async () => {
		const key = fastPendingKey("mac", "s1");
		store.set(key, "1");
		const toggle = vi.fn();
		function Harness({ fast, reported }: { fast?: { on: boolean; toggle: () => void }; reported: boolean }) { usePendingFast(key, fast, reported); return null; }
		act(() => { renderer = create(<Harness reported={false} />); });
		await flush();
		expect(toggle).not.toHaveBeenCalled();
		act(() => renderer!.update(<Harness fast={{ on: false, toggle }} reported />));
		await flush();
		expect(toggle).toHaveBeenCalledOnce();
		expect(store.has(key)).toBe(false);
		act(() => renderer!.update(<Harness fast={{ on: false, toggle }} reported />));
		await flush();
		expect(toggle).toHaveBeenCalledOnce();

		const other = fastPendingKey("mac", "s2");
		store.set(other, "1");
		function NoFast() { usePendingFast(other, undefined, true); return null; }
		act(() => { renderer = create(<NoFast />); });
		await flush();
		expect(store.has(other)).toBe(false);
	});
});
