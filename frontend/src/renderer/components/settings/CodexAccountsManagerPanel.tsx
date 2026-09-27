import { ArrowDown, ArrowUp, KeyRound, LoaderCircle, Upload } from "lucide-react";
import { useEffect, useMemo, useRef, useState } from "react";
import { Button } from "../ui/button";
import { Input } from "../ui/input";
import {
	addCodexAPIKey,
	fetchCodexManagedModels,
	fetchCodexManagedQuota,
	importCodexCredential,
	refreshCodexManagedAccount,
	removeCodexManagedAccount,
	resetCodexManagedQuota,
	setCodexManagedAccountDisabled,
	setCodexRouting,
	useCodexAccountsManagerQuery,
} from "../../hooks/useCodexAccountsManagerQuery";

/** Small Codex-only management surface for the embedded CLIProxy accounts. */
export function CodexAccountsManagerPanel() {
	const [expanded, setExpanded] = useState(false);
	const query = useCodexAccountsManagerQuery(expanded);
	const [order, setOrder] = useState<string[]>([]);
	const [enabled, setEnabled] = useState(true);
	const [apiKey, setAPIKey] = useState("");
	const [label, setLabel] = useState("");
	const [baseURL, setBaseURL] = useState("");
	const [details, setDetails] = useState<Record<string, { models?: string[]; quota?: string }>>({});
	const [busy, setBusy] = useState(false);
	const [error, setError] = useState<string | null>(null);
	const fileInput = useRef<HTMLInputElement>(null);
	const accounts = query.data?.accounts ?? [];
	const ordered = useMemo(() => {
		const known = new Set(accounts.map((account) => account.id));
		return [...order.filter((id) => known.has(id)), ...accounts.map((account) => account.id).filter((id) => !order.includes(id))];
	}, [accounts, order]);

	useEffect(() => {
		if (!query.data?.routing) return;
		setOrder(query.data.routing.accountIds);
		setEnabled(query.data.routing.enabled);
	}, [query.data?.revision]);

	const refresh = async (operation: () => Promise<unknown>) => {
		setError(null);
		setBusy(true);
		try { await operation(); await query.refetch(); }
		catch (cause) { setError(cause instanceof Error ? cause.message : "Account manager operation failed"); }
		finally { setBusy(false); }
	};

	const saveRouting = () => void refresh(() => setCodexRouting(enabled, ordered));
	const inspect = (id: string) => void refresh(async () => {
		const [models, quota] = await Promise.all([fetchCodexManagedModels(id), fetchCodexManagedQuota(id)]);
		setDetails((current) => ({ ...current, [id]: { models: models.models.map((model) => model.id), quota: quota.exceeded ? `${quota.reason || "quota exceeded"}${quota.nextRecoverAt ? ` · resets ${new Date(quota.nextRecoverAt).toLocaleString()}` : ""}` : "available" } }));
	});
	const move = (id: string, direction: -1 | 1) => {
		const next = [...ordered];
		const index = next.indexOf(id);
		const target = index + direction;
		if (index < 0 || target < 0 || target >= next.length) return;
		[next[index], next[target]] = [next[target], next[index]];
		setOrder(next);
	};

	return <div className="border-t border-border px-4 py-4" data-testid="codex-accounts-manager">
		<button type="button" aria-label="Manage CLIProxy accounts" className="flex w-full items-start justify-between gap-3 text-left" onClick={() => setExpanded((value) => !value)} aria-expanded={expanded}><div><p className="text-sm font-medium text-foreground">CLIProxy account routing</p><p className="text-xs text-muted-foreground">Each new Codex session is pinned once; existing pins never move.</p></div><span className="text-xs text-muted-foreground">{expanded ? "Hide" : "Manage"}</span></button>
		{!expanded ? null : <>
		<div className="mb-3 mt-3 flex items-center justify-between gap-3"><span className="text-xs text-muted-foreground">Ordered fallback accounts</span><label className="flex items-center gap-2 text-xs text-muted-foreground"><input type="checkbox" checked={enabled} onChange={(event) => setEnabled(event.target.checked)} />Enabled</label></div>
		{query.isLoading ? <LoaderCircle className="size-4 animate-spin text-muted-foreground" /> : null}
		{query.error ? <p className="text-xs text-error">{query.error instanceof Error ? query.error.message : "Could not load account manager"}</p> : null}
		{accounts.length > 0 ? <div className="mb-3 divide-y divide-border rounded-md border border-border/70">{ordered.map((id, index) => { const account = accounts.find((item) => item.id === id); if (!account) return null; const detail = details[id]; return <div className="px-3 py-2" key={id}><div className="flex items-center gap-2"><span className="w-5 text-xs text-muted-foreground">{index + 1}</span><div className="min-w-0 flex-1"><p className="truncate text-xs text-foreground">{account.email || account.id}</p><p className="text-[11px] text-muted-foreground">{account.kind}{account.disabled ? " · disabled" : ""}</p></div><Button type="button" size="icon-sm" variant="ghost" disabled={busy || index === 0} aria-label="Move account up" onClick={() => move(id, -1)}><ArrowUp aria-hidden="true" /></Button><Button type="button" size="icon-sm" variant="ghost" disabled={busy || index === ordered.length - 1} aria-label="Move account down" onClick={() => move(id, 1)}><ArrowDown aria-hidden="true" /></Button><Button type="button" size="sm" variant="ghost" disabled={busy} onClick={() => void refresh(() => refreshCodexManagedAccount(id))}>Refresh</Button><Button type="button" size="sm" variant="ghost" disabled={busy} onClick={() => void inspect(id)}>Details</Button><Button type="button" size="sm" variant="ghost" disabled={busy} onClick={() => void refresh(() => setCodexManagedAccountDisabled(id, !account.disabled))}>{account.disabled ? "Enable" : "Disable"}</Button><Button type="button" size="sm" variant="ghost" disabled={busy || account.kind !== "api_key"} onClick={() => void refresh(() => removeCodexManagedAccount(id))}>Remove</Button></div>{detail ? <p className="mt-1 pl-7 text-[11px] text-muted-foreground">Models: {detail.models?.join(", ") || "none"} · Quota: {detail.quota}{detail.quota !== "available" ? <Button type="button" size="sm" variant="ghost" className="ml-1 h-5 px-1 text-[11px]" disabled={busy} onClick={() => void refresh(() => resetCodexManagedQuota(id))}>Reset</Button> : null}</p> : null}</div>; })}</div> : <p className="mb-3 text-xs text-muted-foreground">No proxy accounts yet.</p>}
		<div className="flex flex-wrap items-center gap-2"><Input aria-label="Codex API key" type="password" value={apiKey} onChange={(event) => setAPIKey(event.target.value)} placeholder="Codex API key" className="max-w-sm" /><Input aria-label="Codex account label" value={label} onChange={(event) => setLabel(event.target.value)} placeholder="Label (optional)" className="max-w-xs" /><Input aria-label="Codex base URL" value={baseURL} onChange={(event) => setBaseURL(event.target.value)} placeholder="Base URL (optional)" className="max-w-xs" /><Button type="button" size="sm" disabled={busy || !apiKey.trim()} onClick={() => void refresh(async () => { await addCodexAPIKey(apiKey, label, baseURL); setAPIKey(""); setLabel(""); setBaseURL(""); })}><KeyRound aria-hidden="true" />Add API key</Button><input ref={fileInput} type="file" accept="application/json,.json" className="hidden" onChange={(event) => { const file = event.target.files?.[0]; if (!file) return; void refresh(async () => { await importCodexCredential(file.name, JSON.parse(await file.text())); if (fileInput.current) fileInput.current.value = ""; }); }} /><Button type="button" size="sm" variant="outline" disabled={busy} onClick={() => fileInput.current?.click()}><Upload aria-hidden="true" />Import JSON</Button><Button type="button" size="sm" variant="outline" disabled={busy || !accounts.length} onClick={saveRouting}>{busy ? <LoaderCircle className="animate-spin" /> : null}Save order</Button></div>
		{error ? <p role="alert" className="mt-2 text-xs text-error">{error}</p> : null}</>}
	</div>;
}
