import AsyncStorage from "@react-native-async-storage/async-storage";
import { useEffect, useRef, useState } from "react";

/*
 * Fast mode for an agent whose Fast is its own setting (Claude Code's on/off),
 * in New task. The daemon starts no agent but Codex in Fast mode, and Claude
 * Code reports its Fast only once a session runs, for the model it runs; no
 * catalog says which models have it beforehand. So the phone learns it: each
 * chat that shows Claude's Fast remembers its model, New task offers the bolt
 * for those models, and a task started with it leaves a note the chat it
 * opens acts on, turning Claude's Fast on as soon as Claude reports it (as
 * Ultracode is handed over, see ultracodeKey).
 */

/** The agents whose Fast is learned this way. */
export const LEARNED_FAST_HARNESSES: readonly string[] = ["claude-code"];

/**
 * A model by the name both screens show it under, compared loosely: the
 * chat's "Opus 5.5" and New task's "Claude Opus 5.5" are one model.
 */
export function fastModelName(label: string): string {
	return label.trim().toLowerCase().replace(/^claude\s+/, "").replace(/\s+/g, " ");
}

const modelsKey = (machine: string, harness: string) => `ao.fastModels.${machine}.${harness}`;

/** Where a task started in Fast mode leaves its note for the chat it opens. */
export function fastPendingKey(machine: string, sessionId: string): string {
	return `ao.chat.fastPending.${machine}.${sessionId}`;
}

async function readModels(machine: string, harness: string): Promise<string[]> {
	try {
		const parsed: unknown = JSON.parse((await AsyncStorage.getItem(modelsKey(machine, harness))) ?? "[]");
		return Array.isArray(parsed) ? parsed.filter((item): item is string => typeof item === "string") : [];
	} catch {
		return [];
	}
}

/** Remembers that `label`, on this machine's `harness`, offers Fast mode. */
export async function rememberFastModel(machine: string, harness: string, label: string): Promise<void> {
	const name = fastModelName(label);
	if (!name || !LEARNED_FAST_HARNESSES.includes(harness)) return;
	const known = await readModels(machine, harness);
	if (known.includes(name)) return;
	await AsyncStorage.setItem(modelsKey(machine, harness), JSON.stringify([...known, name])).catch(() => {});
}

/** The models of this machine's `harness` seen offering Fast mode, by fastModelName. */
export function useLearnedFastModels(machine: string | null | undefined, harness: string): ReadonlySet<string> {
	const [models, setModels] = useState<ReadonlySet<string>>(() => new Set());
	useEffect(() => {
		setModels(new Set());
		if (!machine || !LEARNED_FAST_HARNESSES.includes(harness)) return;
		let live = true;
		void readModels(machine, harness).then((names) => { if (live) setModels(new Set(names)); });
		return () => { live = false; };
	}, [machine, harness]);
	return models;
}

/**
 * The chat's side of the hand-over: once this session's Fast shows (`fast`),
 * a note left by New task (`key`) turns it on, once. The note goes either way
 * once the agent has reported its settings (`reported`): with no Fast among
 * them, the model has none to turn on.
 */
export function usePendingFast(key: string | null, fast: { on: boolean; toggle: () => void } | undefined, reported: boolean): void {
	const [pending, setPending] = useState(false);
	useEffect(() => {
		setPending(false);
		if (!key) return;
		let live = true;
		AsyncStorage.getItem(key).then((value) => { if (live) setPending(value === "1"); }).catch(() => {});
		return () => { live = false; };
	}, [key]);
	const done = useRef<string | null>(null);
	useEffect(() => {
		if (!pending || !key || done.current === key) return;
		if (!fast && !reported) return;
		done.current = key;
		setPending(false);
		void AsyncStorage.removeItem(key).catch(() => {});
		if (fast && !fast.on) fast.toggle();
	}, [pending, key, fast, reported]);
}
