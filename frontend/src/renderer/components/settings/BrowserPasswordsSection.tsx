import { Pencil, Plus, RefreshCw, Trash2 } from "@/components/app-icons";
import { useQuery } from "@tanstack/react-query";
import { useCallback, useEffect, useId, useMemo, useRef, useState, type ReactNode } from "react";
import { useTranslation } from "react-i18next";
import type { AoBridge } from "../../../preload";
import { workspaceQueryOptions } from "../../hooks/useWorkspaceQuery";
import { aoBridge } from "../../lib/bridge";
import { CLOUD_PROJECT_KIND } from "../../types/workspace";
import { ConfirmDialog } from "../ConfirmDialog";
import { Button } from "../ui/button";
import { Checkbox } from "../ui/checkbox";
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
import { Label } from "../ui/label";
import { SettingsRow } from "./SettingsRow";
import { SettingsSection } from "./SettingsSection";

type VaultBridge = AoBridge["vault"];
type Item = Awaited<ReturnType<VaultBridge["list"]>>["items"][number];
type AuditEntry = Awaited<ReturnType<VaultBridge["audit"]>>[number];

const AUDIT_ROWS = 50;

type ProjectOption = { id: string; name: string };

/**
 * Projects whose sessions use this computer's Browser panel: local ones only.
 * Cloud and remote-host projects run their browsers elsewhere.
 */
function useLocalProjects(): ProjectOption[] {
	const { data } = useQuery(workspaceQueryOptions);
	return useMemo(
		() => (data ?? []).filter((project) => !project.hostId && project.kind !== CLOUD_PROJECT_KIND).map(({ id, name }) => ({ id, name })),
		[data],
	);
}

function vaultError(reason: unknown, fallback: string): string {
	// Electron prefixes main-process errors with IPC plumbing; keep the reason.
	if (!(reason instanceof Error)) return fallback;
	const match = /Error invoking remote method '[^']+': (?:Error: )?(.*)$/.exec(reason.message);
	return match?.[1] || reason.message || fallback;
}

export function BrowserPasswordsSection({ titleHidden }: { titleHidden?: boolean }) {
	const { t } = useTranslation();
	const bridge = (aoBridge as Partial<AoBridge>).vault as VaultBridge | undefined;
	const projects = useLocalProjects();
	const projectName = (id: string) => projects.find((project) => project.id === id)?.name ?? id;
	const [items, setItems] = useState<Item[]>([]);
	const [available, setAvailable] = useState(true);
	const [audit, setAudit] = useState<AuditEntry[]>([]);
	const [loading, setLoading] = useState(Boolean(bridge));
	const [error, setError] = useState("");
	const [editing, setEditing] = useState<Item | "new" | null>(null);
	const [deleting, setDeleting] = useState<Item | null>(null);
	const [deleteBusy, setDeleteBusy] = useState(false);
	const [deleteError, setDeleteError] = useState("");
	// Only the newest refresh may update state; an older one can resolve later.
	const latestRefresh = useRef(0);

	const refresh = useCallback(async () => {
		if (!bridge) return;
		const requestId = ++latestRefresh.current;
		try {
			const [list, entries] = await Promise.all([bridge.list(), bridge.audit()]);
			if (requestId !== latestRefresh.current) return;
			setItems(list.items);
			setAvailable(list.available);
			setAudit(entries.slice(0, AUDIT_ROWS));
			setError("");
		} catch (reason) {
			if (requestId !== latestRefresh.current) return;
			setError(vaultError(reason, t("settings.passwords.loadFailed")));
		} finally {
			if (requestId === latestRefresh.current) setLoading(false);
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
								item.allowedProjects.length ? item.allowedProjects.map(projectName).join(", ") : t("settings.passwords.allProjects"),
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
					projects={projects}
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
	projects,
	item,
	onClose,
	onSaved,
}: {
	bridge: VaultBridge;
	projects: ProjectOption[];
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
	const [allowedProjects, setAllowedProjects] = useState<string[]>(item?.allowedProjects ?? []);
	// A saved project that no longer exists stays listed so it can be unticked.
	const projectOptions = [
		...projects,
		...allowedProjects.filter((id) => !projects.some((project) => project.id === id)).map((id) => ({ id, name: id })),
	];
	const projectsLabelId = useId();
	const projectsHintId = useId();
	const removeTotpId = useId();
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
				allowedProjects: projectOptions.map((project) => project.id).filter((id) => allowedProjects.includes(id)),
			});
			onSaved();
		} catch (reason) {
			setError(vaultError(reason, t("settings.passwords.saveFailed")));
		} finally {
			setBusy(false);
		}
	};

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
					<VaultField label={t("settings.passwords.sites")} hint={t("settings.passwords.sitesHint")}>
						{(control) => (
							<textarea
								{...control}
								autoFocus
								className="min-h-16 rounded-md border border-(--color-border-settings-input) bg-(--color-bg-settings-input) px-3 py-2 font-mono text-sm text-settings-label outline-none placeholder:text-settings-muted"
								onChange={(event) => setSites(event.target.value)}
								placeholder={t("settings.passwords.sitesPlaceholder")}
								spellCheck={false}
								value={sites}
							/>
						)}
					</VaultField>
					<VaultField label={t("settings.passwords.username")}>
						{(control) => <Input {...control} autoComplete="off" onChange={(event) => setUsername(event.target.value)} value={username} />}
					</VaultField>
					<VaultField label={t("settings.passwords.password")}>
						{(control) => (
							<Input
								{...control}
								autoComplete="new-password"
								onChange={(event) => setPassword(event.target.value)}
								placeholder={item ? t("settings.passwords.passwordUnchanged") : undefined}
								type="password"
								value={password}
							/>
						)}
					</VaultField>
					<VaultField label={t("settings.passwords.totp")}>
						{(control) => (
							<Input
								{...control}
								autoComplete="off"
								className="font-mono"
								disabled={removeTotp}
								onChange={(event) => setTotpSeed(event.target.value)}
								placeholder={item?.hasTotp ? t("settings.passwords.totpUnchanged") : t("settings.passwords.totpPlaceholder")}
								spellCheck={false}
								type="password"
								value={totpSeed}
							/>
						)}
					</VaultField>
					{item?.hasTotp ? (
						<div className="flex items-center gap-2">
							<Checkbox checked={removeTotp} id={removeTotpId} onCheckedChange={(checked) => setRemoveTotp(checked === true)} />
							<Label className="text-sm font-normal text-settings-label" htmlFor={removeTotpId}>
								{t("settings.passwords.totpRemove")}
							</Label>
						</div>
					) : null}
					<VaultField label={t("settings.passwords.label")}>
						{(control) => <Input {...control} maxLength={64} onChange={(event) => setLabel(event.target.value)} placeholder={t("settings.passwords.labelPlaceholder")} value={label} />}
					</VaultField>
					<div aria-describedby={projectsHintId} aria-labelledby={projectsLabelId} className="grid gap-1.5" role="group">
						<span className="text-xs text-muted-foreground" id={projectsLabelId}>
							{t("settings.passwords.projects")}
						</span>
						{projectOptions.map((project) => {
							const optionId = `${projectsLabelId}-${project.id}`;
							return (
								<div className="flex items-center gap-2" key={project.id}>
									<Checkbox
										checked={allowedProjects.includes(project.id)}
										id={optionId}
										onCheckedChange={(checked) =>
											setAllowedProjects((current) =>
												checked === true ? [...current, project.id] : current.filter((id) => id !== project.id),
											)
										}
									/>
									<Label className="text-sm font-normal text-settings-label" htmlFor={optionId}>
										{project.name}
									</Label>
								</div>
							);
						})}
						<span className="text-xs text-muted-foreground" id={projectsHintId}>
							{t("settings.passwords.projectsHint")}
						</span>
					</div>
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

/** One labelled dialog field: an explicit label[for] and, when given, a hint the control is described by. */
function VaultField({
	label,
	hint,
	children,
}: {
	label: string;
	hint?: string;
	children: (control: { id: string; "aria-describedby"?: string }) => ReactNode;
}) {
	const id = useId();
	const hintId = `${id}-hint`;
	return (
		<div className="grid gap-1.5">
			<Label className="text-xs font-normal text-muted-foreground" htmlFor={id}>
				{label}
			</Label>
			{children({ id, ...(hint ? { "aria-describedby": hintId } : {}) })}
			{hint ? (
				<span className="text-xs text-muted-foreground" id={hintId}>
					{hint}
				</span>
			) : null}
		</div>
	);
}
