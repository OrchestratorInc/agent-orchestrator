import { useQuery } from "@tanstack/react-query";
import { Check, Download } from "@/components/app-icons";
import { useTranslation } from "react-i18next";
import { workspaceQueryOptions } from "../hooks/useWorkspaceQuery";
import { agentLabel } from "../lib/agent-options";
import { cn } from "../lib/utils";
import { countBy, importableProject, projectChats, sessionImportPreviewQuery, useImportOfferShown } from "../lib/session-import";
import { useSessionImportStore } from "../stores/session-import-store";
import { AgentAvatar } from "./AgentAvatar";
import { useImportRun } from "./SessionImportDialog";
import { Button } from "./ui/button";
import { CloseButton } from "./ui/close-button";

export function ProjectImportOffer({ projectId, className }: { projectId: string; className?: string }) {
	const { t } = useTranslation();
	const workspaces = useQuery(workspaceQueryOptions);
	const project = workspaces.data?.find((workspace) => workspace.id === projectId);
	const dismissed = useSessionImportStore((state) => state.dismissedProjects.includes(projectId));
	const importedCount = useSessionImportStore((state) => state.importedCounts[projectId]);
	const dismissProject = useSessionImportStore((state) => state.dismissProject);
	const openProjectImport = useSessionImportStore((state) => state.openProjectImport);
	const recordImport = useSessionImportStore((state) => state.recordImport);
	const enabled = Boolean(project && importableProject(project)) && !dismissed;
	const preview = useQuery({ ...sessionImportPreviewQuery, enabled, staleTime: 60_000 });
	const chats = projectChats(preview.data?.candidates, projectId);
	const { importing, error, run } = useImportRun("project_offer", (results) => {
		const created = results.filter((result) => result.status === "created").length;
		if (created) recordImport(projectId, created);
	});
	useImportOfferShown("project_board", enabled && !importedCount && chats.length > 0);
	if (!enabled || !project) return null;
	const dismiss = (
		<CloseButton size="icon-xs" className="shrink-0" aria-label={t("sessionImport.projectOfferDismiss")} onClick={() => dismissProject(projectId)} />
	);
	const alert = error ? (
		<p role="alert" className="mt-1.5 px-1 text-xs text-destructive">
			{error}
		</p>
	) : null;
	if (importedCount) {
		return (
			<div className={className}>
				<div role="status" className="flex items-center gap-2 px-1 text-xs text-muted-foreground">
					<Check aria-hidden="true" className="size-3.5 shrink-0 text-success" />
					<span className="min-w-0 flex-1 truncate">{t("sessionImport.projectImported", { count: importedCount, project: project.name })}</span>
					{dismiss}
				</div>
				{alert}
			</div>
		);
	}
	if (!chats.length) return null;
	const perAgent = countBy(chats.map((chat) => chat.harness));
	return (
		<div className={cn("animate-in fade-in-0 duration-300 motion-reduce:animate-none", className)}>
			<div className="flex items-center gap-3 rounded-lg border border-border bg-surface py-1.5 pl-3 pr-1.5">
				<Download aria-hidden="true" className="size-4 shrink-0 text-muted-foreground" />
				<div className="flex min-w-0 flex-1 flex-wrap items-center gap-x-3 gap-y-0.5">
					<span className="whitespace-nowrap text-sm text-foreground">{t("sessionImport.projectOffer", { count: chats.length })}</span>
					<span className="flex items-center gap-2 text-xs text-muted-foreground">
						{[...perAgent].map(([harness, count]) => (
							<span key={harness} className="flex items-center gap-1 whitespace-nowrap">
								<AgentAvatar className="size-3.5" decorative provider={harness} />
								{agentLabel(harness)} {count}
							</span>
						))}
					</span>
				</div>
				<Button size="sm" variant="quiet" disabled={importing} onClick={() => openProjectImport(projectId)}>
					{t("sessionImport.review")}
				</Button>
				<Button size="sm" variant="primary" disabled={importing} onClick={() => void run(chats.map((chat) => chat.id))}>
					{importing ? t("sessionImport.importing") : t("sessionImport.importShort")}
				</Button>
				{dismiss}
			</div>
			{alert}
		</div>
	);
}
