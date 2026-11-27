import { useCallback, useEffect, useRef, useState } from "react";
import { useTranslation } from "react-i18next";
import { cn } from "@/lib/utils";
import { aoBridge } from "../lib/bridge";
import { captureRendererEvent, exitFeedbackAllowed } from "../lib/telemetry";
import {
	EXIT_FEEDBACK_ANSWERS,
	EXIT_FEEDBACK_TEXT_LIMIT,
	EXIT_FEEDBACK_TIMEOUT_MS,
	type ExitFeedbackVersion,
} from "../../shared/exit-feedback";
import { Button } from "./ui/button";
import { Dialog, DialogContent, DialogDescription, DialogTitle, settingsDialogContentClass, settingsDialogFooterClass } from "./ui/dialog";

export const EXIT_FEEDBACK_DOCS_URL = "https://github.com/OrchestratorInc/agent-orchestrator/blob/main/docs/telemetry.md#exit-feedback-prompt";

type Dismissal = "skip" | "escape" | "second_close" | "timeout";

/**
 * The first-day exit feedback prompt. Main asks for it while it holds a quit
 * and quits once this reports "done" (or declines). Rendered outside the shell
 * readiness gate, like the quit confirm.
 */
export function ExitFeedbackPrompt() {
	const [version, setVersion] = useState<ExitFeedbackVersion | null>(null);
	useEffect(
		() =>
			aoBridge.app.onExitFeedbackRequest(({ version: requested }) => {
				void exitFeedbackAllowed()
					.catch(() => false)
					.then((allowed) => {
						if (allowed) setVersion(requested);
						else aoBridge.app.reportExitFeedback("declined");
					});
			}),
		[],
	);
	const done = useCallback(() => {
		setVersion(null);
		aoBridge.app.reportExitFeedback("done");
	}, []);
	return version ? <ExitFeedbackDialog version={version} onDone={done} /> : null;
}

export function ExitFeedbackDialog({ version, onDone }: { version: ExitFeedbackVersion; onDone: () => void }) {
	const { t } = useTranslation();
	const [text, setText] = useState("");
	const shownAt = useRef(Date.now());
	const finished = useRef(false);
	const typing = useRef(false);
	const shownReported = useRef(false);
	const timer = useRef<ReturnType<typeof setTimeout> | null>(null);

	const stopCountdown = () => {
		if (timer.current) clearTimeout(timer.current);
		timer.current = null;
	};
	const finish = useCallback(
		async (event: "ao.exit_feedback.answered" | "ao.exit_feedback.dismissed", properties: Record<string, unknown>) => {
			if (finished.current) return;
			finished.current = true;
			stopCountdown();
			const seconds_open = Math.floor((Date.now() - shownAt.current) / 1000);
			try {
				await captureRendererEvent(event, { version, seconds_open, ...properties });
			} catch {
				// A failed send never keeps the app open.
			}
			onDone();
		},
		[onDone, version],
	);
	const dismiss = useCallback((how: Dismissal) => void finish("ao.exit_feedback.dismissed", { how }), [finish]);

	useEffect(() => {
		if (!shownReported.current) {
			shownReported.current = true;
			shownAt.current = Date.now();
			aoBridge.app.reportExitFeedback("shown");
			void captureRendererEvent("ao.exit_feedback.shown", { version }).catch(() => false);
		}
		timer.current = setTimeout(() => dismiss("timeout"), EXIT_FEEDBACK_TIMEOUT_MS);
		return stopCountdown;
	}, [dismiss, version]);
	useEffect(() => aoBridge.app.onExitFeedbackSecondClose(() => dismiss("second_close")), [dismiss]);

	const onTextChange = (value: string) => {
		setText(value);
		// Typing stops the countdown for good, so nothing written is lost.
		if (!typing.current && value !== "") {
			typing.current = true;
			stopCountdown();
			aoBridge.app.reportExitFeedback("typing");
		}
	};
	const question = version === "no_project" ? t("exitFeedback.question.no_project") : t("exitFeedback.question.project_no_work");

	return (
		<Dialog open onOpenChange={() => undefined}>
			<DialogContent
				showCloseButton={false}
				data-testid="exit-feedback-dialog"
				className={cn(settingsDialogContentClass, "w-[min(440px,calc(100vw-24px))]")}
				// Focus the dialog itself, never an answer, so Enter or Space picks nothing.
				onOpenAutoFocus={(event) => {
					event.preventDefault();
					(event.currentTarget as HTMLElement | null)?.focus();
				}}
				onEscapeKeyDown={(event) => {
					event.preventDefault();
					dismiss("escape");
				}}
				onInteractOutside={(event) => event.preventDefault()}
			>
				<div className="flex flex-col gap-3 p-5">
					<DialogTitle id="exit-feedback-question" className="settings-dialog-title text-base">{question}</DialogTitle>
					<div role="group" aria-labelledby="exit-feedback-question" className="flex flex-col gap-1.5">
						{EXIT_FEEDBACK_ANSWERS[version].map((answer) => (
							<Button
								key={answer}
								type="button"
								variant="secondary"
								className="w-full justify-start"
								onClick={() => void finish("ao.exit_feedback.answered", { answer, text })}
							>
								{t(ANSWER_KEYS[answer as keyof typeof ANSWER_KEYS])}
							</Button>
						))}
					</div>
					<textarea
						value={text}
						maxLength={EXIT_FEEDBACK_TEXT_LIMIT}
						placeholder={t("exitFeedback.textPlaceholder")}
						aria-label={t("exitFeedback.textPlaceholder")}
						onChange={(event) => onTextChange(event.target.value)}
						className="min-h-16 w-full resize-none rounded-md border border-transparent bg-input/50 px-3 py-2 text-[13px] leading-5 outline-none focus-visible:border-ring focus-visible:ring-2 focus-visible:ring-ring/30"
					/>
					<DialogDescription className="text-caption leading-5 text-settings-muted">
						{t("exitFeedback.disclosure")}{" "}
						<a
							href={EXIT_FEEDBACK_DOCS_URL}
							className="underline underline-offset-2 hover:text-foreground"
							onClick={(event) => {
								event.preventDefault();
								void aoBridge.app.openExternal(EXIT_FEEDBACK_DOCS_URL);
							}}
						>
							{t("exitFeedback.whatWeCollect")}
						</a>
					</DialogDescription>
				</div>
				<div className={cn(settingsDialogFooterClass, "px-3 py-2.5")}>
					<Button type="button" variant="ghost" onClick={() => dismiss("skip")}>
						{t("exitFeedback.skip")}
					</Button>
				</div>
			</DialogContent>
		</Dialog>
	);
}

const ANSWER_KEYS = {
	just_looking: "exitFeedback.answer.just_looking",
	unsure_where_to_start: "exitFeedback.answer.unsure_where_to_start",
	no_repo_or_agent: "exitFeedback.answer.no_repo_or_agent",
	something_broke: "exitFeedback.answer.something_broke",
	not_a_fit: "exitFeedback.answer.not_a_fit",
	no_task_in_mind: "exitFeedback.answer.no_task_in_mind",
	unsure_what_to_ask: "exitFeedback.answer.unsure_what_to_ask",
	agent_not_ready: "exitFeedback.answer.agent_not_ready",
	tried_nothing_started: "exitFeedback.answer.tried_nothing_started",
	not_comfortable: "exitFeedback.answer.not_comfortable",
} as const;
