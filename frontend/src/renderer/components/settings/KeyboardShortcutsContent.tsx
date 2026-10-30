import { AlertCircle, Check, Keyboard, MoreHorizontal, Pencil, Plus, RotateCcw, Search, Trash2, X } from "lucide-react";
import { useTranslation } from "react-i18next";
import type { TFunction } from "i18next";
import { shortcutCategoryLabelKeys, shortcutLabelKeys } from "../../i18n/key-maps";
import { useEffect, useId, useLayoutEffect, useMemo, useRef, useState } from "react";
import {
	APP_SHORTCUTS,
	effectiveShortcutBindings,
	SHORTCUT_CATEGORIES,
	shortcutBindingKeys,
	matchesShortcutBinding,
	shortcutBindingLabel,
	shortcutBindingValidationError,
	type AppShortcutId,
	type ShortcutCategory,
	type KeybindingOverrides,
	type ShortcutBinding,
} from "../../../shared/shortcuts";
import { isMacPlatform } from "../../lib/platform";
import { aoBridge } from "../../lib/bridge";
import { cn } from "../../lib/utils";
import { useKeybindingsStore } from "../../stores/keybindings-store";
import { Button } from "../ui/button";
import { Tooltip, TooltipContent, TooltipTrigger } from "../ui/tooltip";
import { ConfirmDialog } from "../ConfirmDialog";
import { ShortcutKeys, shortcutKeysDescription } from "../ShortcutKeys";
import { RadioGroup } from "radix-ui";
import { AnimatePresence, motion, useReducedMotion } from "motion/react";
import { SettingsSection } from "./SettingsSection";
import { HARNESS_CARD_CLASS, HARNESS_CARD_ROW_CLASS } from "./harness/harness-ui";
import "./keyboard-shortcuts.css";

function shortcutLabel(id: AppShortcutId, t: TFunction): string {
	return t(shortcutLabelKeys[id]);
}

function shortcutCategoryLabel(category: ShortcutCategory, t: TFunction): string {
	return t(shortcutCategoryLabelKeys[category]);
}

type RecordingState = { id: AppShortcutId; mode: "replace" | "add" };
type ConflictState = {
	targetId: AppShortcutId;
	conflictingId: AppShortcutId;
	binding: ShortcutBinding;
	mode: RecordingState["mode"];
};
type ToastState = {
	title: string;
	body?: string;
	undo?: () => Promise<void>;
	error?: boolean;
};

const ignoredRecordingKeys = new Set([
	"Alt",
	"AltGraph",
	"CapsLock",
	"Control",
	"Dead",
	"Meta",
	"NumLock",
	"Process",
	"ScrollLock",
	"Shift",
	"Unidentified",
]);

const codeMatchedKeys: ReadonlySet<string> = new Set(["Backquote", "BracketLeft", "BracketRight"]);

export function eventBinding(event: KeyboardEvent): ShortcutBinding | null {
	if (ignoredRecordingKeys.has(event.key) || event.getModifierState?.("AltGraph")) return null;
	return {
		key: event.key.length === 1 ? event.key.toLowerCase() : event.key,
		// Keys whose `key` shifts with modifiers or layout are matched by physical code too.
		// Option also rewrites letters on macOS (⌥B types "∫"), so those keep their code.
		...(codeMatchedKeys.has(event.code) || (event.altKey && /^(Key[A-Z]|Digit\d)$/.test(event.code))
			? { code: event.code }
			: {}),
		ctrl: event.ctrlKey,
		meta: event.metaKey,
		shift: event.shiftKey,
		alt: event.altKey,
	};
}

function bindingDescription(binding: ShortcutBinding, isMac: boolean) {
	return shortcutKeysDescription(shortcutBindingKeys(binding, isMac));
}

function BindingKeys({ binding, isMac }: { binding: ShortcutBinding; isMac: boolean }) {
	return <ShortcutKeys keys={shortcutBindingKeys(binding, isMac)} />;
}

function searchText(value: string) {
	return value.toLowerCase().replace(/command|cmd|⌘/g, " meta ").replace(/control|ctrl|⌃/g, " ctrl ")
		.replace(/option|opt|alt|⌥/g, " alt ").replace(/shift|⇧/g, " shift ").replace(/\+/g, " ").trim();
}

export function KeyboardShortcutsContent({
	active,
	titleHidden,
	bounded = true,
	isMac = isMacPlatform(),
}: {
	active: boolean;
	titleHidden?: boolean;
	bounded?: boolean;
	isMac?: boolean;
}) {
	const { t } = useTranslation();
	const reduceMotion = useReducedMotion();
	const overrides = useKeybindingsStore((state) => state.overrides);
	const setOverrides = useKeybindingsStore((state) => state.setOverrides);
	const resetBinding = useKeybindingsStore((state) => state.resetBinding);
	const resetAll = useKeybindingsStore((state) => state.resetAll);
	const [query, setQuery] = useState("");
	const [modifiedOnly, setModifiedOnly] = useState(false);
	const [expanded, setExpanded] = useState<AppShortcutId | null>(null);
	const [recordingReady, setRecordingReady] = useState(false);
	const [recordingError, setRecordingError] = useState<string | null>(null);
	const [saving, setSaving] = useState(false);
	const [savingId, setSavingId] = useState<AppShortcutId | null>(null);
	const [mutationError, setMutationError] = useState<{ id: AppShortcutId; message: string } | null>(null);
	const savingRef = useRef(false);
	const captureRef = useRef<HTMLDivElement>(null);
	const layoutRef = useRef<HTMLDivElement>(null);
	const triggerRef = useRef<HTMLElement | null>(null);
	const editIdRef = useRef<string | null>(null);
	const searchRef = useRef<HTMLInputElement>(null);
	const contentId = useId();
	const [recording, setRecordingState] = useState<RecordingState | null>(null);
	const [conflict, setConflict] = useState<ConflictState | null>(null);
	const [confirmResetAll, setConfirmResetAll] = useState(false);
	const [resettingAll, setResettingAll] = useState(false);
	const [resetAllError, setResetAllError] = useState<string | null>(null);
	const [conflictError, setConflictError] = useState<string | null>(null);
	const [toast, setToast] = useState<ToastState | null>(null);
	const toastTimerRef = useRef<number | undefined>(undefined);
	const activeRef = useRef(active);
	const recordingRequestRef = useRef(0);
	activeRef.current = active;

	const showToast = (next: ToastState) => {
		if (toastTimerRef.current !== undefined) window.clearTimeout(toastTimerRef.current);
		setToast(next);
		toastTimerRef.current = window.setTimeout(() => setToast(null), 3500);
	};

	useEffect(
		() => () => {
			activeRef.current = false;
			recordingRequestRef.current += 1;
			if (toastTimerRef.current !== undefined) window.clearTimeout(toastTimerRef.current);
			void aoBridge.keybindings.setRecording(false);
		},
		[],
	);

	useEffect(() => {
		if (active) return;
		recordingRequestRef.current += 1;
		setRecordingState(null);
		setRecordingReady(false);
		void aoBridge.keybindings.setRecording(false);
	}, [active]);

	const endRecording = (restoreFocus = true) => {
		recordingRequestRef.current += 1;
		setRecordingState(null);
		setRecordingReady(false);
		setRecordingError(null);
		void aoBridge.keybindings.setRecording(false);
		if (restoreFocus) queueMicrotask(() => {
			const trigger = triggerRef.current;
			if (trigger?.isConnected) trigger.focus();
			else if (editIdRef.current) document.getElementById(editIdRef.current)?.focus();
		});
	};

	useEffect(() => {
		if (!recording) return;
		captureRef.current?.focus();
		const handleBlur = () => endRecording(false);
		window.addEventListener("blur", handleBlur);
		return () => window.removeEventListener("blur", handleBlur);
	}, [recording]);

	const beginRecording = async (next: RecordingState, trigger: HTMLElement) => {
		if (savingRef.current) return;
		triggerRef.current = trigger;
		editIdRef.current = `${contentId}-${next.id}-edit`;
		const request = ++recordingRequestRef.current;
		setRecordingError(null);
		setMutationError(null);
		setRecordingReady(false);
		setRecordingState(next);
		try {
			await aoBridge.keybindings.setRecording(true);
			if (!activeRef.current || request !== recordingRequestRef.current) return;
			setRecordingReady(true);
		} catch {
			if (request !== recordingRequestRef.current) return;
			endRecording();
			setMutationError({ id: next.id, message: t("shortcut.couldNotRecord") });
		}
	};

	// Like Harness, the shortcut list owns scrolling. Keep any offset inherited
	// from another settings page out of the fixed outer viewport.
	useLayoutEffect(() => {
		if (!bounded) return;
		const body = layoutRef.current?.closest<HTMLElement>(".settings-thin-scrollbar");
		if (!body) return;
		const toTop = () => { if (body.scrollTop !== 0) body.scrollTop = 0; };
		toTop();
		body.addEventListener("scroll", toTop);
		return () => body.removeEventListener("scroll", toTop);
	}, [bounded]);

	const filteredShortcuts = useMemo(() => {
		const needles = searchText(query).split(/\s+/).filter(Boolean);
		return APP_SHORTCUTS.filter((shortcut) => {
			if (modifiedOnly && !Object.hasOwn(overrides, shortcut.id)) return false;
			const labels = effectiveShortcutBindings(shortcut.id, isMac, overrides)
				.map((candidate) => `${shortcutBindingLabel(candidate, isMac)} ${bindingDescription(candidate, isMac)}`)
				.join(" ");
			const haystack = searchText(`${shortcutLabel(shortcut.id, t)} ${shortcutCategoryLabel(shortcut.category, t)} ${labels}`);
			return needles.every((needle) => haystack.includes(needle));
		});
	}, [isMac, overrides, query, modifiedOnly, t]);

	const mutate = async (id: AppShortcutId, operation: () => Promise<void>) => {
		if (savingRef.current) return false;
		savingRef.current = true;
		setSaving(true);
		setSavingId(id);
		setMutationError(null);
		try {
			await operation();
			return true;
		} catch {
			setMutationError({ id, message: t("shortcut.saveFailed") });
			return false;
		} finally {
			savingRef.current = false;
			setSaving(false);
			setSavingId(null);
		}
	};

	const applyBinding = async (
		targetId: AppShortcutId,
		candidate: ShortcutBinding,
		mode: RecordingState["mode"],
		conflictingId?: AppShortcutId,
	) => {
		const before = useKeybindingsStore.getState().overrides;
		const next: KeybindingOverrides = { ...before };
		const currentTarget = effectiveShortcutBindings(targetId, isMac, before);
		next[targetId] = mode === "add" ? [...currentTarget, candidate].slice(-2) : [candidate];
		if (conflictingId) {
			next[conflictingId] = effectiveShortcutBindings(conflictingId, isMac, before).filter(
				(existing) => !matchesShortcutBinding(candidate, existing),
			);
		}
		if (!await mutate(targetId, () => setOverrides(next))) return false;
		const targetLabel = shortcutLabel(targetId, t);
		const conflictLabel = conflictingId ? shortcutLabel(conflictingId, t) : undefined;
		showToast({
			title: conflictingId ? t("shortcut.reassigned") : t("shortcut.updated"),
			body: conflictingId
				? t("shortcut.movedBody", {
						binding: shortcutBindingLabel(candidate, isMac),
						from: conflictLabel ?? "",
						to: targetLabel,
					})
				: t("shortcut.updatedBody", { name: targetLabel, binding: shortcutBindingLabel(candidate, isMac) }),
			undo: async () => {
				if (await mutate(targetId, () => setOverrides(before))) showToast({ title: t("shortcut.undone") });
			},
		});
		return true;
	};

	useEffect(() => {
		if (!active || !recording) return;
		const handleKeyDown = (event: KeyboardEvent) => {
			if (event.key === "Tab") {
				endRecording(false);
				return;
			}
			// Own Escape before Radix's document listener, so it cancels capture
			// without dismissing the surrounding settings dialog.
			event.preventDefault();
			event.stopImmediatePropagation();
			if (event.key === "Escape") {
				endRecording();
				return;
			}
			if (!recordingReady || event.repeat) return;
			const candidate = eventBinding(event);
			if (!candidate) {
				return;
			}
			const validationError = shortcutBindingValidationError(candidate, isMac);
			if (validationError) {
				setRecordingError(validationError);
				return;
			}
			if (
				recording.mode === "add" &&
				effectiveShortcutBindings(recording.id, isMac, overrides).some((existing) =>
					matchesShortcutBinding(candidate, existing),
				)
			) {
				setRecordingError(t("shortcut.alreadyAvailable", { binding: shortcutBindingLabel(candidate, isMac) }));
				return;
			}
			const conflicting = APP_SHORTCUTS.find(
				(shortcut) =>
					shortcut.id !== recording.id &&
					effectiveShortcutBindings(shortcut.id, isMac, overrides).some((existing) =>
						matchesShortcutBinding(candidate, existing),
					),
			);
			if (conflicting) {
				if (conflicting.customizable === false) {
					setRecordingError(t("shortcut.usedByFixed", { binding: shortcutBindingLabel(candidate, isMac), name: shortcutLabel(conflicting.id, t) }));
					return;
				}
				setConflict({
					targetId: recording.id,
					conflictingId: conflicting.id,
					binding: candidate,
					mode: recording.mode,
				});
				endRecording();
				return;
			}
			void applyBinding(recording.id, candidate, recording.mode);
			endRecording();
		};
		window.addEventListener("keydown", handleKeyDown, true);
		return () => window.removeEventListener("keydown", handleKeyDown, true);
	}, [isMac, active, overrides, recording, recordingReady]);

	const handleResetBinding = async (id: AppShortcutId) => {
		const before = useKeybindingsStore.getState().overrides;
		if (!await mutate(id, () => resetBinding(id))) return;
		showToast({
			title: t("shortcut.restored"),
			body: t("shortcut.restoredBody", { name: shortcutLabel(id, t) }),
			undo: async () => {
				if (await mutate(id, () => setOverrides(before))) showToast({ title: t("shortcut.undone") });
			},
		});
	};

	const handleRemoveBinding = async (id: AppShortcutId, index: number) => {
		const before = useKeybindingsStore.getState().overrides;
		const current = effectiveShortcutBindings(id, isMac, before);
		const removed = current[index];
		if (!await mutate(id, () => setOverrides({ ...before, [id]: current.filter((_, candidateIndex) => candidateIndex !== index) }))) return;
		showToast({
			title: t("shortcut.removed"),
			body: removed ? t("shortcut.removedBody", { name: shortcutLabel(id, t), binding: shortcutBindingLabel(removed, isMac) }) : undefined,
			undo: async () => {
				if (await mutate(id, () => setOverrides(before))) showToast({ title: t("shortcut.undone") });
			},
		});
	};

	return (
		<>
			<div ref={layoutRef} className="shortcuts-settings" data-shortcuts-layout={bounded ? "" : undefined} aria-busy={saving}>
				<SettingsSection
					title={t("settings.keyboardShortcuts")}
					titleHidden={titleHidden}
					sectionId="keyboard-shortcuts"
					toolbar={
						<div className="shortcut-toolbar sticky top-toolbar z-10 flex flex-wrap items-center gap-2 bg-(--color-bg-primary) pb-2">
							<div className="flex h-9 min-w-40 flex-1 items-center gap-2 rounded-md border border-(--color-border-settings-input) bg-(--color-bg-settings-input) px-3 focus-within:outline-2 focus-within:outline-offset-2 focus-within:outline-(--color-settings-accent)">
								<Search aria-hidden="true" className="size-4 shrink-0 text-settings-muted" />
								<input
									ref={searchRef}
									type="search"
									className="min-w-0 flex-1 bg-transparent text-sm text-settings-label outline-none placeholder:text-settings-muted [&::-webkit-search-cancel-button]:hidden"
									aria-label={t("shortcut.searchPlaceholder")}
									placeholder={t("shortcut.searchPlaceholder")}
									value={query}
									onChange={(event) => { if (recording) endRecording(false); setQuery(event.target.value); }}
								/>
								{query ? <button type="button" className="shortcut-icon-button -mr-2" aria-label={t("shortcut.clearSearch")} onClick={() => { setQuery(""); searchRef.current?.focus(); }}><X className="size-4" aria-hidden="true" /></button> : null}
							</div>
							<RadioGroup.Root value={modifiedOnly ? "modified" : "all"} onValueChange={(value) => { if (recording) endRecording(false); setModifiedOnly(value === "modified"); }} aria-label={t("shortcut.filterLabel")} className="settings-segment rounded-md">
								<RadioGroup.Item value="all" className="settings-segment-item rounded-md">{t("shortcut.allCommands")}</RadioGroup.Item>
								<RadioGroup.Item value="modified" className="settings-segment-item gap-1.5 rounded-md">{t("shortcut.modified")}<span className="text-settings-muted">{Object.keys(overrides).length}</span></RadioGroup.Item>
							</RadioGroup.Root>
						</div>
					}
				>
					<div className="flex min-w-0 flex-col gap-3">
						<div className="flex flex-wrap items-center justify-between gap-x-4 gap-y-2">
							<p className="min-w-0 flex-1 text-xs leading-5 text-settings-muted">{t("shortcut.settingsIntro")}</p>
							<Button type="button" variant="ghost" size="sm" className="h-8 text-settings-muted" disabled={Object.keys(overrides).length === 0 || saving || Boolean(recording)} onClick={() => setConfirmResetAll(true)}><RotateCcw className="size-4" aria-hidden="true" />{t("shortcut.resetAll")}</Button>
						</div>
						<span className="text-xs text-settings-muted" aria-live="polite">{t("shortcut.resultCount", { count: filteredShortcuts.length })}</span>
						<div data-shortcuts-list="" className={cn(HARNESS_CARD_CLASS, "shortcut-list harness-list-scrollbar")}>
						{filteredShortcuts.length > 0 ? <>
				{SHORTCUT_CATEGORIES.map((category) => {
					const shortcuts = filteredShortcuts.filter((shortcut) => shortcut.category === category);
					if (!shortcuts.length) return null;
					return <section className="flex min-w-0 shrink-0 flex-col" key={category} aria-labelledby={`${contentId}-${category}`}>
						<h3 className="border-b border-border bg-(--color-bg-secondary) px-3 py-2 text-xs font-medium leading-4 text-settings-muted" id={`${contentId}-${category}`}>{shortcutCategoryLabel(category, t)}</h3>
						<div className="divide-y divide-border">
							{shortcuts.map((shortcut) => {
								const bindings = effectiveShortcutBindings(shortcut.id, isMac, overrides);
								const modified = Object.hasOwn(overrides, shortcut.id);
								const isRecording = recording?.id === shortcut.id;
								const isExpanded = expanded === shortcut.id;
								const label = shortcutLabel(shortcut.id, t);
								return <div className={cn("shortcut-item", isRecording && "is-recording")} key={shortcut.id}>
									<div className={cn(HARNESS_CARD_ROW_CLASS, "flex-wrap gap-x-4 gap-y-2")}>
										<div className="flex min-w-0 flex-1 flex-wrap items-center gap-2 text-sm leading-5 text-settings-label [overflow-wrap:anywhere]"><span>{label}</span>{modified ? <span className="shortcut-modified">{t("shortcut.modified")}</span> : null}{savingId === shortcut.id ? <span className="text-control text-muted-foreground" role="status">{t("shortcut.saving")}</span> : null}</div>
										<div className="flex min-w-0 max-w-full flex-wrap items-center justify-end gap-x-3 gap-y-2">
										<div className="flex flex-wrap items-center gap-2">
											{bindings.length ? bindings.map((binding, index) => <span className="shortcut-alternative" key={`${binding.key}-${index}`}>{index > 0 ? <span className="shortcut-or">{t("shortcut.or")}</span> : null}<BindingKeys binding={binding} isMac={isMac} /></span>) : <span className="shortcut-unassigned">{t("shortcut.unassigned")}</span>}
										</div>
										<div className="flex shrink-0 items-center justify-end gap-1">
											{shortcut.customizable === false ? <span className="shortcut-fixed" title={t("shortcut.fixedIndexed")}>{t("shortcut.fixed")}</span> : <>
												<Tooltip>
													<TooltipTrigger asChild>
														<button type="button" className="shortcut-icon-button" id={`${contentId}-${shortcut.id}-edit`} disabled={saving || Boolean(recording)} aria-label={t("shortcut.changeAria", { name: label })} onClick={(event) => void beginRecording({ id: shortcut.id, mode: "replace" }, event.currentTarget)}><Pencil className="size-4" aria-hidden="true" /></button>
													</TooltipTrigger>
													<TooltipContent>{t("shortcut.changeAria", { name: label })}</TooltipContent>
												</Tooltip>
												<button type="button" className="shortcut-icon-button" id={`${contentId}-${shortcut.id}-options`} aria-label={t("shortcut.manageAria", { name: label })} aria-expanded={isExpanded} aria-controls={`${contentId}-${shortcut.id}-manage`} disabled={saving || Boolean(recording)} onClick={() => setExpanded(isExpanded ? null : shortcut.id)}><MoreHorizontal className="size-4" aria-hidden="true" /></button>
											</>}
										</div>
										</div>
									</div>
									{isRecording ? <div ref={captureRef} className="shortcut-recorder" tabIndex={0} role="group" aria-label={t("shortcut.recordingAria", { name: label })} aria-describedby={`${contentId}-recording-help`} onBlur={(event) => { if (!event.currentTarget.contains(event.relatedTarget)) endRecording(false); }}>
										<Keyboard className="size-5 shrink-0" aria-hidden="true" />
										<div className="min-w-0 flex-1"><p>{recordingReady ? t("shortcut.pressRecording") : t("shortcut.preparing")}</p><p id={`${contentId}-recording-help`} className="shortcut-recording-help">{t("shortcut.recordingHelp")}</p>{recordingError ? <p role="alert" className="shortcut-error">{recordingError}</p> : null}</div>
										<Button type="button" variant="outline" className="shortcut-button" onClick={() => endRecording()}>{t("confirm.cancel")}</Button>
									</div> : null}
									<AnimatePresence initial={false}>
									{isExpanded && !isRecording ? <motion.div
										key="shortcut-management"
										className="overflow-hidden"
										initial={reduceMotion ? false : { height: 0, opacity: 0 }}
										animate={{ height: "auto", opacity: 1 }}
										exit={reduceMotion ? undefined : { height: 0, opacity: 0 }}
										transition={reduceMotion ? { duration: 0 } : { duration: 0.18, ease: [0.22, 1, 0.36, 1] }}
									>
									<div className="shortcut-management" id={`${contentId}-${shortcut.id}-manage`}>
										{bindings.map((binding, index) => <div className="shortcut-manage-binding" key={`${binding.key}-${index}`}><BindingKeys binding={binding} isMac={isMac} /><Tooltip><TooltipTrigger asChild><button type="button" className="shortcut-icon-button" disabled={saving} aria-label={t("shortcut.removeBindingAria", { binding: shortcutBindingLabel(binding, isMac), name: label })} onClick={() => void handleRemoveBinding(shortcut.id, index)}><Trash2 className="size-4" aria-hidden="true" /></button></TooltipTrigger><TooltipContent>{t("shortcut.removeBindingAria", { binding: shortcutBindingLabel(binding, isMac), name: label })}</TooltipContent></Tooltip></div>)}
										<div className="shortcut-manage-actions">
											{bindings.length < 2 ? <Button type="button" variant="outline" className="shortcut-button" disabled={saving} aria-label={t("shortcut.addAltAria", { name: label })} onClick={(event) => void beginRecording({ id: shortcut.id, mode: "add" }, event.currentTarget)}><Plus className="size-4" aria-hidden="true" />{t("shortcut.addAlternative")}</Button> : null}
											{modified ? <Button type="button" variant="ghost" className="shortcut-button" disabled={saving} aria-label={t("shortcut.resetAria", { name: label })} onClick={() => void handleResetBinding(shortcut.id)}><RotateCcw className="size-4" aria-hidden="true" />{t("shortcut.restoreDefault")}</Button> : null}
											<Button type="button" variant="ghost" className="shortcut-button ml-auto" aria-label={t("shortcut.closeManageAria", { name: label })} onClick={() => { setExpanded(null); document.getElementById(`${contentId}-${shortcut.id}-options`)?.focus(); }}>{t("common.close")}</Button>
										</div>
									</div>
									</motion.div> : null}
									</AnimatePresence>
									{mutationError?.id === shortcut.id ? <p role="alert" className="shortcut-error shortcut-row-error">{mutationError.message}</p> : null}
								</div>;
							})}
						</div>
					</section>;
				})}
						</> : null}
				{filteredShortcuts.length === 0 ? <div className="shortcut-empty"><Search className="size-6" aria-hidden="true" /><p>{modifiedOnly && !query ? t("shortcut.noModified") : t("shortcut.noMatching")}</p><span>{modifiedOnly && !query ? t("shortcut.noModifiedHelp") : t("shortcut.noMatchingHelp")}</span><Button type="button" variant="outline" className="shortcut-button" onClick={() => { setQuery(""); setModifiedOnly(false); searchRef.current?.focus(); }}>{t("shortcut.showAll")}</Button></div> : null}
						</div>
					</div>
				</SettingsSection>

			{toast ? (
				<div
					className="pointer-events-auto absolute bottom-4 right-4 z-10 flex w-[min(24rem,100%)] items-start gap-3 rounded-xl border border-(--color-border-settings-dialog) bg-settings-dialog px-4 py-3 shadow-[var(--shadow-settings-dialog)]"
					role="status"
					aria-live="polite"
					aria-atomic="true"
				>
					{toast.error ? <AlertCircle className="mt-0.5 size-4 shrink-0 text-error" aria-hidden="true" /> : <Check className="mt-0.5 size-4 shrink-0 text-success" aria-hidden="true" />}
					<div className="min-w-0 flex-1">
						<p className="text-sm font-medium text-settings-label">{toast.title}</p>
						{toast.body ? <p className="mt-0.5 text-caption text-settings-muted">{toast.body}</p> : null}
					</div>
					{toast.undo ? (
						<button
							type="button"
							className="shortcut-button text-control font-medium text-settings-label hover:underline"
							disabled={saving}
							onClick={() => void toast.undo?.()}
						>
							{t("shortcut.undo")}
						</button>
					) : null}
					<button
						type="button"
						className="shortcut-icon-button"
						aria-label={t("shortcut.dismissNotification")}
						onClick={() => setToast(null)}
					>
						<X className="size-icon-base" aria-hidden="true" />
					</button>
				</div>
			) : null}
			</div>

			<ConfirmDialog
				open={confirmResetAll}
				title={t("shortcut.resetAll")}
				description={t("shortcut.resetAllConfirm")}
				confirmLabel={t("shortcut.resetAll")}
				destructive
				busy={resettingAll}
				error={resetAllError}
				onOpenChange={(next) => {
					setConfirmResetAll(next);
					if (!next) setResetAllError(null);
				}}
				onConfirm={() => {
					if (resettingAll) return;
					setResettingAll(true);
					setResetAllError(null);
					resetAll()
						.then(() => {
							setConfirmResetAll(false);
							showToast({ title: t("shortcut.restoredDefaults") });
						})
						.catch(() => setResetAllError(t("shortcut.resetAllFailed")))
						.finally(() => setResettingAll(false));
				}}
			/>

			<ConfirmDialog
				open={conflict !== null}
				title={t("shortcut.alreadyInUse")}
				description={
					conflict
						? t("shortcut.conflictBody", {
								binding: shortcutBindingLabel(conflict.binding, isMac),
								from: shortcutLabel(conflict.conflictingId, t),
								to: shortcutLabel(conflict.targetId, t),
							})
						: ""
				}
				confirmLabel={t("shortcut.reassign")}
				busy={saving}
				error={conflictError}
				onOpenChange={(next) => {
					if (!next) { setConflict(null); setConflictError(null); }
				}}
				onConfirm={() => {
					if (!conflict) return;
					const pending = conflict;
					setConflictError(null);
					void applyBinding(pending.targetId, pending.binding, pending.mode, pending.conflictingId).then((saved) => {
						if (saved) setConflict(null);
						else setConflictError(t("shortcut.saveFailed"));
					});
				}}
			/>
		</>
	);
}
