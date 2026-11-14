import { Pencil, Plus, RefreshCw, Trash2 } from "@/components/app-icons";
import { useCallback, useEffect, useState } from "react";
import { useTranslation } from "react-i18next";
import type { AoBridge } from "../../../preload";
import { aoBridge } from "../../lib/bridge";
import { ConfirmDialog } from "../ConfirmDialog";
import { Button } from "../ui/button";
import { CloseButton } from "../ui/close-button";
import {
	Dialog,
	DialogClose,
	DialogContent,
	DialogDescription,
	DialogTitle,
	settingsDialogBodyClass,
	settingsDialogContentClass,
	settingsDialogFooterClass,
	settingsDialogHeaderClass,
} from "../ui/dialog";
import { Input } from "../ui/input";
import { SettingsRow } from "./SettingsRow";
import { SettingsSection } from "./SettingsSection";

type VaultBridge = AoBridge["vault"];
type Item = Awaited<ReturnType<VaultBridge["list"]>>["items"][number];
type AuditEntry = Awaited<ReturnType<VaultBridge["audit"]>>[number];

const AUDIT_ROWS = 50;

function vaultError(reason: unknown, fallback: string): string {
	// Electron prefixes main-process errors with IPC plumbing; keep the reason.
	if (!(reason instanceof Error)) return fallback;
	const match = /Error invoking remote method '[^']+': (?:Error: )?(.*)$/.exec(reason.message);
	return match?.[1] || reason.message || fallback;
}

export function BrowserPasswordsSection({ titleHidden }: { titleHidden?: boolean }) {
	const { t } = useTranslation();
	const bridge = (aoBridge as Partial<AoBridge>).vault as VaultBridge | undefined;
	const [items, setItems] = useState<Item[]>([]);
	const [available, setAvailable] = useState(true);
	const [audit, setAudit] = useState<AuditEntry[]>([]);
	const [loading, setLoading] = useState(Boolean(bridge));
	const [error, setError] = useState("");
	const [editing, setEditing] = useState<Item | "new" | null>(null);
	const [deleting, setDeleting] = useState<Item | null>(null);
	const [deleteBusy, setDeleteBusy] = useState(false);
	const [deleteError, setDeleteError] = useState("");

	const refresh = useCallback(async () => {
		if (!bridge) return;
		try {
			const [list, entries] = await Promise.all([bridge.list(), bridge.audit()]);
			setItems(list.items);
			setAvailable(list.available);
			setAudit(entries.slice(0, AUDIT_ROWS));
			setError("");
		} catch (reason) {
			setError(vaultError(reason, t("settings.passwords.loadFailed")));
		} finally {
			setLoading(false);
		}
	}, [bridge, t]);

	useEffect(() => {
		void refresh();
	}, [refresh]);

	const confirmDelete = async () => {
		if (!bridge || !deleting) return;
		setDeleteBusy(true);
		try {
			await bridge.delete(deleting.id);
			setDeleting(null);
			setDeleteError("");
			await refresh();
		} catch (reason) {
			setDeleteError(vaultError(reason, t("settings.passwords.deleteFailed")));
		} finally {
			setDeleteBusy(false);
		}
	};

	return (
		<>
			<SettingsSection
				title={t("settings.passwords.saved")}
				sectionId="passwords"
				titleHidden={titleHidden}
				grouped
				action={
					<Button disabled={!bridge || !available} onClick={() => setEditing("new")} size="sm" type="button" variant="quiet">
						<Plus aria-hidden="true" className="size-icon-base" />
						{t("settings.passwords.add")}
					</Button>
				}
			>
				{error ? <div className="settings-row-bar text-xs text-error" role="alert">{error}</div> : null}
				{!available ? (
					<div className="settings-row-bar text-xs text-error" role="alert">{t("settings.passwords.keychainUnavailable")}</div>
				) : null}
				{loading ? (
					<div className="settings-row-bar text-sm text-settings-muted">{t("settings.passwords.loading")}</div>
				) : items.length === 0 ? (
					<div className="settings-row-bar text-sm text-settings-muted">{t("settings.passwords.empty")}</div>
				) : (
					items.map((item) => (
						<SettingsRow
							key={item.id}
							data-testid="vault-item"
							label={item.label}
							description={[
								item.username || t("settings.passwords.noUsername"),
								item.origins.join(", "),
								...(item.hasTotp ? [t("settings.passwords.twoFactor")] : []),
								item.allowedProjects.length ? item.allowedProjects.join(", ") : t("settings.passwords.allProjects"),
							].join(" · ")}
						>
							<Button
								aria-label={t("settings.passwords.edit", { label: item.label })}
								className="size-control-sm"
								onClick={() => setEditing(item)}
								size="none"
								type="button"
								variant="quiet"
							>
								<Pencil aria-hidden="true" className="size-icon-base" />
							</Button>
							<Button
								aria-label={t("settings.passwords.delete", { label: item.label })}
								className="size-control-sm"
								onClick={() => setDeleting(item)}
								size="none"
								type="button"
								variant="quiet"
							>
								<Trash2 aria-hidden="true" className="size-icon-base" />
							</Button>
						</SettingsRow>
					))
				)}
			</SettingsSection>
			<SettingsSection
				title={t("settings.passwords.activity")}
				sectionId="passwordActivity"
				grouped
				action={
					<Button disabled={!bridge} onClick={() => void refresh()} size="sm" type="button" variant="quiet">
						<RefreshCw aria-hidden="true" className="size-icon-base" />
						{t("settings.passwords.refresh")}
					</Button>
				}
			>
				{audit.length === 0 ? (
					<div className="settings-row-bar text-sm text-settings-muted">{t("settings.passwords.activityEmpty")}</div>
				) : (
					audit.map((entry, index) => (
						<SettingsRow
							key={`${entry.time}-${index}`}
							data-testid="vault-audit"
							label={`${entry.itemLabel ?? entry.itemId} · ${t(`settings.passwords.field.${entry.field}`)}`}
							description={[entry.origin, entry.sessionId, new Date(entry.time).toLocaleString(), ...(entry.reason ? [entry.reason] : [])].join(" · ")}
						>
							<span className={entry.outcome === "filled" ? "text-sm text-settings-muted" : "text-sm text-error"}>
								{t(`settings.passwords.outcome.${entry.outcome}`)}
							</span>
						</SettingsRow>
					))
				)}
			</SettingsSection>
			{editing && bridge ? (
				<VaultItemDialog
					bridge={bridge}
					item={editing === "new" ? null : editing}
					onClose={() => setEditing(null)}
					onSaved={() => {
						setEditing(null);
						void refresh();
					}}
				/>
			) : null}
			<ConfirmDialog
				open={deleting !== null}
				onOpenChange={(open) => {
					if (open || deleteBusy) return;
					setDeleting(null);
					setDeleteError("");
				}}
				title={deleting ? t("settings.passwords.deleteTitle", { label: deleting.label }) : ""}
				description={t("settings.passwords.deleteDescription")}
				confirmLabel={t("settings.passwords.deleteConfirm")}
				destructive
				busy={deleteBusy}
				error={deleteError || null}
				onConfirm={() => void confirmDelete()}
			/>
		</>
	);
}

function VaultItemDialog({
	bridge,
	item,
	onClose,
	onSaved,
}: {
	bridge: VaultBridge;
	item: Item | null;
	onClose: () => void;
	onSaved: () => void;
}) {
	const { t } = useTranslation();
	const [label, setLabel] = useState(item?.label ?? "");
	const [sites, setSites] = useState(item?.origins.join("\n") ?? "");
	const [username, setUsername] = useState(item?.username ?? "");
	const [password, setPassword] = useState("");
	const [totpSeed, setTotpSeed] = useState("");
	const [removeTotp, setRemoveTotp] = useState(false);
	const [projects, setProjects] = useState(item?.allowedProjects.join(", ") ?? "");
	const [busy, setBusy] = useState(false);
	const [error, setError] = useState("");
	const origins = sites.split(/[\n,]/).map((site) => site.trim()).filter(Boolean);
	const canSave = origins.length > 0 && (item !== null || password.length > 0) && !busy;

	const save = async () => {
		setBusy(true);
		try {
			await bridge.save({
				...(item ? { id: item.id } : {}),
				label,
				origins,
				username,
				password,
				...(removeTotp ? { totpSeed: null } : totpSeed.trim() ? { totpSeed } : {}),
				allowedProjects: projects.split(",").map((project) => project.trim()).filter(Boolean),
			});
			onSaved();
		} catch (reason) {
			setError(vaultError(reason, t("settings.passwords.saveFailed")));
		} finally {
			setBusy(false);
		}
	};

	const field = "grid gap-1.5 text-xs text-muted-foreground";
	return (
		<Dialog open onOpenChange={(open) => !open && !busy && onClose()}>
			<DialogContent className={settingsDialogContentClass} showCloseButton={false}>
				<DialogClose asChild>
					<CloseButton aria-label={t("common.close")} disabled={busy} floating />
				</DialogClose>
				<div className={settingsDialogHeaderClass}>
					<DialogTitle className="settings-dialog-title">
						{t(item ? "settings.passwords.dialogEditTitle" : "settings.passwords.dialogAddTitle")}
					</DialogTitle>
					<DialogDescription className="text-sm text-settings-muted">{t("settings.passwords.dialogDescription")}</DialogDescription>
				</div>
				<form
					className={settingsDialogBodyClass}
					id="vault-item-form"
					onSubmit={(event) => {
						event.preventDefault();
						if (canSave) void save();
					}}
				>
					<label className={field}>
						{t("settings.passwords.sites")}
						<textarea
							autoFocus
							className="min-h-16 rounded-md border border-(--color-border-settings-input) bg-(--color-bg-settings-input) px-3 py-2 font-mono text-sm text-settings-label outline-none placeholder:text-settings-muted"
							onChange={(event) => setSites(event.target.value)}
							placeholder={t("settings.passwords.sitesPlaceholder")}
							spellCheck={false}
							value={sites}
						/>
						<span>{t("settings.passwords.sitesHint")}</span>
					</label>
					<label className={field}>
						{t("settings.passwords.username")}
						<Input autoComplete="off" onChange={(event) => setUsername(event.target.value)} value={username} />
					</label>
					<label className={field}>
						{t("settings.passwords.password")}
						<Input
							autoComplete="new-password"
							onChange={(event) => setPassword(event.target.value)}
							placeholder={item ? t("settings.passwords.passwordUnchanged") : undefined}
							type="password"
							value={password}
						/>
					</label>
					<label className={field}>
						{t("settings.passwords.totp")}
						<Input
							autoComplete="off"
							className="font-mono"
							disabled={removeTotp}
							onChange={(event) => setTotpSeed(event.target.value)}
							placeholder={item?.hasTotp ? t("settings.passwords.totpUnchanged") : t("settings.passwords.totpPlaceholder")}
							spellCheck={false}
							type="password"
							value={totpSeed}
						/>
					</label>
					{item?.hasTotp ? (
						<label className="flex items-center gap-2 text-sm text-settings-label">
							<input checked={removeTotp} onChange={(event) => setRemoveTotp(event.target.checked)} type="checkbox" />
							{t("settings.passwords.totpRemove")}
						</label>
					) : null}
					<label className={field}>
						{t("settings.passwords.label")}
						<Input maxLength={64} onChange={(event) => setLabel(event.target.value)} placeholder={t("settings.passwords.labelPlaceholder")} value={label} />
					</label>
					<label className={field}>
						{t("settings.passwords.projects")}
						<Input onChange={(event) => setProjects(event.target.value)} placeholder={t("settings.passwords.projectsPlaceholder")} value={projects} />
						<span>{t("settings.passwords.projectsHint")}</span>
					</label>
					{error ? <p className="text-xs text-error" role="alert">{error}</p> : null}
				</form>
				<div className={settingsDialogFooterClass}>
					<DialogClose asChild>
						<Button disabled={busy} type="button" variant="secondary">{t("confirm.cancel")}</Button>
					</DialogClose>
					<Button disabled={!canSave} form="vault-item-form" type="submit" variant="primary">
						{t("settings.passwords.save")}
					</Button>
				</div>
			</DialogContent>
		</Dialog>
	);
}
