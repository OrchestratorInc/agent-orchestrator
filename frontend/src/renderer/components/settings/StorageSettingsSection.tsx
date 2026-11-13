import { useEffect, useState } from "react";
import { useTranslation } from "react-i18next";
import { aoBridge } from "../../lib/bridge";
import {
	useStorageClean,
	useStorageReport,
	useStorageScan,
	useUpdateStorageSettings,
	type StorageCleanKind,
	type StorageCleanResult,
	type StorageReport,
} from "../../hooks/useStorage";
import { ConfirmDialog } from "../ConfirmDialog";
import { Button } from "../ui/button";
import { Switch } from "../ui/switch";
import { SettingsOptionMenu, type SettingsOption } from "./SettingsOptionMenu";
import { SettingsRow } from "./SettingsRow";
import { SettingsSection } from "./SettingsSection";

type Category = StorageReport["categories"][number]["category"];

const CATEGORY_LABEL_KEYS = {
	worktrees: "settings.storage.category.worktrees",
	database: "settings.storage.category.database",
	db_backups: "settings.storage.category.dbBackups",
	harnesses: "settings.storage.category.harnesses",
	browser_data: "settings.storage.category.browserData",
	logs: "settings.storage.category.logs",
	dev_profiles: "settings.storage.category.devProfiles",
	dependency_templates: "settings.storage.category.dependencyTemplates",
	other: "settings.storage.category.other",
} as const satisfies Record<Category, string>;

/** What "Clean up" on a category runs; categories AO cannot clean have none. */
const CATEGORY_CLEAN_KINDS: Partial<Record<Category, StorageCleanKind[]>> = {
	worktrees: ["offload", "ended_worktrees", "orphan_worktrees"],
	database: ["database"],
	db_backups: ["db_backups"],
	logs: ["logs"],
	dev_profiles: ["dev_profiles"],
	dependency_templates: ["dependency_templates"],
	other: ["trash"],
};

const BYTE_UNITS = ["byte", "kilobyte", "megabyte", "gigabyte", "terabyte"] as const;

/** Sizes in binary steps, worded by the user's locale ("1.4 GB", "1,4 Go"). */
export function formatStorageBytes(bytes: number, locale: string): string {
	let value = Math.max(0, bytes);
	let unit = 0;
	while (value >= 1024 && unit < BYTE_UNITS.length - 1) {
		value /= 1024;
		unit++;
	}
	return new Intl.NumberFormat(locale, {
		style: "unit",
		unit: BYTE_UNITS[unit],
		unitDisplay: "short",
		maximumFractionDigits: unit > 0 && value < 10 ? 1 : 0,
	}).format(value);
}

const OFFLOAD_HOURS = [12, 24, 48, 72, 168];
const ENDED_WORKTREE_DAYS = [1, 3, 7, 14, 30];

function withCurrent(choices: number[], current: number): number[] {
	return choices.includes(current) ? choices : [...choices, current].sort((a, b) => a - b);
}

type PendingClean = { kinds: StorageCleanKind[]; preview: StorageCleanResult };

export function StorageSettingsSection({ titleHidden }: { titleHidden?: boolean }) {
	const { t, i18n } = useTranslation();
	const { report, isLoading, error } = useStorageReport();
	const { scan } = useStorageScan();
	const cleaner = useStorageClean();
	const [pending, setPending] = useState<PendingClean | null>(null);
	const [notice, setNotice] = useState<string | null>(null);
	const [browserCache, setBrowserCache] = useState<number | null>(null);
	const [clearingCache, setClearingCache] = useState(false);
	const size = (bytes: number) => formatStorageBytes(bytes, i18n.language);

	useEffect(() => {
		let active = true;
		aoBridge.storage
			.browserCacheSize()
			.then((bytes) => {
				if (active) setBrowserCache(bytes);
			})
			.catch(() => undefined);
		return () => {
			active = false;
		};
	}, []);

	const preview = async (kinds: StorageCleanKind[]) => {
		setNotice(null);
		cleaner.reset();
		try {
			const result = await cleaner.clean({ kinds, dryRun: true });
			if (result.actions.length === 0) {
				setNotice(t("settings.storage.nothing"));
				return;
			}
			setPending({ kinds, preview: result });
		} catch {
			// The hook keeps the error for the row to show.
		}
	};

	const confirm = async () => {
		if (!pending) return;
		try {
			await cleaner.clean({ kinds: pending.kinds, dryRun: false });
			setPending(null);
		} catch {
			// Shown in the dialog.
		}
	};

	const clearBrowserCache = async () => {
		setClearingCache(true);
		try {
			await aoBridge.storage.clearBrowserCache();
			setBrowserCache(await aoBridge.storage.browserCacheSize());
		} finally {
			setClearingCache(false);
		}
	};

	const offloaded = (report?.worktrees ?? []).filter((wt) => wt.offloaded);
	const restoreFailure = (report?.worktrees ?? []).find((wt) => wt.restoreError);
	const disk = report?.disk;
	const diskNote =
		disk?.level === "critical" ? (
			<span role="alert" className="text-error">{t("settings.storage.diskCritical")}</span>
		) : disk?.level === "low" ? (
			<span className="text-warning">{t("settings.storage.diskLow")}</span>
		) : undefined;

	const busy = report?.scanning || report?.cleaning || cleaner.pending;
	const status = report?.cleaning
		? t("settings.storage.cleaning")
		: report?.scanning || isLoading
			? t("settings.storage.measuring")
			: undefined;

	return (
		<>
			<SettingsSection
				title={t("settings.storage")}
				sectionId="storage"
				titleHidden={titleHidden}
				grouped
				action={
					<Button variant="quiet" size="sm" onClick={scan} disabled={Boolean(report?.scanning)}>
						{t("settings.storage.measure")}
					</Button>
				}
			>
				{disk && disk.totalBytes > 0 ? (
					<SettingsRow label={t("settings.storage.disk")} description={diskNote}>
						<span className="text-sm tabular-nums text-settings-muted">
							{t("settings.storage.diskFree", { free: size(disk.freeBytes), total: size(disk.totalBytes) })}
						</span>
					</SettingsRow>
				) : null}
				{error ? (
					<SettingsRow label={t("settings.storage")} description={<span role="alert" className="text-error">{error}</span>} />
				) : null}
				{status && !report?.scannedAt ? <SettingsRow label={status} /> : null}
				{(report?.categories ?? [])
					.filter((category) => category.bytes > 0)
					.map((category) => {
						const kinds = CATEGORY_CLEAN_KINDS[category.category];
						const canClean = Boolean(kinds) && category.reclaimableBytes > 0;
						const cache = category.category === "browser_data" && browserCache ? browserCache : 0;
						const description = canClean
							? t("settings.storage.canFree", { size: size(category.reclaimableBytes) })
							: cache > 0
								? t("settings.storage.browserCache", { size: size(cache) })
								: undefined;
						return (
							<SettingsRow key={category.category} label={t(CATEGORY_LABEL_KEYS[category.category])} description={description}>
								<div className="flex items-center gap-3">
									<span className="text-sm tabular-nums text-settings-muted">{size(category.bytes)}</span>
									{canClean && kinds ? (
										<Button variant="ghost" size="sm" disabled={Boolean(busy)} onClick={() => void preview(kinds)}>
											{t("settings.storage.cleanUp")}
										</Button>
									) : null}
									{cache > 0 ? (
										<Button variant="ghost" size="sm" disabled={clearingCache} onClick={() => void clearBrowserCache()}>
											{t("settings.storage.clearCache")}
										</Button>
									) : null}
								</div>
							</SettingsRow>
						);
					})}
				{report?.scannedAt ? (
					<SettingsRow
						label={t("settings.storage.total")}
						description={
							cleaner.error && !pending ? (
								<span role="alert" className="text-error">{cleaner.error}</span>
							) : (
								status ??
								notice ??
								(report.lastClean && !report.lastClean.running
									? t("settings.storage.lastClean", { size: size(report.lastClean.freedBytes) })
									: report.reclaimableBytes > 0
										? t("settings.storage.canFree", { size: size(report.reclaimableBytes) })
										: undefined)
							)
						}
					>
						<div className="flex items-center gap-3">
							<span className="text-sm tabular-nums text-settings-label">{size(report.totalBytes)}</span>
							<Button variant="secondary" size="sm" disabled={Boolean(busy) || report.reclaimableBytes === 0} onClick={() => void preview([])}>
								{t("settings.storage.cleanUp")}
							</Button>
						</div>
					</SettingsRow>
				) : null}
				{offloaded.length > 0 ? (
					<SettingsRow
						label={t("settings.storage.offloaded.label")}
						description={
							restoreFailure ? (
								<span className="text-error">{t("settings.storage.restoreFailed", { session: restoreFailure.sessionId ?? "", error: restoreFailure.restoreError ?? "" })}</span>
							) : (
								t("settings.storage.offloaded.description")
							)
						}
					>
						<span className="text-sm tabular-nums text-settings-muted">
							{t("settings.storage.offloaded.value", { sessions: offloaded.length, size: size(offloaded.reduce((sum, wt) => sum + wt.offloadedBytes, 0)) })}
						</span>
					</SettingsRow>
				) : null}
			</SettingsSection>
			{report ? <StorageCleanupSettings report={report} /> : null}
			<ConfirmDialog
				open={pending !== null}
				title={t("settings.storage.confirmTitle", { size: size(pending?.preview.freedBytes ?? 0) })}
				description={t("settings.storage.confirmDescription", { items: pending?.preview.actions.length ?? 0 })}
				confirmLabel={t("settings.storage.confirm")}
				destructive
				busy={cleaner.pending}
				error={pending ? (cleaner.error ?? null) : null}
				onConfirm={() => void confirm()}
				onOpenChange={(open) => {
					if (!open) setPending(null);
				}}
			/>
		</>
	);
}

/** The automatic cleanup rules. Each row saves on its own, like General. */
function StorageCleanupSettings({ report }: { report: StorageReport }) {
	const { t } = useTranslation();
	const { update, saving, error } = useUpdateStorageSettings();
	const settings = report.settings;
	const autoBlocked = !report.autoCleanAllowed;
	const hours: SettingsOption<string>[] = withCurrent(OFFLOAD_HOURS, settings.offloadIdleHours).map((value) => ({
		value: String(value),
		label: t("settings.storage.hours", { hours: value }),
	}));
	const days: SettingsOption<string>[] = [
		{ value: "0", label: t("settings.storage.never") },
		...withCurrent(ENDED_WORKTREE_DAYS, settings.endedWorktreeDays)
			.filter((value) => value > 0)
			.map((value) => ({ value: String(value), label: t("settings.storage.days", { days: value }) })),
	];
	const hasDevProfiles = report.categories.some((category) => category.category === "dev_profiles" && category.bytes > 0);
	return (
		<SettingsSection title={t("settings.storage.cleanup")} sectionId="storageCleanup" grouped>
			<SettingsRow
				label={t("settings.storage.autoClean.label")}
				description={
					error ? (
						<span role="alert" className="text-error">{error}</span>
					) : autoBlocked ? (
						t("settings.storage.autoClean.blocked")
					) : (
						t("settings.storage.autoClean.description")
					)
				}
			>
				<Switch
					aria-label={t("settings.storage.autoClean.label")}
					checked={settings.autoClean && !autoBlocked}
					disabled={saving || autoBlocked}
					onCheckedChange={(checked) => update({ autoClean: checked })}
				/>
			</SettingsRow>
			<SettingsRow label={t("settings.storage.offload.label")} description={t("settings.storage.offload.description")}>
				<Switch
					aria-label={t("settings.storage.offload.label")}
					checked={settings.offloadEnabled}
					disabled={saving}
					onCheckedChange={(checked) => update({ offloadEnabled: checked })}
				/>
			</SettingsRow>
			{settings.offloadEnabled ? (
				<SettingsRow
					label={t("settings.storage.offloadIdle.label")}
					description={t("settings.storage.offloadIdle.description", {
						noPr: settings.offloadNoPrGraceHours,
						merged: settings.offloadMergedIdleMinutes,
					})}
				>
					<SettingsOptionMenu
						aria-label={t("settings.storage.offloadIdle.label")}
						value={String(settings.offloadIdleHours)}
						options={hours}
						disabled={saving}
						onChange={(next) => update({ offloadIdleHours: Number(next) })}
					/>
				</SettingsRow>
			) : null}
			<SettingsRow label={t("settings.storage.endedWorktrees.label")} description={t("settings.storage.endedWorktrees.description")}>
				<SettingsOptionMenu
					aria-label={t("settings.storage.endedWorktrees.label")}
					value={String(settings.endedWorktreeDays)}
					options={days}
					disabled={saving}
					onChange={(next) => update({ endedWorktreeDays: Number(next) })}
				/>
			</SettingsRow>
			{hasDevProfiles || settings.cleanDevProfiles ? (
				<SettingsRow label={t("settings.storage.devProfiles.label")} description={t("settings.storage.devProfiles.description")}>
					<Switch
						aria-label={t("settings.storage.devProfiles.label")}
						checked={settings.cleanDevProfiles}
						disabled={saving}
						onCheckedChange={(checked) => update({ cleanDevProfiles: checked })}
					/>
				</SettingsRow>
			) : null}
		</SettingsSection>
	);
}
