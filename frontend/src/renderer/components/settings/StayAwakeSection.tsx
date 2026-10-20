import { CalendarClock, ChevronRight, Coffee, Plus, X } from "lucide-react";
import { useEffect, useRef, useState } from "react";
import { useTranslation } from "react-i18next";
import {
	awakeCapability,
	useAwake,
	type AwakeCondition,
	type AwakeRule,
	type AwakeSettings,
	type AwakeStart,
} from "../../hooks/useAwake";
import { cn } from "../../lib/utils";
import { Button } from "../ui/button";
import { Popover, PopoverContent, PopoverTrigger } from "../ui/popover";
import { Input } from "../ui/input";
import { Switch } from "../ui/switch";
import { SettingsOptionMenu } from "./SettingsOptionMenu";
import { SettingsRow } from "./SettingsRow";
import { SettingsSection } from "./SettingsSection";

const conditionKinds = [
	"agent_activity",
	"schedule",
	"power",
	"battery",
	"network",
	"device",
	"display",
	"app",
	"uptime",
	"download",
] as const;
const conditionLabels = {
	agent_activity: "Agent activity",
	schedule: "Schedule",
	power: "Power",
	battery: "Battery level",
	network: "IP network",
	device: "Connected volume",
	display: "External display",
	app: "Running application",
	uptime: "System uptime",
	download: "AO browser download",
};
function newCondition(kind: AwakeCondition["kind"]): AwakeCondition {
	return {
		kind,
		value: kind === "power" ? "ac" : kind === "download" ? "any" : "",
		threshold: kind === "battery" ? 40 : kind === "uptime" ? 3600 : 1,
		start: "09:00",
		end: "17:00",
		timezone: Intl.DateTimeFormat().resolvedOptions().timeZone,
	};
}
// Apps that run for the whole login (macOS shell, AO itself): binding a
// session to them would never end it.
const alwaysRunning = new Set([
	"com.apple.finder",
	"com.apple.dock",
	"com.apple.systemuiserver",
	"com.apple.controlcenter",
	"com.apple.notificationcenterui",
	"com.apple.Spotlight",
]);

const dayMs = 24 * 60 * 60 * 1000;
function startOfDay(offset: number) {
	const date = new Date();
	date.setHours(0, 0, 0, 0);
	date.setDate(date.getDate() + offset);
	return date;
}

/** End-time picker: the next seven days (the daemon's limit) and half-hour
 * slots, with past slots disabled. `value` is an ISO timestamp or "". */
function UntilPicker({
	value,
	onChange,
}: {
	value: string;
	onChange: (value: string) => void;
}) {
	const { t, i18n } = useTranslation();
	const [open, setOpen] = useState(false);
	const selected = value ? new Date(value) : null;
	const [day, setDay] = useState(0);
	const base = startOfDay(day);
	const now = Date.now();
	const dayLabel = (offset: number) =>
		offset === 0
			? t("awake.today", "Today")
			: offset === 1
				? t("awake.tomorrow", "Tomorrow")
				: startOfDay(offset).toLocaleDateString(i18n.language, {
						weekday: "short",
						day: "numeric",
						month: "short",
					});
	const time = (date: Date) =>
		date.toLocaleTimeString(i18n.language, { hour: "numeric", minute: "2-digit" });
	const selectedOffset = selected
		? Math.round((startOfDay(0).getTime() - new Date(selected).setHours(0, 0, 0, 0)) / -dayMs)
		: 0;
	return (
		<Popover
			open={open}
			onOpenChange={(next) => {
				setOpen(next);
				if (next) setDay(selectedOffset);
			}}
		>
			<PopoverTrigger asChild>
				<Button variant="footer" aria-label={t("awake.endTime", "Until")}>
					<CalendarClock className="size-icon-sm" />
					{selected
						? `${dayLabel(selectedOffset)} · ${time(selected)}`
						: t("awake.pickTime", "Pick a time")}
				</Button>
			</PopoverTrigger>
			<PopoverContent align="end" className="w-80 p-3">
				<div className="grid grid-cols-7 gap-1">
					{Array.from({ length: 7 }, (_, offset) => {
						const date = startOfDay(offset);
						return (
							<button
								key={offset}
								type="button"
								aria-pressed={day === offset}
								aria-label={dayLabel(offset)}
								onClick={() => setDay(offset)}
								className={cn(
									"flex flex-col items-center rounded-md py-1.5 text-xs transition-colors",
									day === offset
										? "bg-accent text-foreground"
										: "text-muted-foreground hover:bg-accent/60 hover:text-foreground",
								)}
							>
								<span>
									{date.toLocaleDateString(i18n.language, { weekday: "narrow" })}
								</span>
								<span className="text-sm font-medium">{date.getDate()}</span>
							</button>
						);
					})}
				</div>
				<p className="mt-3 mb-1.5 px-1 text-xs text-muted-foreground">
					{dayLabel(day)}
				</p>
				<div className="settings-thin-scrollbar grid max-h-48 grid-cols-4 gap-1 overflow-y-auto">
					{Array.from({ length: 48 }, (_, slot) => {
						const date = new Date(base.getTime() + slot * 30 * 60 * 1000);
						const past = date.getTime() <= now;
						const tooFar = date.getTime() - now > 7 * dayMs;
						const isSelected = selected?.getTime() === date.getTime();
						return (
							<button
								key={slot}
								type="button"
								disabled={past || tooFar}
								onClick={() => {
									onChange(date.toISOString());
									setOpen(false);
								}}
								className={cn(
									"rounded-md py-1.5 text-xs tabular-nums transition-colors disabled:pointer-events-none disabled:opacity-35",
									isSelected
										? "bg-primary text-primary-foreground"
										: "text-foreground hover:bg-accent",
								)}
							>
								{time(date)}
							</button>
						);
					})}
				</div>
			</PopoverContent>
		</Popover>
	);
}
function elapsed(seconds: number) {
	if (!Number.isFinite(seconds) || seconds < 0) return "—";
	const minutes = Math.floor(seconds / 60);
	return minutes < 60
		? `${minutes}m`
		: `${Math.floor(minutes / 60)}h ${minutes % 60}m`;
}

export function StayAwakeSection({ titleHidden }: { titleHidden?: boolean }) {
	const { t } = useTranslation();
	const awake = useAwake(true, true);
	const state = awake.state;
	const [mode, setMode] = useState<AwakeStart["mode"]>("indefinite");
	const [minutes, setMinutes] = useState("60");
	const [until, setUntil] = useState("");
	const [app, setApp] = useState("");
	const [rules, setRules] = useState<AwakeRule[]>([]);
	const [dirty, setDirty] = useState(false);
	const rulesRevision = useRef(0);
	const markRulesDirty = () => {
		rulesRevision.current += 1;
		setDirty(true);
	};
	useEffect(() => {
		if (!dirty && state?.rules) setRules(state.rules);
	}, [state?.rules, dirty]);
	const update = (next: Partial<AwakeSettings>) => {
		if (state)
			awake.act({ kind: "settings", settings: { ...state.settings, ...next } });
	};
	const capability = (id: string) => awakeCapability(state, id);
	const capabilityReason = (id: string) => {
		const reason = capability(id)?.reason;
		return reason
			? t(`mobile.keepAwake.lidReason.${reason}`, reason.replaceAll("_", " "))
			: undefined;
	};
	const supported = capability("idle")?.available ?? false;
	const boolRow = (
		key: keyof AwakeSettings,
		label: string,
		description: string,
		cap?: string,
	) => {
		const unavailable = cap && !capability(cap)?.available;
		return (
			<SettingsRow
				label={label}
				description={unavailable ? capabilityReason(cap!) : description}
				key={key}
			>
				<Switch
					aria-label={label}
					checked={Boolean(state?.settings[key])}
					disabled={
						awake.busy ||
						!state ||
						(Boolean(unavailable) && !state.settings[key])
					}
					onCheckedChange={(value) => update({ [key]: value })}
				/>
			</SettingsRow>
		);
	};
	const advanced = (title: string, children: React.ReactNode) => (
		<details className="group settings-content-row px-(--settings-row-inset) py-3">
			<summary className="flex cursor-pointer list-none items-center gap-1.5 text-sm text-settings-label [&::-webkit-details-marker]:hidden">
				<ChevronRight className="size-icon-sm text-settings-muted transition-transform group-open:rotate-90" />
				{title}
			</summary>
			<div className="mt-3 flex flex-col gap-1">{children}</div>
		</details>
	);
	const changeRule = (index: number, next: Partial<AwakeRule>) => {
		markRulesDirty();
		setRules((current) =>
			current.map((rule, i) => (i === index ? { ...rule, ...next } : rule)),
		);
	};
	const active = state?.sessions?.filter((session) => !session.endedAt) ?? [];
	// Sessions started from this page: Start toggles to End so repeated clicks
	// never stack duplicate requests.
	const mine = active.filter((session) => session.scope === "ao");
	const ends = mine.map((session) => session.deadline).filter(Boolean).sort().at(-1);
	const error =
		awake.error instanceof Error
			? awake.error.message
			: awake.queryError instanceof Error
				? awake.queryError.message
				: null;
	const start = () =>
		awake.act({
			kind: "start",
			request: {
				scope: "ao",
				mode,
				durationSeconds:
					mode === "duration" ? Math.round(Number(minutes) * 60) : 0,
				...(mode === "until" && until
					? { until }
					: {}),
				boundId:
					mode === "download"
						? "any"
						: mode === "app"
							? (state?.observation.applications.find(
									(entry) => entry.identity === app,
								)?.id ?? "")
							: "",
				boundIdentity: mode === "app" ? app : "",
				lidClosed: state?.settings.lidClosed ?? true,
			},
		});
	return (
		<SettingsSection
			title={t("awake.title", "Stay Awake")}
			sectionId="stay-awake"
			titleHidden={titleHidden}
			grouped
		>
			<SettingsRow
				label={t("awake.status", "Status")}
				description={
					state?.reason
						? t(
								`mobile.keepAwake.lidReason.${state.reason}`,
								state.reason.replaceAll("_", " "),
							)
						: t(
								"awake.independent",
								"Works on this Mac without a phone or mobile access.",
							)
				}
			>
				<span className="text-sm text-settings-muted" role="status">
					{state?.active
						? state.lidActive
							? t("awake.armed", "Awake · lid control armed")
							: t("awake.active", "Awake")
						: t("awake.idle", "Normal sleep")}
				</span>
			</SettingsRow>
			{state?.paused ? (
				<SettingsRow
					label={
						state.reason
							? t("awake.safetyStopped", "Stopped for protection")
							: t("awake.automaticPaused", "Automatic activation is paused.")
					}
					description={t(
						"awake.resumeHelp",
						"Resuming checks fresh battery and heat readings before allowing requests again.",
					)}
				>
					<Button
						variant="footer"
						disabled={awake.busy}
						onClick={() => awake.act({ kind: "resume" })}
					>
						{t("awake.resume", "Resume")}
					</Button>
				</SettingsRow>
			) : null}
			{error ? (
				<div
					className="settings-content-row px-(--settings-row-inset) py-2 text-xs text-error"
					role={awake.error ? "alert" : "status"}
				>
					{error}
				</div>
			) : null}
			<SettingsRow
				label={t("awake.sessionMode", "Keep awake")}
				description={t(
					"awake.sessionHelp",
					"Choose how long this Mac should stay awake.",
				)}
			>
				<SettingsOptionMenu
					aria-label={t("awake.sessionMode", "Keep awake")}
					value={mode}
					onChange={setMode}
					disabled={awake.busy || !supported}
					options={[
						{
							value: "indefinite",
							label: t("awake.indefinite", "Indefinitely"),
						},
						{ value: "duration", label: t("awake.duration", "For a duration") },
						{ value: "until", label: t("awake.until", "Until a time") },
						{ value: "app", label: t("awake.untilApp", "While an app runs") },
						{
							value: "download",
							label: t("awake.untilDownload", "While AO browser downloads run"),
						},
					]}
				/>
			</SettingsRow>
			{mode === "duration" ? (
				<SettingsRow label={t("awake.minutes", "Duration in minutes")}>
					<Input
						className="w-24"
						type="number"
						min={1}
						max={10080}
						aria-label={t("awake.minutes", "Duration in minutes")}
						value={minutes}
						onChange={(e) => setMinutes(e.target.value)}
					/>
				</SettingsRow>
			) : null}
			{mode === "until" ? (
				<SettingsRow label={t("awake.endTime", "Until")}>
					<UntilPicker value={until} onChange={setUntil} />
				</SettingsRow>
			) : null}
			{mode === "app" ? (
				<SettingsRow label={t("awake.application", "Application")}>
					<SettingsOptionMenu
						aria-label={t("awake.application", "Application")}
						value={app}
						onChange={setApp}
						options={(state?.observation.applications ?? [])
							.filter(
								(entry) =>
									!alwaysRunning.has(entry.id) &&
									!entry.id.startsWith("com.aoagents.") &&
									entry.id !== "com.github.Electron",
							)
							.filter(
								(entry, index, all) =>
									all.findIndex((other) => other.name === entry.name) === index,
							)
							.sort((a, b) => a.name.localeCompare(b.name))
							.map((entry) => ({ value: entry.identity, label: entry.name }))}
					/>
				</SettingsRow>
			) : null}
			<SettingsRow
				label={t("awake.forAO", "For AO")}
				description={
					mine.length
						? ends
							? `${t("awake.ends", "Ends")} ${new Date(ends).toLocaleTimeString([], { hour: "2-digit", minute: "2-digit" })}`
							: t("awake.untilEnded", "Awake until you end it")
						: t(
								"awake.quickHelp",
								"Battery and heat protection always apply.",
							)
				}
			>
				{mine.length ? (
					<Button
						variant="footer"
						disabled={awake.busy}
						onClick={() => {
							for (const session of mine)
								awake.act({ kind: "end", id: session.id });
						}}
					>
						{t("awake.end", "End")}
					</Button>
				) : (
					<Button
						variant="footer"
						disabled={
							awake.busy ||
							!supported ||
							(mode === "app" && !app) ||
							(mode === "until" && !until)
						}
						onClick={start}
					>
						<Coffee className="size-icon-sm" />
						{t("awake.start", "Start")}
					</Button>
				)}
			</SettingsRow>
			<SettingsRow label={t("awake.uptime", "System uptime")}>
				<span className="text-sm text-settings-muted">
					{elapsed(state?.observation.uptimeSeconds ?? 0)}
				</span>
			</SettingsRow>
			{boolRow(
				"lidClosed",
				t("awake.lidDefault", "Allow the lid to close"),
				t(
					"awake.lidHelp",
					"New awake sessions also request closed-lid operation. Keep the Mac ventilated.",
				),
				"lid",
			)}
			{boolRow(
				"displayAwake",
				t("awake.displayAwake", "Keep the display awake"),
				t(
					"awake.displayHelp",
					"The internal panel still sleeps when the lid closes.",
				),
				"display_awake",
			)}
			{advanced(
				t("awake.automatic", "Automatic activation"),
				<>
					<p className="text-xs text-settings-muted">
						{t(
							"awake.rulesHelp",
							"A rule keeps this Mac awake by itself while its conditions are true, for example \"while an agent is working\" or \"on AC power between 09:00 and 17:00\". Add a rule, pick its conditions, turn it on, then save.",
						)}
					</p>
					{!state?.paused && (state?.rules.length ?? 0) > 0 ? (
						<Button
							variant="footer"
							disabled={awake.busy}
							onClick={() => awake.act({ kind: "stop" })}
						>
							{t(
								"awake.pauseAutomatic",
								"Pause automatic activation and end desktop requests",
							)}
						</Button>
					) : null}
					{rules.map((rule, index) => (
						<div className="flex flex-col gap-2 rounded-md border border-border p-3" key={rule.id}>
							<div className="flex items-center gap-2">
								<Input
									aria-label={t("awake.ruleName", "Rule name")}
									value={rule.name}
									maxLength={120}
									onChange={(e) => changeRule(index, { name: e.target.value })}
								/>
								<Switch
									aria-label={`${t("awake.enableRule", "Enable rule")} ${rule.name}`}
									checked={rule.enabled}
									onCheckedChange={(enabled) => changeRule(index, { enabled })}
								/>
								<Button
									variant="ghost"
									size="icon-sm"
									aria-label={t("awake.removeRule", "Remove rule")}
									onClick={() => {
										markRulesDirty();
										setRules((current) =>
											current.filter((_, i) => i !== index),
										);
									}}
								>
									<X className="size-icon-sm" />
								</Button>
							</div>
							<div className="flex items-center gap-2 text-xs text-settings-muted">
								{t("awake.keepAwakeWhen", "Keep awake when")}
								<SettingsOptionMenu
									aria-label={t("awake.match", "Match conditions")}
									value={rule.match}
									options={[
										{ value: "all", label: t("awake.all", "all of these are true") },
										{ value: "any", label: t("awake.any", "any of these is true") },
									]}
									onChange={(match) => changeRule(index, { match })}
								/>
							</div>
							{rule.conditions.map((condition, ci) => (
								<div
									key={ci}
									className="flex flex-wrap items-center gap-2"
								>
									<SettingsOptionMenu
										aria-label={t("awake.condition", "Condition")}
										value={condition.kind}
										options={conditionKinds.map((kind) => ({
											value: kind,
											label: t(
												`awake.condition.${kind}`,
												conditionLabels[kind],
											),
										}))}
										onChange={(kind) =>
											changeRule(index, {
												conditions: rule.conditions.map((c, i) =>
													i === ci ? newCondition(kind) : c,
												),
											})
										}
									/>
									<ConditionEditor
										condition={condition}
										onChange={(next) =>
											changeRule(index, {
												conditions: rule.conditions.map((c, i) =>
													i === ci ? { ...c, ...next } : c,
												),
											})
										}
									/>
									{rule.conditions.length > 1 ? (
										<Button
											variant="ghost"
											size="icon-sm"
											aria-label={t(
												"awake.removeCondition",
												"Remove condition",
											)}
											onClick={() =>
												changeRule(index, {
													conditions: rule.conditions.filter(
														(_, i) => i !== ci,
													),
												})
											}
										>
											<X className="size-icon-sm" />
										</Button>
									) : null}
								</div>
							))}
							<div className="flex items-center justify-between gap-2">
								<Button
									variant="ghost"
									size="sm"
									disabled={rule.conditions.length >= 12}
									onClick={() =>
										changeRule(index, {
											conditions: [
												...rule.conditions,
												newCondition("agent_activity"),
											],
										})
									}
								>
									<Plus className="size-icon-sm" />
									{t("awake.addCondition", "Add condition")}
								</Button>
								<label className="flex items-center gap-2 text-xs text-settings-muted">
									{t("awake.lidForRule", "Lid closed")}
									<Switch
										aria-label={`${rule.name} ${t("awake.lidForRule", "Lid closed")}`}
										checked={rule.lidClosed}
										disabled={!capability("lid")?.available}
										onCheckedChange={(lidClosed) =>
											changeRule(index, { lidClosed })
										}
									/>
								</label>
							</div>
						</div>
					))}
					<div className="flex justify-end gap-2 py-2">
						<Button
							variant="footer"
							disabled={awake.busy || rules.length >= 50}
							onClick={() => {
								markRulesDirty();
								setRules((current) => [
									...current,
									{
										id: crypto.randomUUID(),
										name: t("awake.newRule", "New rule"),
										enabled: false,
										match: "all",
										conditions: [newCondition("agent_activity")],
										lidClosed: state?.settings.lidClosed ?? false,
										suppressed: false,
									},
								]);
							}}
						>
							{t("awake.addRule", "Add rule")}
						</Button>
						<Button
							variant="footer-primary"
							disabled={awake.busy || !dirty}
							onClick={() => {
								const submittedRevision = rulesRevision.current;
								awake.act(
									{ kind: "rules", rules },
									{
										onSuccess: () => {
											// A response only acknowledges the submitted draft.
											if (rulesRevision.current === submittedRevision) setDirty(false);
										},
									},
								);
							}}
						>
							{t("awake.saveRules", "Save rules")}
						</Button>
					</div>
				</>,
			)}
			{advanced(
				t("awake.batteryHeat", "Battery and heat protection"),
				<>
					<SettingsRow
						label={t("awake.stopBattery", "Stop below battery percentage")}
						description={t(
							"awake.stopBatteryHelp",
							"At least 10%. Critical heat and unavailable battery readings always prevent closed-lid operation.",
						)}
					>
						<Input
							key={state?.settings.stopBatteryPercent}
							className="w-20"
							aria-label={t(
								"awake.stopBattery",
								"Stop below battery percentage",
							)}
							type="number"
							min={10}
							max={90}
							defaultValue={state?.settings.stopBatteryPercent ?? 10}
							disabled={!state || awake.busy}
							onBlur={(e) => {
								const value = Number(e.target.value);
								if (state && value !== state.settings.stopBatteryPercent)
									update({ stopBatteryPercent: value });
							}}
						/>
					</SettingsRow>
                    <SettingsRow
                        label={t("awake.lowPowerPermission", "Low Power Mode authorization")}
                        description={state?.reason === "low_power_revocation_pending" ? t("awake.revocationPending", "Authorization removal was interrupted. Complete Revoke authorization again before enabling or authorizing Low Power Mode.") : t("awake.lowPowerPermissionHelp", "Grants this account four fixed Low Power Mode commands. macOS asks for administrator approval once. Saved active policies may resume after approval. Disabling automation keeps this permission; use Revoke authorization to remove it.")}
                    >
                        {state?.observation.lowPowerAuthorized ? <span className="text-sm text-settings-muted">{t("awake.authorized", "Authorized")}</span> :
                        <Button variant="footer" disabled={awake.busy || !capability("low_power_authorization")?.available}
                            onClick={() => awake.act({ kind: "authorizeLowPower" })}>{t("awake.authorize", "Authorize…")}</Button>}
                        <Button variant="footer" disabled={awake.busy || !capability("low_power_revocation")?.available}
                            onClick={() => {
                                if (window.confirm(t("awake.revokeLowPowerConfirm", "Disable Low Power Mode automation, restore AO-owned changes, and remove this account’s authorization? macOS will ask for administrator approval. Canceling leaves automation disabled. Other AO instances must disable their Low Power Mode policies first."))) awake.act({ kind: "revokeLowPower" });
                            }}>{t("awake.revokeAuthorization", "Revoke authorization…")}</Button>
                    </SettingsRow>
                    <SettingsRow label={t("awake.lowPowerBattery", "Low Power Mode below battery percentage")}
                        description={capability("low_power")?.available ? t("awake.lowPowerBatteryHelp", "While an awake request is active. 0 disables this rule; otherwise 10–90%. Restores AO-owned changes after recovery or when the request ends.") : capabilityReason("low_power")}>
                        <Input className="w-20" type="number" min={0} max={90} key={state?.settings.lowPowerBatteryPercent}
                            aria-label={t("awake.lowPowerBattery", "Low Power Mode below battery percentage")}
                            defaultValue={state?.settings.lowPowerBatteryPercent ?? 0} disabled={awake.busy || !capability("low_power")?.available}
                            onBlur={(e) => { const value = Number(e.target.value); if (state && value !== state.settings.lowPowerBatteryPercent) update({ lowPowerBatteryPercent: value }); }} />
                    </SettingsRow>
                    {boolRow("lowPowerOnSeriousHeat", t("awake.lowPowerHeat", "Low Power Mode on serious heat"), t("awake.lowPowerHeatHelp", "Applies to the current power source during awake requests. Critical heat still stops wake requests. AO preserves existing user settings."), "low_power")}
                    <SettingsRow label={t("awake.lowPowerCurrent", "Current Low Power Mode")}>
                        <span className="text-sm text-settings-muted">{state?.observation.lowPowerMode ? t("common.on", "On") : t("common.off", "Off")}</span>
                    </SettingsRow>
                    {state?.reason?.startsWith("low_power_") && state.reason !== "low_power_revocation_pending" && state.reason !== "low_power_authorization_in_progress" ? <SettingsRow label={t("awake.lowPowerRecovery", "Low Power Mode needs attention")}
                        description={t("awake.lowPowerRecoveryHelp", "End desktop wake requests and retry restoring AO-owned Low Power Mode changes. If permission was removed, authorize it again first.")}>
                        <Button variant="footer" disabled={awake.busy} onClick={() => {
                            if (window.confirm(t("awake.lowPowerRecoverConfirm", "End desktop wake requests and restore AO-owned Low Power Mode changes?"))) awake.act({ kind: "recover" });
                        }}>{t("awake.recoverPower", "Recover Low Power Mode")}</Button>
                    </SettingsRow> : null}
					<SettingsRow
						label={t("awake.thermal", "Thermal state")}
						description={t(
							"awake.criticalHeat",
							"Critical heat stops wake requests and requires an explicit resume after cooling.",
						)}
					>
						<span className="text-sm text-settings-muted">
							{state?.observation.thermal || "—"}
						</span>
					</SettingsRow>
				</>,
			)}
		</SettingsSection>
	);
}

function ConditionEditor({
	condition,
	onChange,
}: {
	condition: AwakeCondition;
	onChange: (next: Partial<AwakeCondition>) => void;
}) {
	const { t } = useTranslation();
	const input = (
		label: string,
		value: string,
		change: (value: string) => void,
		type = "text",
		placeholder?: string,
	) => (
		<Input
			className="w-40"
			aria-label={label}
			placeholder={placeholder}
			type={type}
			value={value}
			onChange={(e) => change(e.target.value)}
		/>
	);
	switch (condition.kind) {
		case "schedule":
			return (
				<>
					{input(
						t("awake.scheduleStart", "Start time"),
						condition.start,
						(start) => onChange({ start }),
						"time",
					)}
					{input(
						t("awake.scheduleEnd", "End time"),
						condition.end,
						(end) => onChange({ end }),
						"time",
					)}
					{input(
						t("awake.timezone", "Timezone"),
						condition.timezone,
						(timezone) => onChange({ timezone }),
					)}
				</>
			);
		case "power":
			return (
				<SettingsOptionMenu
					aria-label={t("awake.powerSource", "Power source")}
					value={condition.value}
					options={[
						{ value: "ac", label: t("awake.ac", "AC power") },
						{ value: "battery", label: t("awake.battery", "Battery") },
					]}
					onChange={(value) => onChange({ value })}
				/>
			);
		case "battery":
		case "display":
		case "uptime":
			return (
				<label className="flex items-center gap-2 text-xs text-settings-muted">
					{condition.kind === "battery"
						? t("awake.batteryAtLeast", "Battery at least (%)")
						: condition.kind === "display"
							? t("awake.displayAtLeast", "External displays at least")
							: t("awake.uptimeAtLeast", "Uptime at least (seconds)")}
					{input(
						t("awake.threshold", "Threshold"),
						String(condition.threshold),
						(value) => onChange({ threshold: Number(value) }),
						"number",
					)}
				</label>
			);
		case "network":
			return input(
				t("awake.networkCIDR", "Network CIDR"),
				condition.value,
				(value) => onChange({ value }),
				"text",
				"192.168.1.0/24",
			);
		case "device":
			return input(
				t("awake.volumeName", "Connected volume name"),
				condition.value,
				(value) => onChange({ value }),
				"text",
				"Backup Drive",
			);
		case "app":
			return input(
				t("awake.bundleID", "Application bundle ID"),
				condition.value,
				(value) => onChange({ value }),
				"text",
				"com.apple.Music",
			);
		default:
			return null;
	}
}
