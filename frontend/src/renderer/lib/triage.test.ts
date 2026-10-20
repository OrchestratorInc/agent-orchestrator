import { QueryClient } from "@tanstack/react-query";
import { beforeEach, expect, it } from "vitest";
import type { TriageChoice, TriageRun } from "../../shared/triage";
import { STANDALONE_WORKSPACE_ID } from "../types/workspace";
import { rememberTaskComposerPreference } from "./task-composer-preferences";
import { cachedTriageOrigin, configuredTriageSnapshot, defaultTriageSelection, isTriageActive, mergeAgentRows, validTriageSelection } from "./triage";

const choice: TriageChoice = { harness: "codex", label: "Codex", available: true, sessionMode: "chat",
	models: [{ id: "gpt", label: "GPT", default: true, efforts: ["low", "high"] }], accounts: [{ id: "work", label: "Work", default: true }] };
const ready = (id: string, state = "authorized") => ({ id, label: id, authentication: { state, checkedAt: "2026-10-19T10:00:00Z" }, installation: { state: "installed" }, effectiveReadiness: "ready", usageCount: 1 });
beforeEach(() => localStorage.clear());

it("keeps only authenticated harnesses and enabled configured-provider models", () => {
	const cache = new QueryClient();
	cache.setQueryData(["settings"], { defaultSessionMode: "chat", chatHarnesses: ["codex"], harnessDefaults: { codex: { disabledModels: ["hidden"] } } });
	cache.setQueryData(["agent-readiness"], { agents: [ready("codex"), ready("unconfigured", "unknown"), ready("signed-out", "unauthorized")] });
	cache.setQueryData(["agent-models", "codex", ""], { configuredProviders: ["openai"], models: [
		{ id: "gpt", label: "GPT", provider: "openai", isDefault: true },
		{ id: "hidden", label: "Hidden", provider: "openai" }, { id: "public", label: "Public", provider: "other" },
	] });
	expect(configuredTriageSnapshot(cache, [choice, { ...choice, harness: "unconfigured" }, { ...choice, harness: "signed-out" }])).toEqual([
		{ ...choice, modelsMissing: false, models: [{ id: "gpt", label: "GPT", default: true }] },
	]);
});

it.each(["chat", "tui"] as const)("freezes the normal %s interface while preserving native agent mode", (defaultSessionMode) => {
	const cache = new QueryClient();
	cache.setQueryData(["settings"], { defaultSessionMode, chatHarnesses: ["codex"] });
	cache.setQueryData(["agent-readiness"], { agents: [ready("codex"), ready("amp")] });
	const choices = configuredTriageSnapshot(cache, [choice, { ...choice, harness: "amp", mode: "low" }])!;
	expect(choices[0].sessionMode).toBe(defaultSessionMode);
	expect(choices[1]).toMatchObject({ sessionMode: "tui", mode: "low" });
	expect(defaultTriageSelection(cache, choices, {}, "amp")).toMatchObject({ sessionMode: "tui", mode: "low" });
});

it("waits for local settings and readiness without fetching or replacing native defaults", () => {
	const cache = new QueryClient();
	expect(configuredTriageSnapshot(cache, [choice])).toBeUndefined();
	cache.setQueryData(["settings"], {});
	cache.setQueryData(["agent-readiness"], { agents: [ready("codex")] });
	expect(configuredTriageSnapshot(cache, [choice])?.[0]).toMatchObject({ sessionMode: "chat", models: choice.models });
});

it("uses normal standalone composer preferences and the current default account", () => {
	const cache = new QueryClient();
	rememberTaskComposerPreference(STANDALONE_WORKSPACE_ID, "codex", { model: "gpt", mode: "", effort: "high" });
	cache.setQueryData(["agent-accounts"], [{ id: "new-work", provider: "codex", signedIn: true, default: true }]);
	expect(defaultTriageSelection(cache, [choice], {})).toMatchObject({ harness: "codex", model: "gpt", effort: "high", accountId: "new-work", sessionMode: "chat" });
});

it("repairs disabled models and unsupported effort within the selected harness", () => {
	const cache = new QueryClient();
	rememberTaskComposerPreference(STANDALONE_WORKSPACE_ID, "codex", { model: "old", mode: "", effort: "extreme" });
	cache.setQueryData(["settings"], { harnessDefaults: { codex: { disabledModels: ["old"] } } });
	cache.setQueryData(["agent-accounts"], []);
	const selected = defaultTriageSelection(cache, [{ ...choice, models: [...choice.models, { id: "old", label: "Old" }] }], {});
	expect(selected).toEqual({ harness: "codex", model: "gpt", sessionMode: "chat" });
	expect(validTriageSelection([choice], selected)).toBe(true);
	expect(validTriageSelection([choice], { ...selected!, effort: "extreme" })).toBe(false);
});

it("allows the configured native default without selecting another billing account", () => {
	const cache = new QueryClient();
	cache.setQueryData(["agent-accounts"], [{ id: "other", provider: "codex", signedIn: true, default: false }]);
	const native = { ...choice, models: [{ id: "", label: "Configured default" }] };
	const selected = defaultTriageSelection(cache, [native], {});
	expect(selected).toEqual({ harness: "codex", model: "", sessionMode: "chat" });
	expect(validTriageSelection([native], selected)).toBe(true);
});

it("keeps a remembered unavailable harness selected and honors an explicit agent change", () => {
	const cache = new QueryClient();
	rememberTaskComposerPreference(STANDALONE_WORKSPACE_ID, "codex", { model: "gpt", mode: "" });
	const choices = [{ ...choice, available: false }, { ...choice, harness: "other" }];
	expect(defaultTriageSelection(cache, choices, {})?.harness).toBe("codex");
	expect(defaultTriageSelection(cache, choices, {}, "other")?.harness).toBe("other");
});

it("keeps remote context separate from this machine's cached measurements", () => {
	const cache = new QueryClient();
	cache.setQueryData(["workspaces"], [{ sessions: [{ id: "affected", cloud: false }] }]);
	cache.setQueryData(["remote-workspaces", "remote"], [{ sessions: [{ id: "affected", cloud: true }] }]);
	cache.setQueryData(["session-memory", "all"], { system: { cpuPercent: 12 }, fetchedAt: "sample-time" });
	expect(cachedTriageOrigin(cache, { sessionId: "affected" })).toMatchObject({ session: { cloud: false }, machine: { system: { cpuPercent: 12 }, fetchedAt: "sample-time" } });
	expect(cachedTriageOrigin(cache, { hostId: "remote", sessionId: "affected" })).toEqual({ hostId: "remote", sessionId: "affected", session: { id: "affected", cloud: true } });
});

it("lists only native fallback runs beside ordinary sessions", () => {
	const run: TriageRun = { id: "report", version: 1, createdAt: "now", updatedAt: "now", request: { harness: "codex", publishAuthorized: false }, state: "working", activity: "Working", messages: [], transport: "chat" };
	expect(isTriageActive(run)).toBe(true);
	expect(mergeAgentRows([], [run, { ...run, id: "preparing", preparation: true }, { ...run, id: "linked", execution: "session" }]).map((row) => row.id)).toEqual(["triage:report"]);
	expect(isTriageActive({ ...run, state: "cancelled" })).toBe(false);
});


it("uses the standalone composer's remembered native mode as the model choice", () => {
	const cache = new QueryClient();
	rememberTaskComposerPreference(STANDALONE_WORKSPACE_ID, "amp", { model: "", mode: "medium" });
	cache.setQueryData(["agent-models", "amp", ""], { selectionMode: "mode", models: [{ id: "low", label: "Low", isDefault: true }, { id: "medium", label: "Medium" }] });
	const native = { ...choice, harness: "amp", mode: "low", sessionMode: "tui" as const, models: [{ id: "low", label: "Low", default: true }, { id: "medium", label: "Medium" }] };
	expect(defaultTriageSelection(cache, [native], {})).toMatchObject({ harness: "amp", model: "medium", mode: "low", sessionMode: "tui" });
});


it("retains an authenticated cached harness when native file discovery did not find it", () => {
	const cache = new QueryClient();
	cache.setQueryData(["settings"], { defaultSessionMode: "tui", chatHarnesses: [] });
	cache.setQueryData(["agent-readiness"], { agents: [ready("command-code")] });
	cache.setQueryData(["agent-models", "command-code", ""], { models: [{ id: "configured", label: "Configured", isDefault: true }] });
	expect(configuredTriageSnapshot(cache, [])).toEqual([expect.objectContaining({ harness: "command-code", modelsMissing: false, models: [{ id: "configured", label: "Configured", default: true }], sessionMode: "tui" })]);
});
