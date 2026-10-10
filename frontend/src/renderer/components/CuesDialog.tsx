import { useEffect, useRef, useState } from "react";
import { useTranslation } from "react-i18next";
import { Loader2, MessageSquare, Pencil, Plus, TerminalSquare, Trash2, X } from "lucide-react";
import { apiErrorMessage } from "../lib/api-client";
import { useUiStore } from "../stores/ui-store";
import {
	useCreateCueMutation,
	useDeleteCueMutation,
	useProjectCuesQuery,
	useUpdateCueMutation,
} from "../hooks/useCuesQuery";
import { CUE_LIMITS } from "../lib/cues";
import type { CueDTO, CueInput } from "../lib/cues";
import { Button } from "./ui/button";
import { ConfirmDialog } from "./ConfirmDialog";
import {
	Dialog,
	DialogContent,
	DialogHeader,
	DialogTitle,
	settingsDialogBodyClass,
	settingsDialogContentClass,
	settingsDialogHeaderClass,
} from "./ui/dialog";
import { SettingsOptionMenu } from "./settings/SettingsOptionMenu";

type CuesSettingsProps = {
	projectId: string;
	onBusyChange?: (busy: boolean) => void;
	createOnly?: boolean;
	onCreated?: () => void;
	onCancel?: () => void;
	hideCueActions?: boolean;
};

type CueType = "command" | "agent";

function cueType(cue: CueDTO): CueType {
	return cue.type === "agent" ? "agent" : "command";
}

type CueDraft = {
	name: string;
	type: CueType;
	command: string;
	prompt: string;
};

function emptyDraft(): CueDraft {
	return { name: "", type: "command", command: "", prompt: "" };
}

function draftFromDTO(cue: CueDTO): CueDraft {
	return {
		name: cue.name,
		type: cueType(cue),
		command: cue.command ?? "",
		prompt: cue.prompt ?? "",
	};
}

function CueTypeIcon({ type, className }: { type: CueType; className?: string }) {
	if (type === "agent") {
		return <MessageSquare aria-hidden="true" className={className} />;
	}
	return <TerminalSquare aria-hidden="true" className={className} />;
}

export function CuesSettings(props: CuesSettingsProps) {
	return <ProjectCuesSettings key={props.projectId} {...props} />;
}

function ProjectCuesSettings({ projectId, onBusyChange, createOnly = false, onCreated, onCancel, hideCueActions = false }: CuesSettingsProps) {
	const { t } = useTranslation();
	const showGlobalToast = useUiStore((state) => state.showGlobalToast);
	const cuesQuery = useProjectCuesQuery(projectId, !createOnly);
	useEffect(() => {
		if (!createOnly && cuesQuery.isError && !cuesQuery.isFetching) {
			showGlobalToast(t("cues.loadFailed"), apiErrorMessage(cuesQuery.error, t("cues.loadFailed")), { tone: "error", dedupeKey: `cues.load.${projectId}` });
		}
	}, [projectId, createOnly, cuesQuery.isError, cuesQuery.isFetching, cuesQuery.error, showGlobalToast, t]);
	const createMutation = useCreateCueMutation(projectId);
	const updateMutation = useUpdateCueMutation(projectId);
	const deleteMutation = useDeleteCueMutation(projectId);

	const [formOpen, setFormOpen] = useState<"new" | CueDTO | null>(createOnly ? "new" : null);
	const [deletingCue, setDeletingCue] = useState<CueDTO | null>(null);
	const [draft, setDraft] = useState<CueDraft>(emptyDraft);
	const [formError, setFormError] = useState<string | null>(null);
	const [saving, setSaving] = useState(false);
	const [deleting, setDeleting] = useState(false);
	const pending = useRef(false);
	const mounted = useRef(true);
	useEffect(() => {
		mounted.current = true;
		return () => { mounted.current = false; };
	}, []);
	const busy = saving || deleting;
	useEffect(() => {
		onBusyChange?.(busy);
		return () => onBusyChange?.(false);
	}, [busy, onBusyChange]);

	const openNew = () => {
		if (pending.current) return;
		setDraft(emptyDraft());
		setFormError(null);
		setFormOpen("new");
	};

	const openEdit = (cue: CueDTO) => {
		if (pending.current) return;
		setDraft(draftFromDTO(cue));
		setFormError(null);
		setFormOpen(cue);
	};

	const handleSave = async () => {
		if (formOpen === null || pending.current) return;
		const trimmedName = draft.name.trim();
		if (!trimmedName) {
			setFormError(t("cues.nameRequired"));
			return;
		}
		const input: CueInput = {
			name: trimmedName,
			type: draft.type,
		};
		if (draft.type === "command") {
			input.command = draft.command;
		} else {
			input.prompt = draft.prompt;
		}
		const content = draft.type === "command" ? draft.command : draft.prompt;
		if (!content.trim()) {
			setFormError(t(draft.type === "command" ? "cues.commandRequired" : "cues.promptRequired"));
			return;
		}
		const encoder = new TextEncoder();
		for (const [value, limit, field] of [[trimmedName, CUE_LIMITS.name, t("cues.nameLabel")], [content, draft.type === "command" ? CUE_LIMITS.command : CUE_LIMITS.prompt, t(draft.type === "command" ? "cues.commandLabel" : "cues.agentLabel")]] as const) {
			if (encoder.encode(value).length > limit) {
				setFormError(t("cues.fieldTooLong", { field, limit }));
				return;
			}
		}
		pending.current = true;
		setSaving(true);
		setFormError(null);
		try {
			if (formOpen === "new") {
				await createMutation.mutateAsync(input);
				if (!mounted.current) return;
			} else {
				await updateMutation.mutateAsync({ cueId: formOpen.id, input });
				if (!mounted.current) return;
			}
			if (createOnly) onCreated?.();
			else setFormOpen(null);
		} catch (error) {
			if (!mounted.current) return;
			showGlobalToast(t("cues.saveFailed"), apiErrorMessage(error, t("cues.saveFailed")), "error");
		} finally {
			pending.current = false;
			if (mounted.current) setSaving(false);
		}
	};

	const handleDelete = async () => {
		if (!deletingCue || pending.current) return;
		pending.current = true;
		setDeleting(true);
		try {
			await deleteMutation.mutateAsync(deletingCue.id);
			if (!mounted.current) return;
			setDeletingCue(null);
		} catch (error) {
			if (!mounted.current) return;
			showGlobalToast(t("cues.deleteFailed"), apiErrorMessage(error, t("cues.deleteFailed")), "error");
		} finally {
			pending.current = false;
			if (mounted.current) setDeleting(false);
		}
	};

	const renderList = () => {
		if (!cuesQuery.isFetchedAfterMount || cuesQuery.isFetching) {
			// Hold the list's footprint while loading so nothing flashes before the real rows.
			return (
				<div className="project-settings-form" aria-busy="true" aria-label={t("cues.loading")}>
					<section><div className="settings-grouped-rows flex w-full flex-col"><div className="settings-row-bar" /></div></section>
				</div>
			);
		}
		if (cuesQuery.isError) {
			return (
				<div className="flex flex-col items-center gap-3 py-8 text-center">
					<p role="alert" className="text-sm text-error">
						{apiErrorMessage(cuesQuery.error, t("cues.loadFailed"))}
					</p>
					<Button type="button" variant="outline" size="sm" onClick={() => void cuesQuery.refetch()}>
						{t("cues.retry")}
					</Button>
				</div>
			);
		}
		const cues = cuesQuery.data ?? [];
		if (cues.length === 0) {
			return (
				<div className="project-settings-form">
					<section>
						<div className="settings-grouped-rows flex w-full flex-col">
							<div className="settings-row-bar">
								<span className="text-sm leading-5 text-settings-label">{t("cues.empty")}</span>
								<Button type="button" variant="ghost" disabled={busy} onClick={openNew}>
									<Plus aria-hidden="true" />
									{t("cues.newCue")}
								</Button>
							</div>
						</div>
					</section>
				</div>
			);
		}
		return (
			<div className="project-settings-form">
				<section>
					<div className="settings-grouped-rows flex w-full flex-col">
						{cues.map((cue) => {
							const cueKind = cueType(cue);
							return (
								<div key={cue.id} className="settings-row-bar">
									<CueTypeIcon type={cueKind} className="size-4 shrink-0 text-settings-muted" />
									<div className="min-w-0 flex-1">
										<div className="flex items-baseline gap-2 text-sm leading-5 text-foreground">
											<span className="truncate font-medium">{cue.name}</span>
											<span className="shrink-0 text-xs text-settings-muted">{cueKind === "agent" ? t("cues.typeName.agent") : t("cues.typeName.command")}</span>
										</div>
									</div>
									{hideCueActions ? null : <div className="flex shrink-0 items-center gap-0.5">
										<Button
											type="button"
											variant="ghost"
											size="icon-sm"
											disabled={busy}
											onClick={() => openEdit(cue)}
											aria-label={t("cues.edit")}
											title={t("cues.edit")}
											className="size-7 shrink-0 p-0 text-settings-muted hover:text-foreground"
										>
											<Pencil className="size-3.5" aria-hidden="true" />
										</Button>
										<Button
											type="button"
											variant="ghost"
											size="icon-sm"
											disabled={busy}
											onClick={() => { if (!pending.current) { deleteMutation.reset(); setDeletingCue(cue); } }}
											aria-label={t("cues.delete")}
											title={t("cues.delete")}
											className="size-7 shrink-0 p-0 text-settings-muted hover:text-destructive"
										>
											<Trash2 className="size-3.5" aria-hidden="true" />
										</Button>
									</div>}
								</div>
							);
						})}
					</div>
				</section>
			</div>
		);
	};

	const renderForm = () => {
		const command = draft.type === "command";
		const contentId = command ? "cue-command" : "cue-prompt";
		return <div className="flex flex-col gap-(--size-settings-section-inner-gap)">
			<div className="flex flex-col gap-1.5">
				<label htmlFor="cue-name" className="settings-field-label">
					{t("cues.nameLabel")}
				</label>
				<input
					id="cue-name"
					value={draft.name}
					onChange={(event) => setDraft((d) => ({ ...d, name: event.target.value }))}
					placeholder={t("cues.namePlaceholder")}
					className="settings-field-control h-(--size-settings-action-height) rounded-md!"
					autoFocus
				/>
			</div>

			<div className="flex flex-col gap-1.5">
				<span className="settings-field-label">{t("cues.typeLabel")}</span>
				<SettingsOptionMenu
					aria-label={t("cues.typeLabel")}
					value={draft.type}
					options={[
						{ value: "command", label: t("cues.typeName.command"), icon: <CueTypeIcon type="command" className="size-3! shrink-0 text-settings-muted" /> },
						{ value: "agent", label: t("cues.typeName.agent"), icon: <CueTypeIcon type="agent" className="size-3! shrink-0 text-settings-muted" /> },
					]}
					onChange={(type) => setDraft((current) => ({ ...current, type }))}
					triggerClassName="w-fit self-start"
					menuAlign="start"
					menuClassName="border-0! shadow-md!"
				/>
			</div>

			<div className="flex flex-col gap-1.5">
				<label htmlFor={contentId} className="settings-field-label">
					{t(command ? "cues.commandLabel" : "cues.agentLabel")}
				</label>
				<textarea
					id={contentId}
					value={command ? draft.command : draft.prompt}
					onChange={(event) => {
						const value = event.target.value;
						setDraft((d) => command ? { ...d, command: value } : { ...d, prompt: value });
					}}
					placeholder={t(command ? "cues.commandPlaceholder" : "cues.promptPlaceholder")}
					className="settings-field-control min-h-(--size-textarea-min) resize-none overflow-y-auto py-2.5 rounded-md! disabled:cursor-not-allowed disabled:opacity-50"
				/>
			</div>


			{formError ? (
				<p role="alert" className="text-caption leading-4 text-error">
					{formError}
				</p>
			) : null}
		</div>;
	};

	// The footer action only exists once a list is showing; an empty list carries its own button.
	const empty = !formOpen && !cuesQuery.isError && (!cuesQuery.isFetchedAfterMount || (cuesQuery.data ?? []).length === 0);
	return (
		<div className="flex flex-col gap-(--size-settings-section-inner-gap)">
			<p className="text-sm leading-5 text-settings-muted">{t("cues.settingsDescription")}</p>
			<fieldset className="min-w-0" disabled={busy}>{formOpen ? renderForm() : renderList()}</fieldset>
			{empty ? null : (
			<div className="flex items-center justify-end gap-2">
				{formOpen ? (
					<>
						<Button type="button" variant="outline" disabled={saving} onClick={() => createOnly ? onCancel?.() : setFormOpen(null)}>
							{t("cues.cancel")}
						</Button>
						<Button type="button" disabled={saving} onClick={() => void handleSave()}>
							{saving ? <Loader2 className="animate-spin" aria-hidden="true" /> : null}
							{formOpen === "new" ? t("cues.create") : t("cues.save")}
						</Button>
					</>
				) : (
					<Button type="button" disabled={busy} onClick={openNew}>
						<Plus aria-hidden="true" />
						{t("cues.newCue")}
					</Button>
				)}
			</div>
			)}

			<ConfirmDialog
				open={deletingCue !== null}
				title={t("cues.deleteTitle")}
				description={deletingCue ? t("cues.deleteBody", { name: deletingCue.name }) : ""}
				confirmLabel={t("cues.delete")}
				destructive
				busy={deleting}
				error={deleteMutation.isError ? apiErrorMessage(deleteMutation.error, t("cues.deleteFailed")) : null}
				onConfirm={() => void handleDelete()}
				onOpenChange={(nextOpen) => {
					if (!nextOpen && !pending.current) setDeletingCue(null);
				}}
			/>
		</div>
	);
}

/** Session-local creation surface; shares the settings editor and validation. */
export function CreateCueDialog({ projectId, open, onOpenChange, onClosed }: {
	projectId: string;
	open: boolean;
	onOpenChange: (open: boolean) => void;
	onClosed?: () => void;
}) {
	const { t } = useTranslation();
	const [busy, setBusy] = useState(false);
	return (
		<Dialog open={open} onOpenChange={(next) => { if (!busy) onOpenChange(next); }}>
			<DialogContent showCloseButton={false} aria-describedby={undefined} className={`${settingsDialogContentClass} w-[min(32rem,calc(100vw-2rem))] max-w-none`}
				onCloseAutoFocus={(event) => {
					if (!onClosed) return;
					event.preventDefault();
					onClosed();
				}}>
				<DialogHeader className={`${settingsDialogHeaderClass} flex flex-row items-center justify-between`}>
					<DialogTitle className="text-2xl font-bold text-foreground">{t("cues.newCue")}</DialogTitle>
					<button type="button" aria-label={t("settings.close")} className="settings-close-button" disabled={busy} onClick={() => onOpenChange(false)}><X aria-hidden="true" className="size-4" /></button>
				</DialogHeader>
				{open ? <div className={settingsDialogBodyClass}>
					<CuesSettings projectId={projectId} createOnly hideCueActions onBusyChange={setBusy}
						onCreated={() => onOpenChange(false)} onCancel={() => onOpenChange(false)} />
				</div> : null}
			</DialogContent>
		</Dialog>
	);
}
