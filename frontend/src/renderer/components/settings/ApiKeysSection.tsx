import { LoaderCircle } from "lucide-react";
import { useState, type FormEvent } from "react";
import { useTranslation } from "react-i18next";
import { checkProviderKey, removeProviderKey, saveProviderKey, useInvalidateProviderKeys, useProviderKeys, type ProviderKey } from "../../hooks/useProviderKeys";
import { agentLabel } from "../../lib/agent-options";
import { Button } from "../ui/button";
import { Checkbox } from "../ui/checkbox";
import { Input } from "../ui/input";
import { SettingsRow } from "./SettingsRow";
import { SettingsSearch } from "./SettingsSearch";
import { SettingsSection } from "./SettingsSection";

type Notice = { tone: "status" | "error"; text: string };

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
	// The daemon's order (most shared first) stays put, so a row never moves
	// under the pointer after its key is saved.
	const ordered = (items: ProviderKey[]) => items.filter(matches);
	const groups = [
		{ id: "providers", title: t("settings.apiKeys.providers"), items: ordered(all.filter((key) => key.agents.length > 1)) },
		{ id: "services", title: t("settings.apiKeys.services"), items: ordered(all.filter((key) => key.agents.length <= 1)) },
	// The first list always renders so the search field stays mounted while typing.
	].filter((group, index) => index === 0 || group.items.length > 0);
	const search = <SettingsSearch aria-label={t("settings.apiKeys.search")} placeholder={t("settings.apiKeys.search")} value={query} onChange={setQuery} />;

	if (keys.isPending) return <SettingsSection title={t("settings.apiKeys")} titleHidden={titleHidden}>
		<p role="status" className="flex items-center gap-2 px-(--settings-row-inset) text-control text-settings-muted"><LoaderCircle className="size-3.5 animate-spin" aria-hidden="true" />{t("settings.apiKeys.loading")}</p>
	</SettingsSection>;
	if (keys.isError) return <SettingsSection title={t("settings.apiKeys")} titleHidden={titleHidden}>
		<p role="alert" className="px-(--settings-row-inset) text-control text-error">{keys.error.message}</p>
	</SettingsSection>;
	return <>
		{groups.map((group, index) => <SettingsSection
			key={group.id}
			sectionId={`api-keys-${group.id}`}
			title={group.title}
			grouped
			{...(index === 0 ? { toolbar: search, action: saved ? <span className="text-xs text-settings-muted tabular-nums">{t("settings.apiKeys.savedCount", { saved })}</span> : undefined } : {})}
		>
			{group.items.length === 0 ? <p role="status" className="settings-content-row text-control text-settings-muted">{t("settings.apiKeys.noMatches")}</p> : null}
			{group.items.map((key) => <div key={key.name}>
				<ProviderKeyRow
					providerKey={key}
					editing={editing === key.name}
					notice={notices[key.name]}
					onEdit={() => { notify(key.name); setEditing(editing === key.name ? undefined : key.name); }}
					onNotice={(notice) => notify(key.name, notice)}
				/>
				{editing === key.name ? <ProviderKeyEditor
					providerKey={key}
					onDone={(notice) => { setEditing(undefined); notify(key.name, notice); }}
				/> : null}
			</div>)}
		</SettingsSection>)}
	</>;
}

function agentsLabel(agents: string[], t: ReturnType<typeof useTranslation>["t"]) {
	const names = agents.map(agentLabel);
	if (names.length <= 3) return t("settings.apiKeys.usedBy", { names: names.join(", ") });
	return t("settings.apiKeys.usedByMore", { names: names.slice(0, 3).join(", "), extra: names.length - 3 });
}

function ProviderKeyRow({ providerKey: key, editing, notice, onEdit, onNotice }: {
	providerKey: ProviderKey;
	editing: boolean;
	notice?: Notice;
	onEdit: () => void;
	onNotice: (notice: Notice) => void;
}) {
	const { t } = useTranslation();
	const [checking, setChecking] = useState(false);
	const check = async () => {
		setChecking(true);
		try {
			const result = await checkProviderKey(key.name, "");
			onNotice(checkNotice(result.status, t));
		} catch (err) {
			onNotice({ tone: "error", text: err instanceof Error ? err.message : t("settings.apiKeys.checkFailed") });
		} finally { setChecking(false); }
	};
	const saved = key.configured
		? [key.hint ? t("settings.apiKeys.savedHint", { hint: key.hint }) : t("settings.apiKeys.saved"), key.device ? t("settings.apiKeys.outsideAo") : ""].filter(Boolean).join(" · ")
		: agentsLabel(key.agents, t);
	const description = notice
		? <span role={notice.tone === "error" ? "alert" : "status"} className={notice.tone === "error" ? "text-error" : undefined}>{notice.text}</span>
		: saved;
	return <SettingsRow label={key.label} description={description}>
		<div className="flex items-center gap-1">
			{key.configured && key.checkable && !editing ? <Button type="button" size="sm" variant="ghost" disabled={checking} onClick={() => void check()}>
				{checking ? <LoaderCircle className="animate-spin" aria-hidden="true" /> : null}{t("settings.apiKeys.check")}
			</Button> : null}
			<Button type="button" size="sm" variant="ghost" aria-expanded={editing} onClick={onEdit}>
				{t(editing ? "settings.apiKeys.close" : key.configured ? "settings.apiKeys.edit" : "settings.apiKeys.add")}
			</Button>
		</div>
	</SettingsRow>;
}

function checkNotice(status: string, t: ReturnType<typeof useTranslation>["t"]): Notice {
	switch (status) {
	case "valid": return { tone: "status", text: t("settings.apiKeys.accepted") };
	case "invalid": return { tone: "error", text: t("settings.apiKeys.rejected") };
	case "unreachable": return { tone: "error", text: t("settings.apiKeys.unreachable") };
	default: return { tone: "status", text: t("settings.apiKeys.unverified") };
	}
}

function ProviderKeyEditor({ providerKey: key, onDone }: { providerKey: ProviderKey; onDone: (notice?: Notice) => void }) {
	const { t } = useTranslation();
	const invalidate = useInvalidateProviderKeys();
	const [secret, setSecret] = useState("");
	const [device, setDevice] = useState(key.device);
	const [busy, setBusy] = useState(false);
	const [confirmRemove, setConfirmRemove] = useState(false);
	const [error, setError] = useState("");
	const typed = secret.trim();
	const changed = Boolean(typed) || device !== key.device;

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

	return <form className="settings-content-row space-y-3 pt-0" onSubmit={(event) => void submit(event)} onKeyDown={(event) => { if (event.key === "Escape") { event.stopPropagation(); onDone(); } }}>
		<Input
			aria-label={key.label}
			autoFocus
			className="font-mono text-control"
			type="password"
			value={secret}
			disabled={busy}
			autoComplete="off"
			spellCheck={false}
			maxLength={16384}
			placeholder={key.configured ? t("settings.apiKeys.keepSaved") : undefined}
			onChange={(event) => { setSecret(event.target.value); setError(""); }}
		/>
		<label className="flex items-start gap-2">
			<Checkbox checked={device} className="mt-0.5" disabled={busy} onCheckedChange={(checked) => setDevice(checked === true)} />
			<span className="min-w-0 flex-1">
				<span className="block text-control leading-5 text-settings-label">{t("settings.apiKeys.device")}</span>
				<span className="block text-xs leading-4 text-settings-muted">{t("settings.apiKeys.deviceHint")}</span>
			</span>
		</label>
		{error ? <p role="alert" className="text-control text-error">{error}</p> : null}
		<div className="flex flex-wrap items-center gap-2">
			<Button type="submit" size="sm" disabled={busy || !changed || (!typed && !key.configured)}>
				{busy && !confirmRemove ? <LoaderCircle className="animate-spin" aria-hidden="true" /> : null}
				{t(typed && key.checkable ? "settings.apiKeys.checkAndSave" : "settings.apiKeys.save")}
			</Button>
			<Button type="button" size="sm" variant="ghost" disabled={busy} onClick={() => onDone()}>{t("settings.apiKeys.cancel")}</Button>
			{key.configured ? <Button type="button" size="sm" variant="ghost" className="ml-auto text-error hover:text-error" disabled={busy} onClick={() => void remove()}>
				{busy && confirmRemove ? <LoaderCircle className="animate-spin" aria-hidden="true" /> : null}
				{t(confirmRemove ? "settings.apiKeys.confirmRemove" : "settings.apiKeys.remove")}
			</Button> : null}
		</div>
	</form>;
}
