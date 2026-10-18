import { useState, type ReactNode } from "react";
import { useNavigate } from "@tanstack/react-router";
import { Trans, useTranslation } from "react-i18next";
import { Archive, ChevronRight, Pencil } from "lucide-react";
import { AnimatePresence, motion, useReducedMotion } from "motion/react";
import { Button } from "./ui/button";
import { Select, SelectContent, SelectItem, SelectSeparator, SelectTrigger, SelectValue } from "./ui/select";
import { Switch } from "./ui/switch";
import { useCreateAutomation, useUpdateAutomation, type Automation } from "../hooks/useAutomations";
import { useInstructions, useSaveInstructions } from "../hooks/useInstructions";
import { cn } from "../lib/utils";

// Radix Select reserves the empty string, so the user's own scope needs a key.
const USER_SCOPE = "user";
const MAX_INSTRUCTIONS_BYTES = 8 * 1024;

type Project = { id: string; name: string };
type Draft = { text: string; revision: number };

/** "3 minutes ago", or null under a minute so the caller can say "just now". */
function relativeTime(value: string, locale: string | undefined, now = Date.now()) {
	const seconds = Math.round((new Date(value).getTime() - now) / 1000);
	const format = new Intl.RelativeTimeFormat(locale, { numeric: "auto" });
	for (const [unit, size] of [["day", 86_400], ["hour", 3_600], ["minute", 60]] as const) {
		if (Math.abs(seconds) >= size) return format.format(Math.round(seconds / size), unit);
	}
	return null;
}

function TextButton({ onClick, children }: { onClick: () => void; children?: ReactNode }) {
	return <button type="button" onClick={onClick} className="rounded-sm text-foreground underline-offset-2 hover:underline focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring/60">{children}</button>;
}

/**
 * Dreaming's state across projects. Each project has its own dreaming
 * automation; turning dreaming on creates the missing ones and enables the rest.
 */
function useDreaming(automations: Automation[], projects: Project[]) {
	const { t } = useTranslation();
	const create = useCreateAutomation();
	const update = useUpdateAutomation();
	const [switching, setSwitching] = useState(false);
	const [switchError, setSwitchError] = useState<string | null>(null);
	const dreaming = automations.filter((item) => item.purpose === "dreaming");
	const enabledIn = new Set(dreaming.filter((item) => item.enabled).map((item) => item.projectId));
	const onCount = projects.filter((project) => enabledIn.has(project.id)).length;

	async function setDreaming(on: boolean, only?: string) {
		setSwitchError(null);
		setSwitching(true);
		try {
			await Promise.all(projects.filter((project) => only === undefined || project.id === only).map((project) => {
				const existing = dreaming.find((item) => item.projectId === project.id);
				if (existing) return existing.enabled === on ? undefined : update.mutateAsync({ id: existing.id, body: { enabled: on } });
				if (!on) return undefined;
				return create.mutateAsync({
					projectId: project.id, purpose: "dreaming", kind: "worker", trigger: "session_ended",
					// An empty prompt takes the daemon's built-in dreaming prompt.
					displayName: t("automations.dreaming.name"), prompt: "",
					timezone: Intl.DateTimeFormat().resolvedOptions().timeZone || "UTC",
				});
			}));
		} catch (error) {
			setSwitchError(error instanceof Error ? error.message : t("automations.dreaming.switchError"));
		} finally {
			setSwitching(false);
		}
	}

	return { dreaming, enabledIn, onCount, everywhere: projects.length > 0 && onCount === projects.length, switching, switchError, setDreaming };
}

type DreamingState = ReturnType<typeof useDreaming>;

/**
 * Dreaming as a built-in automation: one row with a switch for every project.
 * Opening it shows the instructions AO adds to new sessions, which dreaming
 * keeps up to date. The status line under them links the latest dream, and a
 * failed dream shows its error on the row, so there is no run history.
 */
export function DreamingRow({ automations, projects, expanded, onExpand, onEdit }: {
	automations: Automation[];
	projects: Project[];
	expanded: boolean;
	onExpand: () => void;
	onEdit: () => void;
}) {
	const { t } = useTranslation();
	const reduceMotion = useReducedMotion();
	const dream = useDreaming(automations, projects);
	const latest = dream.dreaming
		.map((item) => item.latestRun)
		.filter((run): run is NonNullable<Automation["latestRun"]> => Boolean(run))
		.sort((a, b) => b.scheduledFor.localeCompare(a.scheduledFor))[0];
	const name = t("automations.dreaming.name");
	const detailsId = "automation-dreaming-details";
	return <article className="group">
		<div className={cn("grid grid-cols-[minmax(0,1fr)_auto] items-center gap-x-4 gap-y-2 px-4 py-3 transition-colors motion-reduce:transition-none lg:grid-cols-[minmax(0,1fr)_180px_160px_132px]", expanded ? "bg-muted/30" : "hover:bg-muted/20")}>
			<button type="button" className="flex min-w-0 items-center gap-2.5 rounded-sm text-left focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring/60" aria-label={t(expanded ? "automations.dreaming.close" : "automations.dreaming.open")} aria-expanded={expanded} aria-controls={detailsId} onClick={onExpand}>
				<ChevronRight aria-hidden="true" className={cn("size-3.5 shrink-0 text-muted-foreground transition-transform duration-150 motion-reduce:transition-none", expanded && "rotate-90")} />
				<div className="min-w-0"><h2 className="truncate text-[13px] font-medium leading-5 text-foreground">{name}</h2><p className="truncate text-[11px] leading-5 text-muted-foreground">{dream.onCount > 0 && !dream.everywhere ? t("automations.dreaming.partial", { on: dream.onCount, total: projects.length }) : t("automations.dreaming.everyProject")}</p></div>
			</button>
			<div className="col-start-1 flex items-start gap-2 pl-6 text-xs lg:col-start-auto lg:pl-0"><Archive aria-hidden="true" className="mt-0.5 size-3.5 shrink-0 text-muted-foreground" /><p>{t("automations.schedule.sessionEnded")}</p></div>
			<div className="col-start-1 pl-6 text-xs lg:col-start-auto lg:pl-0"><span className={cn("tabular-nums", dream.onCount === 0 && "text-muted-foreground")}>{dream.onCount === 0 ? t("automations.dreaming.off") : t("automations.waiting")}</span>{latest ? <p className="mt-1 text-[11px] capitalize text-muted-foreground">{latest.status}</p> : null}</div>
			<div className="col-start-2 row-start-1 row-span-3 flex items-center justify-end gap-1 lg:col-start-auto lg:row-start-auto lg:row-span-1">
				{dream.dreaming.length > 0 ? <Button variant="ghost" size="icon-sm" className="text-muted-foreground lg:opacity-0 lg:group-hover:opacity-100 lg:group-focus-within:opacity-100 focus-visible:ring-2 focus-visible:ring-ring/60" aria-label={t("automations.edit.aria", { name })} onClick={onEdit}><Pencil className="size-3.5" /></Button> : null}
				<Switch checked={dream.everywhere} disabled={dream.switching || projects.length === 0} aria-label={t("automations.dreaming.toggle")} onCheckedChange={(checked) => void dream.setDreaming(checked)} />
			</div>
		</div>
		{dream.switchError ? <p role="alert" className="mx-4 mb-3 rounded-md bg-destructive/10 px-3 py-2 text-xs text-destructive">{dream.switchError}</p> : null}
		{latest?.errorMessage ? <p role="alert" className="mx-4 mb-3 rounded-md bg-destructive/10 px-3 py-2 text-xs text-destructive">{latest.errorMessage}</p> : null}
		<AnimatePresence initial={false}>
			{expanded ? <motion.div id={detailsId} initial={{ height: 0, opacity: 0 }} animate={{ height: "auto", opacity: 1 }} exit={{ height: 0, opacity: 0 }} transition={{ duration: reduceMotion ? 0 : 0.18, ease: "easeOut" }} className="overflow-hidden">
				<div className="border-t border-border bg-muted/10 px-4 py-4 pl-10">
					<DreamingInstructions dream={dream} projects={projects} />
				</div>
			</motion.div> : null}
		</AnimatePresence>
	</article>;
}

/** The instructions AO adds to new sessions: the user's own and each project's. */
function DreamingInstructions({ dream, projects }: { dream: DreamingState; projects: Project[] }) {
	const { t, i18n } = useTranslation();
	const navigate = useNavigate();
	const [scope, setScope] = useState(USER_SCOPE);
	const projectId = scope === USER_SCOPE ? null : scope;
	const instructions = useInstructions(projectId);
	const save = useSaveInstructions(projectId);
	const [drafts, setDrafts] = useState<Record<string, Draft>>({});

	const server = instructions.data;
	const draft = drafts[scope];
	const text = draft?.text ?? server?.body ?? "";
	const dirty = draft !== undefined && server !== undefined && draft.text !== server.body;
	// The draft started from an older revision: a dream (or another edit)
	// landed while the user typed. Saving still keeps the user's text.
	const changedUnderneath = dirty && draft.revision !== server.revision;
	const tooLong = new TextEncoder().encode(text.trim()).length > MAX_INSTRUCTIONS_BYTES;
	const offForProject = projectId !== null && !dream.enabledIn.has(projectId);

	function clearDraft(key = scope) {
		setDrafts(({ [key]: _discarded, ...rest }) => rest);
	}

	function onSave(value = text) {
		if (save.isPending || tooLong) return;
		const key = scope;
		save.mutate(value, { onSuccess: () => clearDraft(key) });
	}

	let status: ReactNode = null;
	if (instructions.isLoading) {
		status = <span role="status">{t("automations.dreaming.loading")}</span>;
	} else if (instructions.error) {
		status = <span role="alert" className="text-destructive">{t("automations.dreaming.loadError")}</span>;
	} else if (save.error) {
		status = <span role="alert" className="text-destructive">{save.error.message}</span>;
	} else if (tooLong) {
		status = <span role="alert" className="text-destructive">{t("automations.dreaming.tooLong")}</span>;
	} else if (changedUnderneath) {
		status = <>{t("automations.dreaming.changedWhileEditing")} <TextButton onClick={() => clearDraft()}>{t("automations.dreaming.showLatest")}</TextButton></>;
	} else if (offForProject) {
		status = <>{t("automations.dreaming.offForProject")} <TextButton onClick={() => void dream.setDreaming(true, projectId)}>{t("automations.dreaming.turnOn")}</TextButton></>;
	} else if (server?.updatedAt) {
		const time = relativeTime(server.updatedAt, i18n.resolvedLanguage) ?? t("automations.dreaming.justNow");
		const author = server.updatedBy;
		const openSession = () => { if (author?.sessionId) void navigate({ to: "/sessions/$sessionId", params: { sessionId: author.sessionId } }); };
		if (author?.dreamed) {
			status = <>
				{t("automations.dreaming.updatedByDream", { time })}
				{author.sessionId ? <> · <TextButton onClick={openSession}>{t("automations.dreaming.viewSession")}</TextButton></> : null}
				{typeof server.previousBody === "string" && !dirty ? <> · <TextButton onClick={() => onSave(server.previousBody ?? "")}>{t("automations.dreaming.undo")}</TextButton></> : null}
			</>;
		} else if (author) {
			status = <Trans i18nKey="automations.dreaming.updatedBySession" values={{ title: author.title || author.sessionId, time }} components={{ session: <TextButton onClick={openSession} /> }} />;
		} else {
			status = t("automations.dreaming.saved", { time });
		}
	}

	const scopeLabel = projectId === null ? t("automations.dreaming.you") : projects.find((project) => project.id === projectId)?.name ?? projectId;
	return <section aria-label={t("automations.dreaming.title")}>
		<p className="text-xs leading-5 text-muted-foreground">{t("automations.dreaming.description")}</p>
		<div className="mt-3">
			<Select value={scope} onValueChange={setScope}>
				<SelectTrigger size="sm" className="h-7 max-w-full text-xs" aria-label={t("automations.dreaming.scope")}><SelectValue /></SelectTrigger>
				<SelectContent>
					<SelectItem value={USER_SCOPE}>{t("automations.dreaming.you")}</SelectItem>
					{projects.length > 0 ? <SelectSeparator /> : null}
					{projects.map((project) => <SelectItem key={project.id} value={project.id}>{project.name}</SelectItem>)}
				</SelectContent>
			</Select>
		</div>
		<textarea
			aria-label={t("automations.dreaming.textLabel", { scope: scopeLabel })}
			value={text}
			disabled={instructions.isLoading || Boolean(instructions.error)}
			placeholder={t(projectId === null ? "automations.dreaming.placeholder.user" : "automations.dreaming.placeholder.project")}
			spellCheck
			onChange={(event) => {
				const value = event.target.value;
				setDrafts((current) => ({ ...current, [scope]: { text: value, revision: current[scope]?.revision ?? server?.revision ?? 0 } }));
			}}
			onKeyDown={(event) => {
				if (event.key === "Enter" && (event.metaKey || event.ctrlKey) && dirty) {
					event.preventDefault();
					onSave();
				}
			}}
			className="mt-2 min-h-36 w-full resize-y rounded-md border border-transparent bg-input/50 px-3 py-2 text-[13px] leading-5 outline-none focus-visible:border-ring focus-visible:ring-2 focus-visible:ring-ring/30 disabled:opacity-60 aria-invalid:border-destructive"
			aria-invalid={tooLong || undefined}
		/>
		<div className="mt-2 flex min-h-7 items-center justify-between gap-3">
			<p className="min-w-0 text-[11px] leading-5 text-muted-foreground">{status}</p>
			<Button size="sm" className="shrink-0" disabled={!dirty || save.isPending || tooLong} onClick={() => onSave()}>{save.isPending ? t("automations.dreaming.saving") : t("automations.dreaming.save")}</Button>
		</div>
	</section>;
}
