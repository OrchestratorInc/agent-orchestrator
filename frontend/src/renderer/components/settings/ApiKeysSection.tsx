import { LoaderCircle } from "lucide-react";
import { useEffect, useId, useState, type FormEvent } from "react";
import { useTranslation } from "react-i18next";
import { checkProviderKey, removeProviderKey, saveProviderKey, useInvalidateProviderKeys, useProviderKeys, type ProviderKey } from "../../hooks/useProviderKeys";
import { agentLabel } from "../../lib/agent-options";
import { Button } from "../ui/button";
import { Input } from "../ui/input";
import { DeviceEnvToggle, KeyStatusDot, savedKeySummary } from "./providerKeyUi";
import { SettingsRow } from "./SettingsRow";
import { SettingsSearch } from "./SettingsSearch";
import { SettingsSection } from "./SettingsSection";

type Notice = { tone: "status" | "error"; text: string };

// A success message fades after this long; an error stays until the next action.
const NOTICE_MS = 6_000;
// An armed "Confirm remove" disarms itself after this long.
const CONFIRM_MS = 4_000;

/** Provider keys AO saves once and passes to every agent session it starts. */
export function ApiKeysSection({ titleHidden }: { titleHidden?: boolean }) {
	const { t } = useTranslation();
	const keys = useProviderKeys();
	const [editing, setEditing] = useState<string>();
	const [query, setQuery] = useState("");
	const [notices, setNotices] = useState<Record<string, Notice>>({});
	const notify = (name: string, notice?: Notice) => setNotices((current) => {
		const next = { ...current };
		if (notice) next[name] = notice; else delete next[name];
		return next;
	});
	const all = keys.data ?? [];
	const saved = all.filter((key) => key.configured).length;
	// Matches the provider, the variable, or a harness that reads it.
	const needle = query.trim().toLocaleLowerCase();
	const matches = (key: ProviderKey) => !needle || [key.label, key.name, ...key.agents.map(agentLabel)].some((text) => text.toLocaleLowerCase().includes(needle));
	// One list in the daemon's order (keys shared by the most harnesses first).
	// The order never changes on save, so a row never moves under the pointer.
	const visible = all.filter(matches);

	if (keys.isPending) return <SettingsSection title={t("settings.apiKeys")} titleHidden={titleHidden}>
		<p role="status" className="flex items-center gap-2 px-(--settings-row-inset) text-control text-settings-muted"><LoaderCircle className="size-3.5 animate-spin" aria-hidden="true" />{t("settings.apiKeys.loading")}</p>
	</SettingsSection>;
	if (keys.isError) return <SettingsSection title={t("settings.apiKeys")} titleHidden={titleHidden}>
		<p role="alert" className="px-(--settings-row-inset) text-control text-error">{keys.error.message}</p>
	</SettingsSection>;
	return <SettingsSection
		sectionId="api-keys"
		title={t("settings.apiKeys")}
		titleHidden={titleHidden}
		grouped
		// The search stays mounted while typing, even when nothing matches.
		toolbar={<SettingsSearch aria-label={t("settings.apiKeys.search")} placeholder={t("settings.apiKeys.search")} value={query} onChange={setQuery} />}
		action={<span className="text-xs text-settings-muted tabular-nums">{t("settings.apiKeys.savedCount", { saved, total: all.length })}</span>}
	>
		{visible.length === 0 ? <p role="status" className="settings-content-row text-control text-settings-muted">{t("settings.apiKeys.noMatches")}</p> : null}
		{visible.map((key) => {
			const open = editing === key.name;
			// The open editor shares the row's tint so the two read as one block.
			return <div key={key.name} data-editing={open ? "" : undefined} className={open ? "bg-(--color-interactive-active)" : undefined}>
				<ProviderKeyRow
					providerKey={key}
					editing={open}
					notice={notices[key.name]}
					onEdit={() => { notify(key.name); setEditing(open ? undefined : key.name); }}
					onNotice={(notice) => notify(key.name, notice)}
				/>
				{open ? <ProviderKeyEditor
					providerKey={key}
					onDone={(notice) => { setEditing(undefined); notify(key.name, notice); }}
				/> : null}
			</div>;
		})}
	</SettingsSection>;
}

function ProviderKeyRow({ providerKey: key, editing, notice, onEdit, onNotice }: {
	providerKey: ProviderKey;
	editing: boolean;
	notice?: Notice;
	onEdit: () => void;
	onNotice: (notice?: Notice) => void;
}) {
	const { t } = useTranslation();
	const [checking, setChecking] = useState(false);
	// Success messages clear themselves; errors stay until the next action.
	useEffect(() => {
		if (notice?.tone !== "status") return;
		const timer = window.setTimeout(() => onNotice(undefined), NOTICE_MS);
		return () => window.clearTimeout(timer);
	// onNotice is rebuilt each render; re-arming on it would never let the timer fire.
	// eslint-disable-next-line react-hooks/exhaustive-deps
	}, [notice]);
	const check = async () => {
		setChecking(true);
		onNotice(undefined);
		try {
			const result = await checkProviderKey(key.name, "");
			switch (result.status) {
			case "valid": onNotice({ tone: "status", text: t("settings.apiKeys.accepted") }); break;
			case "invalid": onNotice({ tone: "error", text: t("settings.apiKeys.rejected") }); break;
			case "unreachable": onNotice({ tone: "error", text: t("settings.apiKeys.unreachable") }); break;
			default: onNotice({ tone: "status", text: t("settings.apiKeys.unverified") });
			}
		} catch (err) {
			onNotice({ tone: "error", text: err instanceof Error ? err.message : t("settings.apiKeys.checkFailed") });
		} finally { setChecking(false); }
	};
	const agentNames = key.agents.map(agentLabel);
	const usedBy = agentNames.length <= 3
		? t("settings.apiKeys.usedBy", { names: agentNames.join(", ") })
		: t("settings.apiKeys.usedByMore", { names: agentNames.slice(0, 3).join(", "), extra: agentNames.length - 3 });
	const state = savedKeySummary(t, key) ?? t("settings.apiKeys.notSet");
	// The key's state and who reads it always stay visible; a check or save
	// result is added on its own line instead of replacing them.
	const description = <>
		<span className="block">{state} · {usedBy}</span>
		{notice ? <span role={notice.tone === "error" ? "alert" : "status"} className={`mt-0.5 block ${notice.tone === "error" ? "text-error" : "text-success"}`}>{notice.text}</span> : null}
	</>;
	return <SettingsRow label={key.label} leading={<KeyStatusDot configured={key.configured} />} description={description}>
		<div className="flex items-center gap-1">
			{key.configured && key.checkable && !editing ? <Button type="button" size="sm" variant="ghost" disabled={checking} onClick={() => void check()}>
				{checking ? <LoaderCircle className="animate-spin" aria-hidden="true" /> : null}{t("settings.apiKeys.check")}
			</Button> : null}
			<Button type="button" size="sm" variant={key.configured || editing ? "ghost" : "outline"} aria-expanded={editing} onClick={onEdit}>
				{t(editing ? "settings.apiKeys.close" : key.configured ? "settings.apiKeys.edit" : "settings.apiKeys.add")}
			</Button>
		</div>
	</SettingsRow>;
}

function ProviderKeyEditor({ providerKey: key, onDone }: { providerKey: ProviderKey; onDone: (notice?: Notice) => void }) {
	const { t } = useTranslation();
	const inputId = useId();
	const invalidate = useInvalidateProviderKeys();
	const [secret, setSecret] = useState("");
	const [device, setDevice] = useState(key.device);
	const [busy, setBusy] = useState(false);
	const [confirmRemove, setConfirmRemove] = useState(false);
	const [error, setError] = useState("");
	const typed = secret.trim();
	const changed = Boolean(typed) || device !== key.device;
	useEffect(() => {
		if (!confirmRemove || busy) return;
		const timer = window.setTimeout(() => setConfirmRemove(false), CONFIRM_MS);
		return () => window.clearTimeout(timer);
	}, [confirmRemove, busy]);

	const submit = async (event: FormEvent) => {
		event.preventDefault();
		if (busy || !changed || (!typed && !key.configured)) return;
		setBusy(true);
		setError("");
		try {
			// A new key is checked first so a mistyped key is never saved.
			const check = typed && key.checkable ? await checkProviderKey(key.name, typed) : undefined;
			if (check?.status === "invalid") {
				setError(t("settings.apiKeys.rejected"));
				return;
			}
			await saveProviderKey(key.name, typed, device);
			setSecret("");
			await invalidate();
			onDone(check?.status === "valid" ? { tone: "status", text: t("settings.apiKeys.savedAccepted") }
				: check?.status === "unreachable" ? { tone: "status", text: t("settings.apiKeys.savedUnchecked") }
				: { tone: "status", text: t("settings.apiKeys.savedNotice") });
		} catch (err) {
			setError(err instanceof Error ? err.message : t("settings.apiKeys.saveFailed"));
		} finally { setBusy(false); }
	};
	const remove = async () => {
		if (!confirmRemove) { setConfirmRemove(true); return; }
		setBusy(true);
		setError("");
		try {
			await removeProviderKey(key.name);
			await invalidate();
			onDone({ tone: "status", text: t("settings.apiKeys.removed") });
		} catch (err) {
			setError(err instanceof Error ? err.message : t("settings.apiKeys.removeFailed"));
		} finally { setBusy(false); }
	};

	// Indented to the row label, past the status dot.
	return <form className="settings-content-row space-y-3 pt-0 pl-[calc(var(--settings-row-inset)+0.5rem+var(--size-settings-row-icon-gap))]" onSubmit={(event) => void submit(event)} onKeyDown={(event) => { if (event.key === "Escape") { event.stopPropagation(); onDone(); } }}>
		<div className="space-y-1.5">
			<div className="flex items-baseline justify-between gap-2">
				<label htmlFor={inputId} className="text-xs leading-4 font-medium text-settings-label">{t("settings.harness.loginUi.apiKey")}</label>
				<code className="truncate font-mono text-xs text-settings-muted">{key.name}</code>
			</div>
			<Input
				id={inputId}
				autoFocus
				className="font-mono text-control placeholder:font-sans"
				type="password"
				value={secret}
				disabled={busy}
				autoComplete="off"
				spellCheck={false}
				maxLength={16384}
				aria-invalid={error ? true : undefined}
				placeholder={key.configured ? t("settings.apiKeys.keepSaved") : undefined}
				onChange={(event) => { setSecret(event.target.value); setError(""); }}
			/>
		</div>
		<DeviceEnvToggle checked={device} disabled={busy} onChange={setDevice} />
		{error ? <p role="alert" className="text-control text-error">{error}</p> : null}
		<div className="flex flex-wrap items-center gap-2">
			{key.configured ? <Button type="button" size="sm" variant="ghost" className="-ml-2 text-error hover:text-error" disabled={busy} onClick={() => void remove()}>
				{busy && confirmRemove ? <LoaderCircle className="animate-spin" aria-hidden="true" /> : null}
				{t(confirmRemove ? "settings.apiKeys.confirmRemove" : "settings.apiKeys.remove")}
			</Button> : null}
			<div className="ml-auto flex items-center gap-2">
				<Button type="button" size="sm" variant="ghost" disabled={busy} onClick={() => onDone()}>{t("settings.apiKeys.cancel")}</Button>
				<Button type="submit" size="sm" disabled={busy || !changed || (!typed && !key.configured)}>
					{busy && !confirmRemove ? <LoaderCircle className="animate-spin" aria-hidden="true" /> : null}
					{t(typed && key.checkable ? "settings.apiKeys.checkAndSave" : "settings.apiKeys.save")}
				</Button>
			</div>
		</div>
	</form>;
}
