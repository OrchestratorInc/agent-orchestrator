import { LoaderCircle } from "@/components/app-icons";
import { useEffect, useId, useRef, useState, type FormEvent } from "react";
import { useTranslation } from "react-i18next";
import { checkProviderKey, removeProviderKey, saveProviderKey, useInvalidateProviderKeys, useProviderKeys, type ProviderKey } from "../../hooks/useProviderKeys";
import { agentLabel } from "../../lib/agent-options";
import { Button } from "../ui/button";
import { Input } from "../ui/input";
import { DeviceEnvToggle, KEY_FIELD_CLASS, KeyStatusDot, savedKeySummary } from "./providerKeyUi";
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
		toolbar={<SettingsSearch aria-label={t("settings.apiKeys.search")} placeholder={t("settings.apiKeys.search")} value={query} onChange={setQuery}
			trailing={<span className="shrink-0 px-1 text-xs text-settings-muted tabular-nums">{t("settings.apiKeys.savedCount", { saved, total: all.length })}</span>} />}
	>
		{visible.length === 0 ? <p role="status" className="settings-content-row text-control text-settings-muted">{t("settings.apiKeys.noMatches")}</p> : null}
		{visible.map((key) => {
			const open = editing === key.name;
			// Row and editor share one block (no divider between them), so the
			// editor reads as part of its row without an extra surface tint.
			return <div key={key.name} data-editing={open ? "" : undefined}>
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
	// The row's button is hidden while its editor is open; when the editor
	// closes with focus inside it, focus comes back to the button.
	const editButtonRef = useRef<HTMLButtonElement>(null);
	const wasEditing = useRef(editing);
	useEffect(() => {
		if (wasEditing.current && !editing && (!document.activeElement || document.activeElement === document.body)) editButtonRef.current?.focus();
		wasEditing.current = editing;
	}, [editing]);
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
	// "Claude Code, memcode, Pi +9": who reads the key, on the label's line.
	const usedBy = agentNames.length <= 3 ? agentNames.join(", ") : `${agentNames.slice(0, 3).join(", ")} +${agentNames.length - 3}`;
	return <div className="settings-row-bar min-h-10 py-1.5!" data-key-status={key.configured ? "saved" : "unset"}>
		<div className="flex min-w-0 flex-1 items-baseline gap-2">
			<span className="shrink-0 text-sm leading-5 text-settings-label">{key.label}</span>
			<span className="min-w-0 truncate text-xs leading-4 text-settings-muted" title={agentNames.join(", ")}>
				<span className="sr-only">{t("settings.apiKeys.usedBy", { names: "" })}</span>{usedBy}
			</span>
		</div>
		{notice ? <span role={notice.tone === "error" ? "alert" : "status"} className={`max-w-[45%] truncate text-xs leading-4 ${notice.tone === "error" ? "text-error" : "text-success"}`} title={notice.text}>{notice.text}</span>
			: key.configured ? <SavedKeyBadge providerKey={key} />
			: null}
		{/* Outline buttons, like Log in again and Log out on a harness's Account card.
		    While the editor is open its own Cancel and Save replace them. */}
		{editing ? null : <div className="flex shrink-0 items-center gap-1.5">
			{key.configured && key.checkable ? <Button type="button" size="sm" variant="outline" disabled={checking} onClick={() => void check()}>
				{checking ? <LoaderCircle className="animate-spin" aria-hidden="true" /> : null}{t("settings.apiKeys.check")}
			</Button> : null}
			<Button ref={editButtonRef} type="button" size="sm" variant="outline" aria-expanded={false} aria-label={`${t(key.configured ? "settings.apiKeys.edit" : "settings.apiKeys.add")} · ${key.label}`} onClick={onEdit}>
				{t(key.configured ? "settings.apiKeys.edit" : "settings.apiKeys.add")}
			</Button>
		</div>}
	</div>;
}

/** Saved state at the row's trailing edge: a green dot and the key's last four. */
function SavedKeyBadge({ providerKey: key }: { providerKey: ProviderKey }) {
	const { t } = useTranslation();
	return <span className="flex shrink-0 items-center gap-1.5 text-xs leading-4 text-settings-muted" title={savedKeySummary(t, key)}>
		<KeyStatusDot configured />
		<span className="sr-only">{savedKeySummary(t, key)}</span>
		<span aria-hidden="true" className="font-mono tabular-nums">{key.hint ? `••••${key.hint}` : t("settings.apiKeys.saved")}</span>
		{key.device ? <span aria-hidden="true">· {t("settings.apiKeys.outsideAo")}</span> : null}
	</span>;
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

	// The key field and its two actions on one line; the device opt-in under
	// the field, starting at its left edge.
	return <form className="flex flex-col gap-2.5 px-(--settings-row-inset) pt-0.5 pb-3" onSubmit={(event) => void submit(event)} onKeyDown={(event) => { if (event.key === "Escape") { event.stopPropagation(); onDone(); } }}>
		<div className="flex items-center gap-2">
			<Input
				id={inputId}
				autoFocus
				aria-label={key.label}
				className={`${KEY_FIELD_CLASS} min-w-0 flex-1`}
				type="password"
				value={secret}
				disabled={busy}
				autoComplete="off"
				spellCheck={false}
				maxLength={16384}
				aria-invalid={error ? true : undefined}
				placeholder={key.configured ? t("settings.apiKeys.keepSaved") : key.name}
				onChange={(event) => { setSecret(event.target.value); setError(""); }}
			/>
			<Button type="button" variant="outline" disabled={busy} onClick={() => onDone()}>{t("settings.apiKeys.cancel")}</Button>
			<Button type="submit" disabled={busy || !changed || (!typed && !key.configured)}>
				{busy && !confirmRemove ? <LoaderCircle className="animate-spin" aria-hidden="true" /> : null}
				{t(typed && key.checkable ? "settings.apiKeys.checkAndSave" : "settings.apiKeys.save")}
			</Button>
		</div>
		{error ? <p role="alert" className="text-xs leading-4 text-error">{error}</p> : null}
		<div className="flex min-h-7 items-start gap-3">
			{key.deviceAvailable ? <DeviceEnvToggle checked={device} disabled={busy} onChange={setDevice} className="flex-1 pt-1" /> : <span className="flex-1" />}
			{key.configured ? <Button type="button" size="sm" variant="destructive-ghost" disabled={busy} onClick={() => void remove()}>
				{busy && confirmRemove ? <LoaderCircle className="animate-spin" aria-hidden="true" /> : null}
				{t(confirmRemove ? "settings.apiKeys.confirmRemove" : "settings.apiKeys.remove")}
			</Button> : null}
		</div>
	</form>;
}
