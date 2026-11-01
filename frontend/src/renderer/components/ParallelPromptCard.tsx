import { useQuery, useQueryClient } from "@tanstack/react-query";
import { CircleDot, ListTodo, Pencil, Sparkles, X } from "lucide-react";
import { useEffect, useRef, useState } from "react";
import { useTranslation } from "react-i18next";
import { apiClient, apiErrorCode, apiErrorMessage } from "../lib/api-client";
import {
	clearPendingParallelStart,
	dismissParallelPrompt,
	markSuggestionStarted,
	rankParallelSuggestions,
	reportParallelPromptAction,
	reportParallelPromptShown,
	readPendingParallelStart,
	readStartedSuggestions,
	savePendingParallelStart,
	type ParallelSuggestion,
	type TaskSuggestionView,
} from "../lib/parallel-prompt";
import { POST_START_ERRORS } from "../lib/spawn-errors";
import { isMacPlatform } from "../lib/platform";
import { captureRendererEvent } from "../lib/telemetry";
import { workspaceQueryKey } from "../hooks/useWorkspaceQuery";
import { Button } from "./ui/button";
import { Tooltip, TooltipContent, TooltipTrigger } from "./ui/tooltip";

const START_NEXT_SHORTCUT = isMacPlatform() ? "⌘↵" : "Ctrl+Enter";

export function parallelSuggestionsQueryKey(projectId: string) {
	return ["task-suggestions", projectId] as const;
}

/**
 * The board's "run more at once" card: two or three follow-up tasks AO found
 * for this project, each started as its own worker in its own worktree.
 * Rendered only while the board decides the prompt is on offer.
 */
export function ParallelPromptCard({ projectId, onDismiss }: { projectId: string; onDismiss: () => void }) {
	const { t } = useTranslation();
	const queryClient = useQueryClient();
	const suggestionsQuery = useQuery({
		queryKey: parallelSuggestionsQueryKey(projectId),
		retry: false,
		staleTime: 5 * 60_000,
		queryFn: async () => {
			const { data, error } = await apiClient.GET("/api/v1/projects/{id}/task-suggestions", {
				params: { path: { id: projectId } },
			});
			if (error) throw error;
			return data?.suggestions ?? [];
		},
	});
	// Ranked once per visit, leaving out tasks started on an earlier one. This
	// visit's starts drop out of the list below without another sliding into
	// their place, even as the cached suggestions are updated for the next visit.
	// A failed lookup still offers the built-in tasks.
	const [ranked, setRanked] = useState<ParallelSuggestion[]>([]);
	const rankedOnce = useRef(false);
	useEffect(() => {
		if (suggestionsQuery.isPending || rankedOnce.current) return;
		rankedOnce.current = true;
		setRanked(rankParallelSuggestions(suggestionsQuery.data ?? [], readStartedSuggestions(projectId)));
	}, [projectId, suggestionsQuery.isPending, suggestionsQuery.data]);
	const [started, setStarted] = useState<ReadonlySet<string>>(new Set());
	const [edits, setEdits] = useState<Record<string, string>>({});
	const [editingId, setEditingId] = useState<string>();
	const [pending, setPending] = useState(false);
	const [error, setError] = useState<string>();
	// Bumped when a start's outcome becomes, or stops being, unknown, so rows
	// re-read their saved request.
	const [, setPendingRevision] = useState(0);
	const suggestions = ranked.filter((suggestion) => !started.has(suggestion.id));

	useEffect(() => {
		if (ranked.length > 0) reportParallelPromptShown(projectId, ranked);
	}, [projectId, ranked]);

	const promptFor = (suggestion: ParallelSuggestion): string => {
		if (edits[suggestion.id] !== undefined) return edits[suggestion.id];
		switch (suggestion.source) {
			case "issue": {
				const prompt = t("parallelPrompt.prompt.issue", { number: suggestion.issueNumber, title: suggestion.title });
				return suggestion.url ? `${prompt}\n\n${suggestion.url}` : prompt;
			}
			case "todo":
				return t("parallelPrompt.prompt.todo", { location: `${suggestion.path}:${suggestion.line}`, text: suggestion.text });
			case "starter":
				return t(`parallelPrompt.starter.${suggestion.starter}.prompt`);
		}
	};

	const start = async (targets: ParallelSuggestion[]): Promise<number> => {
		setPending(true);
		setError(undefined);
		let count = 0;
		try {
			// One at a time, stopping at the first refusal: a limit, a missing agent
			// or sign-in applies to the rest too, and the reason is shown here.
			for (const suggestion of targets) {
				// An earlier start that got no answer is resent exactly as it was, with
				// its request id, so the daemon replays it rather than starting a second
				// worker for the same task.
				const saved = readPendingParallelStart(projectId, suggestion.id);
				const brief = saved?.brief ?? promptFor(suggestion).trim();
				if (!brief) continue;
				const requestId = saved?.requestId ?? crypto.randomUUID();
				savePendingParallelStart(projectId, suggestion.id, { requestId, brief });
				// A rejected request got no answer at all: undefined, not a refusal.
				const response = await apiClient.POST("/api/v1/orchestrators/delegate", {
					body: {
						clientRequestId: requestId,
						projectId,
						brief,
						spawnSource: "parallel_prompt",
						// The worker records its issue, so the daemon stops suggesting it.
						...(suggestion.source === "issue" && suggestion.issueId ? { issueId: suggestion.issueId } : {}),
					},
				}).catch(() => undefined);
				const apiError = response?.error;
				const workerId = response?.data?.workerId;
				if (!workerId) {
					void captureRendererEvent("ao.renderer.task_create_failed", { project_id: projectId });
					const code = apiErrorCode(apiError) ?? "";
					// No answer, a failure after the worker may exist, or a replay that
					// finds the first attempt still unfinished.
					const mayHaveStarted = !response || !apiError || POST_START_ERRORS.has(code) || code === "CLIENT_REQUEST_INCOMPLETE";
					if (mayHaveStarted || saved) {
						// Keep the request. A refusal of a retry says nothing about whether
						// the first attempt started, and resending the same id is always
						// safe: with nothing to replay, the daemon starts it under that id.
						if (editingId === suggestion.id) setEditingId(undefined);
						setError(mayHaveStarted ? t("parallelPrompt.startUncertain") : apiErrorMessage(apiError, t("newTask.unableToStart")));
					} else {
						// The daemon refused a first attempt: nothing started, so a retry is a new request.
						clearPendingParallelStart(projectId, suggestion.id);
						setError(apiErrorMessage(apiError, t("newTask.unableToStart")));
					}
					setPendingRevision((revision) => revision + 1);
					break;
				}
				clearPendingParallelStart(projectId, suggestion.id);
				setPendingRevision((revision) => revision + 1);
				void captureRendererEvent("ao.renderer.task_create_succeeded", { project_id: projectId });
				count += 1;
				markSuggestionStarted(projectId, suggestion.id);
				// The cached list predates this start; drop the started task from it.
				queryClient.setQueryData<TaskSuggestionView[]>(parallelSuggestionsQueryKey(projectId), (cached) =>
					cached?.filter((item) => !suggestionMatches(item, suggestion)));
				setStarted((current) => new Set(current).add(suggestion.id));
				if (editingId === suggestion.id) setEditingId(undefined);
			}
		} catch (cause) {
			setError(apiErrorMessage(cause, t("newTask.unableToStart")));
		} finally {
			setPending(false);
			await queryClient.invalidateQueries({ queryKey: workspaceQueryKey });
		}
		return count;
	};

	const startOne = async (suggestion: ParallelSuggestion) => {
		reportParallelPromptAction("spawn_one", await start([suggestion]));
	};
	const startAll = async () => {
		reportParallelPromptAction("spawn_all", await start(suggestions));
	};
	const edit = (suggestion: ParallelSuggestion) => {
		if (editingId === suggestion.id) {
			setEditingId(undefined);
			return;
		}
		setEdits((current) => ({ ...current, [suggestion.id]: promptFor(suggestion) }));
		setEditingId(suggestion.id);
		reportParallelPromptAction("edit", 1);
	};
	const dismiss = () => {
		dismissParallelPrompt();
		reportParallelPromptAction("dismiss", suggestions.length);
		onDismiss();
	};

	if (suggestions.length === 0) return null;

	return (
		<section
			aria-labelledby="parallel-prompt-title"
			className="mx-3 my-3 rounded-lg bg-card text-sm"
			data-testid="parallel-prompt"
		>
			<header className="flex items-start gap-2 px-4 pt-3 pb-2">
				<Sparkles className="mt-0.5 size-icon-base shrink-0 text-muted-foreground" aria-hidden="true" />
				<div className="min-w-0 flex-1">
					<h2 id="parallel-prompt-title" className="text-md font-medium text-foreground">{t("parallelPrompt.title")}</h2>
					<p className="text-xs text-muted-foreground">{t("parallelPrompt.subtitle")}</p>
				</div>
				<Tooltip>
					<TooltipTrigger asChild>
						<Button aria-label={t("parallelPrompt.dismiss")} size="icon-sm" variant="ghost" onClick={dismiss}>
							<X className="size-icon-base" aria-hidden="true" />
						</Button>
					</TooltipTrigger>
					<TooltipContent side="bottom">{t("parallelPrompt.dismiss")}</TooltipContent>
				</Tooltip>
			</header>
			<ul className="divide-y divide-border border-y border-border">
				{suggestions.map((suggestion) => (
					<li key={suggestion.id} className="px-4 py-2">
						<div className="flex items-center gap-2">
							<SuggestionIcon suggestion={suggestion} />
							<SuggestionLabel suggestion={suggestion} />
							<Tooltip>
								<TooltipTrigger asChild>
									<span className="inline-flex">
										<Button
											aria-expanded={editingId === suggestion.id}
											aria-label={t("parallelPrompt.edit")}
											// A start awaiting confirmation is retried exactly as sent.
											disabled={pending || Boolean(readPendingParallelStart(projectId, suggestion.id))}
											size="icon-sm"
											variant="ghost"
											onClick={() => edit(suggestion)}
										>
											<Pencil className="size-icon-sm" aria-hidden="true" />
										</Button>
									</span>
								</TooltipTrigger>
								<TooltipContent side="bottom">
									{readPendingParallelStart(projectId, suggestion.id) ? t("parallelPrompt.startUncertain") : t("parallelPrompt.edit")}
								</TooltipContent>
							</Tooltip>
							<Button disabled={pending} size="sm" variant="ghost" onClick={() => void startOne(suggestion)}>
								{t("parallelPrompt.start")}
							</Button>
						</div>
						{editingId === suggestion.id ? (
							<textarea
								aria-label={t("parallelPrompt.promptLabel")}
								className="mt-2 block min-h-20 w-full resize-y rounded-md border border-transparent bg-input/50 px-3 py-2 text-sm text-foreground outline-none focus-visible:outline-2 focus-visible:outline-ring"
								disabled={pending}
								value={edits[suggestion.id] ?? ""}
								onChange={(event) => setEdits((current) => ({ ...current, [suggestion.id]: event.target.value }))}
							/>
						) : null}
					</li>
				))}
			</ul>
			<footer className="flex items-center gap-3 px-4 py-2">
				<p className="min-w-0 flex-1 truncate text-xs text-muted-foreground">
					{t("parallelPrompt.shortcutHint", { shortcut: START_NEXT_SHORTCUT })}
				</p>
				{error ? <p role="alert" className="min-w-0 truncate text-xs text-destructive" title={error}>{error}</p> : null}
				<Button aria-busy={pending} disabled={pending} size="sm" onClick={() => void startAll()}>
					{pending ? t("newTask.starting") : t("parallelPrompt.startAll")}
				</Button>
			</footer>
		</section>
	);
}

function SuggestionIcon({ suggestion }: { suggestion: ParallelSuggestion }) {
	const className = "size-icon-base shrink-0 text-muted-foreground";
	if (suggestion.source === "issue") return <CircleDot className={className} aria-hidden="true" />;
	if (suggestion.source === "todo") return <ListTodo className={className} aria-hidden="true" />;
	return <Sparkles className={className} aria-hidden="true" />;
}

function SuggestionLabel({ suggestion }: { suggestion: ParallelSuggestion }) {
	const { t } = useTranslation();
	switch (suggestion.source) {
		case "issue":
			return (
				<span className="min-w-0 flex-1 truncate text-foreground" title={suggestion.title}>
					<span className="mr-1.5 font-mono text-xs text-muted-foreground tabular-nums">#{suggestion.issueNumber}</span>
					{suggestion.title}
				</span>
			);
		case "todo":
			return (
				<span className="flex min-w-0 flex-1 items-baseline gap-2" title={suggestion.text}>
					<span className="min-w-0 truncate text-foreground">{suggestion.text}</span>
					<span className="max-w-[40%] shrink-0 truncate font-mono text-xs text-muted-foreground">{`${suggestion.path}:${suggestion.line}`}</span>
				</span>
			);
		case "starter":
			return <span className="min-w-0 flex-1 truncate text-foreground">{t(`parallelPrompt.starter.${suggestion.starter}.label`)}</span>;
	}
}

function suggestionMatches(item: TaskSuggestionView, suggestion: ParallelSuggestion): boolean {
	if (suggestion.source === "issue") return item.source === "issue" && item.issueNumber === suggestion.issueNumber;
	if (suggestion.source === "todo") return item.source === "todo" && item.path === suggestion.path && item.line === suggestion.line;
	return false;
}
