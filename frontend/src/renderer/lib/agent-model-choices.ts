export function isConcreteModelID(id: string): boolean {
	return id !== "" && id.toLowerCase() !== "default";
}

export function isDefaultPlaceholderLabel(label: string): boolean {
	return /^default(?:\s*\([^)]*\))?$/i.test(label.trim());
}

export function modelChoiceLabel(choice: { id: string; label: string }): string {
	return isDefaultPlaceholderLabel(choice.label) ? choice.id : choice.label || choice.id;
}

export function agentModelDisplayLabel(agentId: string | undefined, label: string): string {
	return agentId === "claude-code" ? label.replace(/^Claude\s+/i, "") : label;
}

const CLAUDE_FAMILIES = ["fable", "opus", "sonnet", "haiku"];

function claudeFamilyVersion(model: { id: string; label: string }) {
	for (const text of [modelChoiceLabel(model), model.id]) {
		// "(1M context)" and "[1m]" are not versions.
		const lower = text.toLowerCase().replace(/\(.*?\)|\[.*?\]/g, "");
		const family = CLAUDE_FAMILIES.findIndex((name) => lower.includes(name));
		// Major and minor only: skips snapshot dates and suffixes like "v1:0".
		const version = lower.match(/\d+/g)?.filter((part) => part.length <= 2).slice(0, 2).map(Number);
		if (family >= 0 && version?.length) return { family, version };
	}
	return undefined;
}

function compareVersionsDesc(a: number[], b: number[]) {
	for (let i = 0; i < Math.max(a.length, b.length); i += 1) {
		if ((a[i] ?? 0) !== (b[i] ?? 0)) return (b[i] ?? 0) - (a[i] ?? 0);
	}
	return 0;
}

// A context variant ("opus[1m]", "Opus 5.5 (1M context)") is its own choice,
// not an older release of its family.
function claudeVariant(model: { id: string; label: string }): string {
	return (model.id.match(/\[.*?\]/)?.[0] ?? model.label.match(/\(.*?\)/)?.[0] ?? "").toLowerCase();
}

/** The newest model of each Claude family (and context variant), in Fable, Opus, Sonnet, Haiku order, then everything else, newest first. Models without a family and version stay current. */
export function splitClaudeModels<T extends { id: string; label: string }>(models: T[]): { current: T[]; other: T[] } {
	const ranked = models.flatMap((model) => {
		const parsed = claudeFamilyVersion(model);
		return parsed ? [{ model, ...parsed }] : [];
	});
	ranked.sort((a, b) => a.family - b.family || compareVersionsDesc(a.version, b.version));
	const current: T[] = [];
	const other: T[] = [];
	const seen = new Set<string>();
	for (const { model, family } of ranked) {
		const slot = `${family}${claudeVariant(model)}`;
		(seen.has(slot) ? other : current).push(model);
		seen.add(slot);
	}
	current.push(...models.filter((model) => !claudeFamilyVersion(model)));
	return { current, other };
}

type ClaudeRow = { value: string; name: string; description?: string | null };

function isContextVariant(row: ClaudeRow): boolean {
	return /\[.*\]$/.test(row.value);
}

/** The release a Claude Code row runs: read from its name or id, else from what the row says about itself ("Opus 5.5 · Best for…"). */
function claudeRowRelease(row: ClaudeRow) {
	return (
		claudeFamilyVersion({ id: row.value, label: row.name }) ??
		claudeFamilyVersion({ id: "", label: (row.description ?? "").split("·")[0] ?? "" })
	);
}

function rowRunsModel(row: ClaudeRow, model: { id: string; label: string }): boolean {
	if (row.value.replace(/\[.*\]$/, "").toLowerCase() === model.id.toLowerCase()) return true;
	const runs = claudeRowRelease(row);
	const wanted = claudeFamilyVersion(model);
	return Boolean(
		runs && wanted && runs.family === wanted.family &&
			runs.version.length === wanted.version.length && compareVersionsDesc(runs.version, wanted.version) === 0,
	);
}

/**
 * The model choices of a Claude chat that runs on a managed account: the
 * account's models, in the account's order and under the account's names.
 *
 * Claude Code only switches to a row it reported, and it reports some models
 * under a short name ("opus" for the newest Opus), so each model is sent as the
 * row that runs it. A model with no row cannot be switched to in this process
 * and is left out, as is every row that is not one of the account's models.
 * A row for the same model with a larger context window stays, next to it. The
 * row the chat is on now always stays, so the picker can show it.
 */
export function claudeAccountChoices<Row extends ClaudeRow>(
	rows: Row[],
	models: { id: string; label: string }[],
	current?: string,
): Row[] {
	if (models.length === 0) return rows;
	const plain = rows.filter((row) => row.value !== "default" && !isContextVariant(row));
	const wide = rows.filter(isContextVariant);
	const choices: Row[] = [];
	const taken = new Set<string>();
	const offer = (row: Row | undefined, name: string) => {
		if (!row || taken.has(row.value)) return;
		taken.add(row.value);
		choices.push({ ...row, name });
	};
	for (const model of models) {
		offer(
			plain.find((row) => row.value.toLowerCase() === model.id.toLowerCase()) ?? plain.find((row) => rowRunsModel(row, model)),
			model.label,
		);
		for (const row of wide) {
			if (rowRunsModel(row, model)) offer(row, `${model.label} ${row.name.match(/\(.*?\)/)?.[0] ?? "(1M context)"}`);
		}
	}
	const now = rows.find((row) => row.value === current);
	if (now && !taken.has(now.value)) choices.unshift(now);
	return choices;
}

type AccountModel = { id: string; label: string; efforts?: string[]; defaultEffort?: string };
type LiveModel = { id: string; displayName: string; description?: string; default: boolean; efforts?: string[]; defaultEffort?: string };

/**
 * The model choices of a chat that runs on a managed account and takes any
 * model by name: the account's models. The running chat still says which model
 * it is on, and that model stays listed even if the account's list lacks it.
 */
export function accountChatModels(account: AccountModel[], live: LiveModel[]): LiveModel[] {
	const offered = account.filter((model) => isConcreteModelID(model.id));
	if (offered.length === 0) return live;
	const running = new Map(live.map((model) => [model.id, model]));
	const models = offered.map((model) => {
		const now = running.get(model.id);
		return {
			id: model.id,
			displayName: model.label || model.id,
			default: Boolean(now?.default),
			efforts: model.efforts?.length ? model.efforts : now?.efforts,
			defaultEffort: now?.defaultEffort ?? model.defaultEffort,
		};
	});
	const current = live.find((model) => model.default && !offered.some((entry) => entry.id === model.id));
	return current ? [current, ...models] : models;
}

/** A configured family alias ("sonnet") duplicates that family's newest model, so mark that model as the default instead. */
export function foldClaudeAliasDefault<T extends { id: string; label: string; isDefault?: boolean }>(models: T[]): T[] {
	const alias = models.find((model) => model.isDefault && CLAUDE_FAMILIES.includes(model.id));
	if (!alias) return models;
	const rest = models.filter((model) => model !== alias);
	const family = CLAUDE_FAMILIES.indexOf(alias.id);
	const newest = splitClaudeModels(rest).current.find((model) => claudeFamilyVersion(model)?.family === family && claudeVariant(model) === "");
	return newest ? rest.map((model) => (model === newest ? { ...model, isDefault: true } : model)) : models;
}
