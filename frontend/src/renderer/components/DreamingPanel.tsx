import { useState, type ReactNode } from "react";
import { useNavigate } from "@tanstack/react-router";
import { Trans, useTranslation } from "react-i18next";
import { Button } from "./ui/button";
import { Select, SelectContent, SelectItem, SelectSeparator, SelectTrigger, SelectValue } from "./ui/select";
import { Switch } from "./ui/switch";
import { useCreateAutomation, useUpdateAutomation, type Automation } from "../hooks/useAutomations";
import { useInstructions, useSaveInstructions } from "../hooks/useInstructions";

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
 * The instructions AO adds to new sessions, and the switch for dreaming, which
 * keeps them up to date from what the user types. Each project has its own
 * dreaming automation; the switch turns all of them on or off.
 */
export function DreamingPanel({ automations, projects }: { automations: Automation[]; projects: Project[] }) {
	const { t, i18n } = useTranslation();
	const navigate = useNavigate();
	const [scope, setScope] = useState(USER_SCOPE);
	const projectId = scope === USER_SCOPE ? null : scope;
	const instructions = useInstructions(projectId);
	const save = useSaveInstructions(projectId);
	const create = useCreateAutomation();
	const update = useUpdateAutomation();
	const [drafts, setDrafts] = useState<Record<string, Draft>>({});
	const [switching, setSwitching] = useState(false);
	const [switchError, setSwitchError] = useState<string | null>(null);

	const server = instructions.data;
	const draft = drafts[scope];
	const text = draft?.text ?? server?.body ?? "";
	const dirty = draft !== undefined && server !== undefined && draft.text !== server.body;
	// The draft started from an older revision: a dream (or another edit)
	// landed while the user typed. Saving still keeps the user's text.
	const changedUnderneath = dirty && draft.revision !== server.revision;
	const tooLong = new TextEncoder().encode(text.trim()).length > MAX_INSTRUCTIONS_BYTES;

	const dreaming = automations.filter((item) => item.purpose === "dreaming");
	const enabledIn = new Set(dreaming.filter((item) => item.enabled).map((item) => item.projectId));
	const onCount = projects.filter((project) => enabledIn.has(project.id)).length;
	const everywhere = projects.length > 0 && onCount === projects.length;
	const offForProject = projectId !== null && !enabledIn.has(projectId);

	function clearDraft(key = scope) {
		setDrafts(({ [key]: _discarded, ...rest }) => rest);
	}

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
		status = <>{t("automations.dreaming.offForProject")} <TextButton onClick={() => void setDreaming(true, projectId)}>{t("automations.dreaming.turnOn")}</TextButton></>;
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
	return <section aria-labelledby="dreaming-title" className="mb-6">
		<div className="flex items-start justify-between gap-4">
			<div className="min-w-0">
				<h2 id="dreaming-title" className="text-[13px] font-medium leading-5 text-foreground">{t("automations.dreaming.title")}</h2>
				<p className="text-xs leading-5 text-muted-foreground">{t("automations.dreaming.description")}</p>
			</div>
			<div className="flex shrink-0 items-center gap-2 pt-0.5">
				{onCount > 0 && !everywhere ? <span className="text-[11px] tabular-nums text-muted-foreground">{t("automations.dreaming.partial", { on: onCount, total: projects.length })}</span> : null}
				<Switch checked={everywhere} disabled={switching || projects.length === 0} aria-label={t("automations.dreaming.toggle")} onCheckedChange={(checked) => void setDreaming(checked)} />
			</div>
		</div>
		{switchError ? <p role="alert" className="mt-2 text-xs text-destructive">{switchError}</p> : null}
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
