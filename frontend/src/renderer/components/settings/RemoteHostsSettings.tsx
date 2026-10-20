import { Pencil, Server, Trash2 } from "lucide-react";
import { useEffect, useRef, useState, type FormEvent } from "react";
import { useTranslation } from "react-i18next";
import { aoBridge } from "../../lib/bridge";
import { disconnectHost } from "../../lib/host-clients";
import { requestRemoteHostsRefresh } from "../../hooks/useRemoteHosts";
import { useCloudLocalAuth } from "../../hooks/useCloudLocalAuth";
import { useCloudSession } from "../../lib/cloud-session";
import { deleteAccountRemoteHost, saveAccountRemoteHost } from "../../lib/account-remote-hosts";
import { useSettings } from "../../hooks/useSettings";
import { useLocalSignInDialogStore } from "../../stores/local-signin-dialog-store";
import { useUiStore } from "../../stores/ui-store";
import { ConfirmDialog } from "../ConfirmDialog";
import { Button } from "../ui/button";
import { Input } from "../ui/input";
import { Switch } from "../ui/switch";
import { SettingsRow } from "./SettingsRow";
import { SettingsSection } from "./SettingsSection";

type SavedHost = { hostId: string; label: string; url: string };

/** Row actions are quiet: ghost, muted until hovered. */
const GHOST_ACTION = "text-settings-muted hover:text-settings-label";

export function RemoteHostsSettings({ titleHidden }: { titleHidden?: boolean }) {
	const { t } = useTranslation();
	const enabled = useUiStore((state) => state.remoteHosts);
	const setEnabled = useUiStore((state) => state.setRemoteHosts);
	const { status, signIn } = useCloudSession();
	const { settings } = useSettings();
	const cloudBaseUrl = settings?.cloudControlPlaneUrl ?? "";
	const { available: localAuthAvailable } = useCloudLocalAuth();
	const openLocalSignIn = useLocalSignInDialogStore((state) => state.openDialog);
	const [saved, setSaved] = useState<SavedHost[]>([]);
	const [loading, setLoading] = useState(true);
	const [loadError, setLoadError] = useState("");
	const [label, setLabel] = useState("");
	const [url, setUrl] = useState("");
	const [password, setPassword] = useState("");
	const [editing, setEditing] = useState<SavedHost | null>(null);
	const [busy, setBusy] = useState(false);
	const [formError, setFormError] = useState("");
	const [confirmingRemoval, setConfirmingRemoval] = useState<SavedHost | null>(null);
	const [removeBusy, setRemoveBusy] = useState(false);
	const [removeError, setRemoveError] = useState("");
	const formRef = useRef<HTMLFormElement>(null);

	const load = async () => {
		setLoading(true);
		setLoadError("");
		try {
			setSaved(await aoBridge.remotes.list());
		} catch (cause) {
			setLoadError(cause instanceof Error ? cause.message : t("remote.loadHostsFailed"));
		} finally {
			setLoading(false);
		}
	};

	useEffect(() => {
		if (status === "authenticated") void load();
		else setSaved([]);
		// The bridge is fixed for the lifetime of this Settings page.
		// eslint-disable-next-line react-hooks/exhaustive-deps
	}, [status]);

	const resetForm = () => {
		setEditing(null);
		setLabel("");
		setUrl("");
		setPassword("");
		setFormError("");
	};

	const save = async (event: FormEvent<HTMLFormElement>) => {
		event.preventDefault();
		const nextLabel = label.trim();
		const nextUrl = url.trim();
		if (!nextLabel || !nextUrl || (!editing?.hostId && !password)) return;
		setBusy(true);
		setFormError("");
		try {
			// Old saved records have no identity, so re-pair them through add.
			const health = editing?.hostId
				? await aoBridge.remotes.update(editing.url, {
					label: nextLabel,
					url: nextUrl,
					...(password ? { password } : {}),
				})
				: await aoBridge.remotes.add({ label: nextLabel, url: nextUrl, password });
			if (health === "offline") throw new Error(t("remote.hostOffline"));
			if (health === "unauthorized") throw new Error(t("remote.hostUnauthorized"));
			if (health === "not-a-daemon") throw new Error(t("remote.hostNotDaemon"));
			if (health === "incompatible") throw new Error(t("remote.hostIncompatible"));
			if (editing?.hostId) await disconnectHost(editing.hostId);
			if (editing && !editing.hostId && editing.url !== nextUrl) await aoBridge.remotes.remove(editing.url);
			const current = (await aoBridge.remotes.list()).find((item) => item.url === nextUrl);
			if (current?.hostId && cloudBaseUrl) {
				const token = await aoBridge.remotes.issueAccountToken(current.url);
				await saveAccountRemoteHost(cloudBaseUrl, { hostId: current.hostId, label: current.label, url: current.url, token });
			}
			await load();
			resetForm();
			if (!enabled) setEnabled(true);
			requestRemoteHostsRefresh();
		} catch (cause) {
			setFormError(cause instanceof Error ? cause.message : t("remote.addHostFailed"));
		} finally {
			setBusy(false);
		}
	};

	const remove = async () => {
		if (!confirmingRemoval) return;
		setRemoveBusy(true);
		setRemoveError("");
		try {
			if (confirmingRemoval.hostId && cloudBaseUrl) await deleteAccountRemoteHost(cloudBaseUrl, confirmingRemoval.hostId);
			await aoBridge.remotes.remove(confirmingRemoval.url);
			if (confirmingRemoval.hostId) await disconnectHost(confirmingRemoval.hostId);
			await load();
			setConfirmingRemoval(null);
			requestRemoteHostsRefresh();
		} catch (cause) {
			setRemoveError(cause instanceof Error ? cause.message : t("remote.removeHostFailed"));
		} finally {
			setRemoveBusy(false);
		}
	};

	if (status !== "authenticated") return (
		<SettingsSection title={t("settings.remoteHosts")} sectionId="remoteHosts" titleHidden={titleHidden} grouped>
			<SettingsRow label={t("remote.aoCloud")} description={t("remote.signInDescription")}>{status === "unauthenticated" ? (
				<Button className={GHOST_ACTION} onClick={() => localAuthAvailable ? openLocalSignIn() : signIn()} size="sm" type="button" variant="ghost">
					{t("shell.signIn")}
				</Button>
			) : null}</SettingsRow>
		</SettingsSection>
	);

	return <>
		<SettingsSection title={t("settings.remoteHosts")} sectionId="remoteHosts" titleHidden={titleHidden} grouped>
			<SettingsRow
				label={t("remote.enable")}
				description={
					<>
						{t("remote.enableDescription")}
						<span className="mt-1 block">{t("remote.connectionDescription")}</span>
					</>
				}
			>
				<Switch aria-label={t("remote.enable")} checked={enabled} onCheckedChange={setEnabled} />
			</SettingsRow>
		</SettingsSection>

		<SettingsSection title={t("remote.savedHosts")} grouped>
			{loading && saved.length === 0 ? <div className="settings-row-bar text-sm text-settings-muted" role="status">{t("remote.loadingHosts")}</div> : null}
			{!loading && saved.length === 0 && !loadError ? <div className="settings-row-bar text-pretty text-sm text-settings-muted">{t("remote.emptyHosts")}</div> : null}
			{saved.map((host) => (
				<SettingsRow
					key={host.url}
					icon={Server}
					label={host.label}
					description={host.hostId ? host.url : (
						<>
							{host.url}
							<span className="mt-0.5 block text-warning">{t("remote.repairRequired")}</span>
						</>
					)}
				>
					<div className="flex items-center gap-1">
						<Button aria-label={t("remote.editHost", { label: host.label })} className={GHOST_ACTION} disabled={busy || removeBusy} onClick={() => {
							setEditing(host);
							setLabel(host.label);
							setUrl(host.url);
							setPassword("");
							setFormError("");
							formRef.current?.scrollIntoView?.({ block: "nearest" });
						}} size="icon-sm" type="button" variant="ghost"><Pencil aria-hidden="true" className="size-icon-base" /></Button>
						<Button aria-label={t("remote.removeHost", { label: host.label })} className={GHOST_ACTION} disabled={busy || removeBusy} onClick={() => {
							setConfirmingRemoval(host);
							setRemoveError("");
						}} size="icon-sm" type="button" variant="ghost"><Trash2 aria-hidden="true" className="size-icon-base" /></Button>
					</div>
				</SettingsRow>
			))}
			{loadError ? (
				<SettingsRow label={t("remote.savedHosts")} description={<span className="text-error" role="alert">{loadError}</span>}>
					<Button className={GHOST_ACTION} onClick={() => void load()} size="sm" type="button" variant="ghost">{t("notify.retry")}</Button>
				</SettingsRow>
			) : null}
		</SettingsSection>

		{/* The form wraps the whole section so its rows stay direct children of
		    the grouped card and pick up the card's dividers. */}
		<form className="flex w-full flex-col" onSubmit={(event) => void save(event)} ref={formRef}>
			<SettingsSection title={editing ? t("remote.editHost", { label: editing.label }) : t("remote.addHost")} grouped>
				<p className="settings-content-row text-pretty text-control leading-4 text-settings-muted">{t("remote.setupHint")}</p>
				<SettingsRow label={t("remote.name")}><Input aria-label={t("remote.name")} className="w-80 max-w-full" disabled={busy} maxLength={64} onChange={(event) => setLabel(event.target.value)} required value={label} /></SettingsRow>
				<SettingsRow label={t("mobile.address")}><Input aria-label={t("mobile.address")} className="w-80 max-w-full" disabled={busy} inputMode="url" onChange={(event) => setUrl(event.target.value)} placeholder={t("remote.addressPlaceholder")} required value={url} /></SettingsRow>
				<SettingsRow label={t("remote.connectionPassword")}><Input aria-label={t("remote.connectionPassword")} autoComplete="off" className="w-80 max-w-full" disabled={busy} onChange={(event) => setPassword(event.target.value)} placeholder={editing?.hostId ? t("remote.keepPassword") : undefined} required={!editing?.hostId} type="password" value={password} /></SettingsRow>
				<div className="settings-content-row flex items-center justify-end gap-2">
					{formError ? <p className="mr-auto min-w-0 text-xs text-error" role="alert">{formError}</p> : null}
					{editing ? <Button className={GHOST_ACTION} disabled={busy} onClick={resetForm} size="sm" type="button" variant="ghost">{t("confirm.cancel")}</Button> : null}
					<Button className={GHOST_ACTION} disabled={busy || !label.trim() || !url.trim() || (!editing?.hostId && !password)} size="sm" type="submit" variant="ghost">{busy ? t("remote.saving") : editing ? t("remote.saveHost") : t("remote.addHost")}</Button>
				</div>
			</SettingsSection>
		</form>

		<ConfirmDialog
			busy={removeBusy}
			confirmLabel={t("shell.remove")}
			description={t("remote.removeDescription")}
			destructive
			error={removeError || null}
			onConfirm={() => void remove()}
			onOpenChange={(open) => { if (!open && !removeBusy) setConfirmingRemoval(null); }}
			open={confirmingRemoval !== null}
			title={t("remote.removeTitle", { label: confirmingRemoval?.label })}
		/>
	</>;
}
