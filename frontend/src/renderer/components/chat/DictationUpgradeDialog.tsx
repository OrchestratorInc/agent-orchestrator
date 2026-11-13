import { useEffect, useRef, useState, type ReactNode } from "react";
import { useTranslation } from "react-i18next";
import { AudioLines, ChevronRight, Download, Laptop, Search } from "lucide-react";
import { apiClient } from "../../lib/api-client";
import { useUiStore } from "../../stores/ui-store";
import { Button } from "../ui/button";
import { Pressable } from "../ui/pressable";
import { dictationClient, nativeDictationAvailable } from "../../lib/dictation-client";
import { DictationProgressBar, useDictationProgressText } from "./DictationProgress";
import { Dialog, DialogClose, DialogContent, DialogDescription, DialogOverlay, DialogTitle } from "../ui/dialog";
import { CloseButton } from "../ui/close-button";
import { cn } from "../../lib/utils";
import {
	centeredOnboardingDialogClass,
	onboardingPanelBodyClass,
	onboardingPanelDescriptionClass,
	onboardingPanelTitleClass,
} from "../../lib/onboarding-ui";

export const dictationUpgradeKey = "ao.dictation.chooser.v2";
const recommendedModel = "phonon-2.5";
const accurateModel = "handy-computer/parakeet-tdt-0.6b-v3-gguf";
const nativeModel = "native-macos";

type ModelOption = { installed: boolean; available: boolean; bytes: number };

function DictationRow({
	icon,
	label,
	badge,
	hint,
	action,
	disabled,
	chevron,
	installing,
	onClick,
}: {
	icon: ReactNode;
	label: string;
	badge?: string;
	hint?: string;
	action: string;
	disabled?: boolean;
	chevron?: boolean;
	/** Set while this row's model downloads: the row becomes its own progress. */
	installing?: { done: number; total: number; cancelling: boolean; onCancel?: () => void; status?: string };
	onClick: () => void;
}) {
	if (installing) return <DictationInstallingRow icon={icon} label={label} {...installing} />;
	return (
		<Pressable
			className="group flex w-full items-center gap-4 rounded-lg bg-interactive-hover px-4 py-3 text-left hover:bg-interactive-active disabled:pointer-events-none disabled:opacity-50"
			aria-label={action}
			disabled={disabled}
			onClick={onClick}
		>
			<span className="flex shrink-0 text-muted-foreground group-hover:text-foreground">{icon}</span>
			<span className="min-w-0 flex-1">
				<span className="block text-[14px] font-medium text-foreground">{label}</span>
				{hint && <span className="block text-pretty text-[12px] leading-5 text-muted-foreground">{hint}</span>}
			</span>
			{badge && <span className="shrink-0 text-[12px] leading-5 text-muted-foreground">{badge}</span>}
			{chevron && (
				<ChevronRight
					className="size-4 shrink-0 text-muted-foreground group-hover:text-foreground"
					aria-hidden="true"
				/>
			)}
		</Pressable>
	);
}

function DictationInstallingRow({
	icon,
	label,
	done,
	total,
	cancelling,
	onCancel,
	status,
}: {
	icon: ReactNode;
	label: string;
	done: number;
	total: number;
	cancelling: boolean;
	/** Missing while the row waits on something that cannot be cancelled, such as a macOS permission prompt. */
	onCancel?: () => void;
	/** Replaces the byte count, for steps that have none. */
	status?: string;
}) {
	const { t } = useTranslation();
	const progressText = useDictationProgressText(done, total);
	const text = status ?? progressText;
	return (
		<div className="flex w-full items-center gap-4 rounded-lg bg-interactive-hover px-4 py-3">
			<span className="flex shrink-0 text-muted-foreground">{icon}</span>
			<div className="min-w-0 flex-1 space-y-2.5">
				<div className="flex items-baseline justify-between gap-3">
					<span className="truncate text-[14px] font-medium text-foreground">{label}</span>
					<span className="shrink-0 text-[12px] leading-5 tabular-nums text-muted-foreground" role="status">
						{text}
					</span>
				</div>
				<DictationProgressBar done={done} total={total} label={label} />
			</div>
			{onCancel && (
				<Button variant="ghost" size="sm" disabled={cancelling} onClick={onCancel}>
					{t("dictation.cancel")}
				</Button>
			)}
		</div>
	);
}

export function useDictationUpgrade(ownerKey?: string) {
	const { t } = useTranslation();
	const [open, setOpen] = useState(false);
	const [phase, setPhase] = useState<"choose" | "install" | "cancel">("choose");
	const [active, setActive] = useState("");
	const [options, setOptions] = useState<{
		native: boolean;
		models: Record<string, ModelOption>;
		downloading: string;
	}>({
		native: false,
		models: {},
		downloading: "",
	});
	const target = useRef(recommendedModel);
	const [progress, setProgress] = useState({ done: 0, total: 0 });
	const [error, setError] = useState<string>();
	const start = useRef<() => void>(() => {});
	const checking = useRef(false);
	const generation = useRef(0);
	const operation = useRef(0);
	useEffect(() => {
		setOpen(false);
		setPhase("choose");
		setActive("");
		checking.current = false;
		return () => {
			generation.current += 1;
			operation.current += 1;
		};
	}, [ownerKey]);
	const acknowledge = () => {
		try {
			localStorage.setItem(dictationUpgradeKey, "seen");
		} catch {
			/* Storage can be unavailable in previews. */
		}
	};
	const requestStart = async (onStart: () => void) => {
		if (checking.current || open) return;
		checking.current = true;
		const current = generation.current;
		try {
			const { data, error } = await apiClient.GET("/api/v1/dictation");
			if (current !== generation.current) return;
			if (error || !data) throw new Error(t("dictation.loadFailed"));
			let seen = false;
			try {
				seen = localStorage.getItem(dictationUpgradeKey) === "seen";
			} catch {
				/* Show the chooser. */
			}
			const usable = data.models.some((model) => model.id === data.selected && model.installed && model.available);
			if (seen && usable) {
				onStart();
				return;
			}
			const models: Record<string, ModelOption> = {};
			for (const id of [recommendedModel, accurateModel]) {
				const found = data.models.find((model) => model.id === id);
				if (found)
					models[id] = {
						installed: found.installed,
						available: found.available,
						bytes: found.downloadBytes ?? 0,
					};
			}
			const next = {
				native: nativeDictationAvailable() && data.models.some((model) => model.id === nativeModel && model.available),
				models,
				downloading: data.installing ?? "",
			};
			setOptions(next);
			start.current = onStart;
			setError(undefined);
			setPhase("choose");
			setOpen(true);
			// An install already under way (started earlier, or from Settings) is shown as that row's progress.
			if (next.downloading in models) void choose(next.downloading, next);
		} catch {
			if (current === generation.current) onStart();
		} finally {
			if (current === generation.current) checking.current = false;
		}
	};
	const change = async (action: "install" | "select" | "cancel", model: string) => {
		const { data, error } = await apiClient.POST("/api/v1/dictation", {
			body: { action, model },
		});
		if (error || !data) throw new Error(error?.message ?? t("dictation.updateFailed"));
		return data;
	};
	const choose = async (model: string, from = options) => {
		const native = model === nativeModel;
		target.current = model;
		const current = ++operation.current;
		const valid = () => current === operation.current;
		const needsInstall = !native && !from.models[model]?.installed;
		setError(undefined);
		// Switching to something already on this machine is instant; only a download, or the one-time
		// macOS permission prompts, earn a progress state.
		if (needsInstall || native) {
			setPhase("install");
			setActive(model);
			setProgress({ done: 0, total: 0 });
		}
		try {
			if (native) {
				// Both macOS prompts (speech recognition, microphone) are settled here, once, so they never
				// interrupt the first dictation. When access was already granted this returns at once.
				const access = await dictationClient.authorize();
				if (!valid()) return;
				if (!access.granted) throw new Error(access.message ?? t("dictation.updateFailed"));
			}
			if (needsInstall) {
				let data =
					from.downloading === model ? (await apiClient.GET("/api/v1/dictation")).data : await change("install", model);
				if (!valid()) return;
				while (valid()) {
					if (!data) throw new Error(t("dictation.loadFailed"));
					if (!data.installing) {
						if (!data.models.some((entry) => entry.id === model && entry.installed))
							throw new Error(data.error || t("dictation.updateFailed"));
						break;
					}
					if (data.installing !== model) throw new Error(t("dictation.updateFailed"));
					setProgress({ done: data.progressDone, total: data.progressTotal });
					await new Promise((resolve) => setTimeout(resolve, 500));
					if (!valid()) return;
					const response = await apiClient.GET("/api/v1/dictation");
					if (!valid()) return;
					if (response.error) throw new Error(t("dictation.loadFailed"));
					data = response.data;
				}
			}
			if (!valid()) return;
			await change("select", model);
			if (!valid()) return;
			acknowledge();
			// Whatever was chosen is now ready: get out of the way and start listening.
			setOpen(false);
			setPhase("choose");
			setActive("");
			start.current();
		} catch (cause) {
			if (!valid()) return;
			setPhase("choose");
			setActive("");
			setOptions((previous) => ({ ...previous, downloading: "" }));
			setError(cause instanceof Error ? cause.message : t("dictation.updateFailed"));
		}
	};
	const cancel = async () => {
		const current = ++operation.current;
		setPhase("cancel");
		try {
			await change("cancel", target.current);
			if (current !== operation.current) return;
			setOptions((previous) => ({ ...previous, downloading: "" }));
			setPhase("choose");
			setActive("");
		} catch (cause) {
			if (current !== operation.current) return;
			setPhase("choose");
			setActive("");
			setError(cause instanceof Error ? cause.message : t("dictation.updateFailed"));
		}
	};
	const modelIcon = (model: ModelOption) =>
		model.installed ? (
			<AudioLines className="size-6" aria-hidden="true" strokeWidth={1.6} />
		) : (
			<Download className="size-6" aria-hidden="true" strokeWidth={1.6} />
		);
	const busy = phase !== "choose";
	// Closing the chooser abandons a choice still waiting on the daemon, so a late reply cannot start dictation.
	const dismiss = () => {
		operation.current += 1;
		setOpen(false);
	};
	const installingProps = (model: string) => {
		if (active !== model) return undefined;
		// macOS dictation has nothing to download; its row waits on the system's permission prompts.
		if (model === nativeModel)
			return { done: 0, total: 0, cancelling: false, status: t("dictation.upgrade.allowAccess") };
		return {
			done: progress.done,
			total: progress.total,
			cancelling: phase === "cancel",
			onCancel: () => void cancel(),
		};
	};
	return {
		requestStart,
		dialog: (
			<Dialog
				open={open}
				onOpenChange={(next) => {
					if (!next && !busy) dismiss();
				}}
			>
				<DialogContent
					// Opens and closes instantly: no fade or scale, so choosing never waits on an animation.
					className={cn(
						centeredOnboardingDialogClass,
						"data-[state=open]:animate-none data-[state=closed]:animate-none",
					)}
					overlay={<DialogOverlay className="data-[state=open]:animate-none data-[state=closed]:animate-none" />}
					showCloseButton={false}
				>
					{!busy && (
						<DialogClose asChild>
							<CloseButton floating aria-label={t("common.close")} />
						</DialogClose>
					)}
					<DialogTitle className={cn(onboardingPanelTitleClass, "pr-12")}>{t("dictation.upgrade.title")}</DialogTitle>
					<DialogDescription className={onboardingPanelDescriptionClass}>
						{t("dictation.upgrade.description")}
					</DialogDescription>
					<div className={cn(onboardingPanelBodyClass, "gap-3")}>
						<div className="space-y-2">
							{options.native && (
								<DictationRow
									icon={<Laptop className="size-6" aria-hidden="true" strokeWidth={1.6} />}
									label={t("dictation.upgrade.nativeName")}
									hint={t("dictation.upgrade.nativeDetail")}
									action={t("dictation.upgrade.native")}
									disabled={busy}
									onClick={() => void choose(nativeModel)}
								/>
							)}
							{options.models[recommendedModel] && (
								<DictationRow
									icon={modelIcon(options.models[recommendedModel])}
									label="Phonon 2.5"
									badge={t("dictation.upgrade.recommended")}
									hint={t(
										options.models[recommendedModel].installed
											? "dictation.upgrade.installed"
											: "dictation.upgrade.download",
										{ size: Math.round(options.models[recommendedModel].bytes / 1_000_000) },
									)}
									action={t("dictation.upgrade.install")}
									disabled={busy || !options.models[recommendedModel].available}
									installing={installingProps(recommendedModel)}
									onClick={() => void choose(recommendedModel)}
								/>
							)}
							{options.models[accurateModel] && (
								<DictationRow
									icon={modelIcon(options.models[accurateModel])}
									label="Parakeet TDT 0.6B v3"
									badge={t("dictation.upgrade.accurate")}
									hint={t(
										options.models[accurateModel].installed
											? "dictation.upgrade.installed"
											: "dictation.upgrade.download",
										{ size: Math.round(options.models[accurateModel].bytes / 1_000_000) },
									)}
									action={t("dictation.upgrade.useAccurate")}
									disabled={busy || !options.models[accurateModel].available}
									installing={installingProps(accurateModel)}
									onClick={() => void choose(accurateModel)}
								/>
							)}
							<DictationRow
								chevron
								icon={<Search className="size-6" aria-hidden="true" strokeWidth={1.6} />}
								label={t("dictation.upgrade.explore")}
								hint={t("dictation.upgrade.exploreDetail")}
								action={t("dictation.upgrade.explore")}
								disabled={busy}
								onClick={() => {
									acknowledge();
									dismiss();
									useUiStore.getState().openGlobalSettings("dictation");
								}}
							/>
						</div>
						{error && (
							<p className="text-caption leading-4 text-error" role="alert">
								{error}
							</p>
						)}
					</div>
				</DialogContent>
			</Dialog>
		),
	};
}
