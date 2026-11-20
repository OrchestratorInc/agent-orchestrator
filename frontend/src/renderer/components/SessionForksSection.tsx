import { InspectorSection } from "@aoagents/product-ui";
import { useNavigate } from "@tanstack/react-router";
import { GitFork } from "@/components/app-icons";
import { Trans, useTranslation } from "react-i18next";
import { sessionNavigateTarget } from "../lib/navigate-to-session";
import type { WorkspaceSession } from "../types/workspace";
import { ProductExternalLink } from "./ProductExternalLink";
import { useSessionForkOptions } from "./SessionForkControls";
import { Button } from "./ui/button";

/**
 * The inspector's Forks section, in plain sentences: which session this one
 * was forked from, linked, with the original's pull requests as read-only
 * links; and which sessions were forked from this one, each linked. Nothing
 * renders for a session that is neither a fork nor forked.
 */
export function SessionForksSection({ session }: { session: WorkspaceSession }) {
	const options = useSessionForkOptions(session);
	const { t } = useTranslation();
	const navigate = useNavigate();
	const origin = options.data?.forkedFrom;
	const forks = options.data?.forks ?? [];
	const sourcePRs = options.data?.sourcePRs ?? [];
	if (!origin && forks.length === 0) return null;
	const open = (sessionId: string) => void navigate(sessionNavigateTarget(session.workspaceId, sessionId, session.hostId));
	// Inside a sentence, a long name wraps with it rather than spilling out.
	const sessionLink = (sessionId: string) => (
		<Button type="button" variant="link" className="inline whitespace-normal text-left font-medium text-foreground" onClick={() => open(sessionId)} />
	);
	return (
		<InspectorSection surface={false} title={t("sessionFork.sectionTitle")}>
			<div className="flex flex-col gap-3 text-sm" data-testid="session-forks">
				{origin ? (
					<div className="flex gap-2">
						<GitFork aria-hidden="true" className="mt-0.5 size-3.5 shrink-0 text-settings-muted" />
						<div className="flex min-w-0 flex-col gap-1">
							<p className="m-0">
								<Trans
									t={t}
									i18nKey="sessionFork.forkedFromSentence"
									values={{ title: origin.title }}
									components={{ session: origin.sourceExists ? sessionLink(origin.sessionId) : <span className="font-medium" /> }}
								/>
							</p>
							{origin.sourceExists ? null : <p className="m-0 text-xs text-settings-muted">{t("sessionFork.originalDeleted")}</p>}
							{origin.textHandoff ? <p className="m-0 text-xs text-settings-muted">{t("sessionFork.textCopy")}</p> : null}
							{sourcePRs.length > 0 ? (
								<div className="flex flex-wrap items-center gap-1.5">
									<span className="text-xs text-settings-muted">{t("sessionFork.originalPRs")}</span>
									{sourcePRs.map((pr) => (
										<ProductExternalLink
											ariaLabel={t("sessionFork.originalPR", { number: pr.number })}
											className="text-xs text-settings-muted underline-offset-2 hover:underline"
											href={pr.url}
											key={pr.url}
										>
											#{pr.number}
										</ProductExternalLink>
									))}
								</div>
							) : null}
						</div>
					</div>
				) : null}
				{forks.length > 0 ? (
					<div className="flex gap-2">
						<GitFork aria-hidden="true" className="mt-0.5 size-3.5 shrink-0 text-settings-muted" />
						<div className="flex min-w-0 flex-col gap-1">
							<p className="m-0">{t("sessionFork.forkedInto", { count: forks.length })}</p>
							<ul className="m-0 flex list-none flex-col gap-0.5 p-0">
								{forks.map((fork) => (
									<li className="min-w-0 truncate" key={fork.sessionId}>
										{fork.exists ? (
											// Left-aligned and shrinkable, so a long name keeps its start
											// ("Fork 3 of …") and is cut at its end; hover shows it all.
											<Button type="button" variant="link" className="min-w-0 max-w-full shrink justify-start font-medium text-foreground" title={fork.title} onClick={() => open(fork.sessionId)}>
												<span className="truncate">{fork.title}</span>
											</Button>
										) : (
											<span className="text-settings-muted">{t("sessionFork.forkDeleted", { title: fork.title })}</span>
										)}
									</li>
								))}
							</ul>
						</div>
					</div>
				) : null}
			</div>
		</InspectorSection>
	);
}
