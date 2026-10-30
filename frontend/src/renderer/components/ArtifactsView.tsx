import { BrowserIcon } from "./icons";
import { ChevronDown, Clock, Cloud, File, FileText, Folder, LayoutGrid, List, Loader2, Pin, Plus, Search } from "lucide-react";
import { ScrollArea } from "radix-ui";
import { useId, useMemo, useState, type ReactNode } from "react";
import { useTranslation } from "react-i18next";
import { useCloudOffering } from "../hooks/useCloudOffering";
import { useCreateArtifactSession } from "../hooks/useCreateArtifactSession";
import { useWorkspaceQuery } from "../hooks/useWorkspaceQuery";
import {
	artifactCount,
	artifactKey,
	filterArtifactGroups,
	groupArtifactsByProject,
	type ArtifactRow,
} from "../lib/artifacts";
import { formatTimeCompact } from "../lib/format-time";
import { useNavigateToSession } from "../lib/navigate-to-session";
import { hidesShellTopbar } from "../lib/platform";
import { cn } from "../lib/utils";
import { useArtifactsViewStore, type ArtifactViewMode } from "../stores/artifacts-view-store";
import { useUiStore } from "../stores/ui-store";
import { CLOUD_PROJECT_KIND, STANDALONE_WORKSPACE_ID, type WorkspaceSummary } from "../types/workspace";
import { ArtifactActionsMenu } from "./ArtifactActionsMenu";
import { ArtifactGalleryCard } from "./ArtifactGalleryCard";
import { ArtifactPreviewModal } from "./ArtifactPreviewModal";
import { TopbarButton, topbarProjectLabelClass } from "./TopbarButton";
import { Button } from "./ui/button";
import {
	DropdownMenu,
	DropdownMenuContent,
	DropdownMenuItem,
	DropdownMenuLabel,
	DropdownMenuRadioGroup,
	DropdownMenuRadioItem,
	DropdownMenuSeparator,
	DropdownMenuTrigger,
} from "./ui/dropdown-menu";
import { Input } from "./ui/input";
import { Tooltip, TooltipContent, TooltipTrigger } from "./ui/tooltip";

const ARTIFACT_GRID_CLASS = "grid grid-cols-[repeat(auto-fill,minmax(min(100%,18rem),1fr))] gap-4";
const MAX_LIVE_PREVIEWS = 4;

/**
 * Every artifact agents have written, grouped by project: on this machine, and
 * in cloud sessions when the control plane keeps artifacts.
 */
export function ArtifactsView() {
	const { t } = useTranslation();
	const showPageTitle = hidesShellTopbar();
	const cloudArtifacts = useCloudOffering().artifacts;
	const workspaceQuery = useWorkspaceQuery({ includeCloud: cloudArtifacts });
	const workspaces = workspaceQuery.data;
	const groups = useMemo(() => groupArtifactsByProject(workspaces ?? []), [workspaces]);
	const [query, setQuery] = useState("");
	const [projectId, setProjectId] = useState("");
	const [preview, setPreview] = useState<{ row: ArtifactRow; projectName: string } | null>(null);
	const openPreview = (row: ArtifactRow, projectName: string) => setPreview({ row, projectName });
	const view = useArtifactsViewStore((state) => state.view);
	const setView = useArtifactsViewStore((state) => state.setView);
	const pinnedArtifactIds = useArtifactsViewStore((state) => state.pinnedArtifactIds);
	const scratchpadName = t("standalone.workspaceName");
	const visibleGroups = useMemo(
		() => filterArtifactGroups(groups, query, projectId, scratchpadName),
		[groups, query, projectId, scratchpadName],
	);
	const hasArtifacts = artifactCount(groups) > 0;
	const showProjectSections = visibleGroups.length > 1;
	const cards = useMemo(
		() => visibleGroups
			.flatMap((group) => group.rows.map((row) => ({
				row,
				projectName: group.isScratchpad ? t("standalone.workspaceName") : group.name,
			})))
			.sort((left, right) => (Date.parse(right.row.updatedAt) || 0) - (Date.parse(left.row.updatedAt) || 0)),
		[visibleGroups, t],
	);
	const pinnedCards = useMemo(() => {
		const byId = new Map(cards.map((card) => [artifactKey(card.row), card]));
		return pinnedArtifactIds.flatMap((id) => {
			const card = byId.get(id);
			return card ? [card] : [];
		});
	}, [cards, pinnedArtifactIds]);
	const livePreviewSections = useMemo(() => {
		const owners = new Map<string, string>();
		const sections = [
			{ id: "pinned", rows: pinnedCards.map(({ row }) => row) },
			...(showProjectSections ? [{ id: "recent", rows: cards.slice(0, 3).map(({ row }) => row) }] : []),
			...visibleGroups.map((group) => ({ id: `project:${group.id}`, rows: group.rows })),
		];
		for (const section of sections) {
			for (const row of section.rows) {
				const page = row.kind === "html" && (row.cloud || (row.inlineUrl && row.rawUrl));
				const pdf = !row.cloud && /\.pdf$/i.test(row.path) && row.rawUrl;
				if (!page && !pdf) continue;
				const key = artifactKey(row);
				if (!owners.has(key) && owners.size < MAX_LIVE_PREVIEWS) owners.set(key, section.id);
			}
		}
		return owners;
	}, [cards, pinnedCards, showProjectSections, visibleGroups]);
	const createArtifact = useCreateArtifactSession();
	const clearFilters = () => {
		setQuery("");
		setProjectId("");
	};

	return (
		<div className="flex min-h-0 flex-1 flex-col bg-background">
			{/* The controls sit the same distance from the top, the right edge, and
			    each other: half the gap between the toolbar and a control. */}
			<header className="workspace-topbar-container center-panel-titlebar flex h-toolbar shrink-0 items-center justify-between gap-3 border-b border-border-strong pr-[calc((var(--size-toolbar)-var(--size-topbar-control))/2)]">
				<h1 className={showPageTitle ? cn(topbarProjectLabelClass, "inline-flex items-center gap-1.5") : "sr-only"}>
					{showPageTitle ? <File className="size-icon-md" aria-hidden="true" /> : null}
					{t("artifacts.title")}
				</h1>
				<div className="workspace-topbar-actions flex min-w-0 flex-1 items-center justify-end gap-[calc((var(--size-toolbar)-var(--size-topbar-control))/2)]">
					{hasArtifacts ? (
						<>
						{/* Same field as the inspector's Files filter and browser address bar. */}
						<label className="relative block w-56 min-w-0 shrink">
							<Search aria-hidden="true" className="pointer-events-none absolute start-2.5 top-1/2 size-icon-sm -translate-y-1/2 text-passive" />
							<Input
								aria-label={t("artifacts.search")}
								className="inspector-field-input ps-8 [&::-webkit-search-cancel-button]:appearance-none"
								onChange={(event) => setQuery(event.target.value)}
								placeholder={t("artifacts.search")}
								type="search"
								value={query}
							/>
						</label>
						<DropdownMenu>
							<DropdownMenuTrigger asChild>
								<TopbarButton variant="secondary" aria-label={t("artifacts.projectFilter")} className="max-w-44 transition-none!">
									<Folder aria-hidden="true" className="size-icon-sm shrink-0" />
									<span className="truncate">{projectId ? (projectId === STANDALONE_WORKSPACE_ID ? scratchpadName : groups.find((group) => group.id === projectId)?.name) : t("artifacts.allProjects")}</span>
									<ChevronDown aria-hidden="true" className="size-icon-xs shrink-0" />
								</TopbarButton>
							</DropdownMenuTrigger>
							<DropdownMenuContent align="end" className="max-h-80 overflow-y-auto" data-browser-native-overlay="true">
								<DropdownMenuRadioGroup value={projectId} onValueChange={setProjectId}>
									<DropdownMenuRadioItem value="">{t("artifacts.allProjects")}</DropdownMenuRadioItem>
									<DropdownMenuSeparator />
									{groups.map((group) => <DropdownMenuRadioItem key={group.id} value={group.id}>{group.isScratchpad ? scratchpadName : group.name}</DropdownMenuRadioItem>)}
								</DropdownMenuRadioGroup>
							</DropdownMenuContent>
						</DropdownMenu>
						<div role="group" aria-label={t("artifacts.view")} className="inline-flex shrink-0 items-center gap-0 rounded-md transition-none hover:bg-interactive-hover">
							{([
								{ value: "gallery", label: t("artifacts.galleryView"), icon: LayoutGrid },
								{ value: "list", label: t("artifacts.listView"), icon: List },
							] as const).map(({ value, label, icon: Icon }) => (
								<Tooltip key={value}>
									<TooltipTrigger asChild>
										<TopbarButton
											variant="icon"
											aria-label={label}
											aria-pressed={view === value}
											onClick={() => setView(value)}
											className={cn("transition-none! duration-0", view === value ? "bg-interactive-active text-foreground hover:bg-interactive-active" : "hover:bg-transparent")}
										>
											<Icon aria-hidden="true" className="size-icon-sm" />
										</TopbarButton>
									</TooltipTrigger>
									<TooltipContent side="bottom">{label}</TooltipContent>
								</Tooltip>
							))}
						</div>
						</>
					) : null}
					<CreateArtifactMenu
						workspaces={workspaces ?? []}
						create={createArtifact.create}
						pending={createArtifact.pending}
						label="new"
						align="end"
						localDisabledReason={createArtifact.agentReady ? undefined : t("artifacts.noAgent.description")}
					/>
				</div>
			</header>
			<ScrollArea.Root asChild type="hover" scrollHideDelay={500}>
				<main className="relative min-h-0 flex-1 overflow-hidden">
					<ScrollArea.Viewport className="h-full w-full overscroll-contain focus-visible:outline-none">
						<div className={cn("pr-4 pb-4 pl-(--space-4_5)", visibleGroups.length === 1 && "pt-4")}>
							{workspaceQuery.isLoading ? (
								<p role="status" className="px-3 py-8 text-center text-sm text-muted-foreground">{t("artifacts.loading")}</p>
							) : workspaceQuery.error ? (
								<p role="alert" className="px-3 py-8 text-center text-sm text-destructive">{workspaceQuery.error.message}</p>
							) : !hasArtifacts ? (
								<EmptyArtifacts workspaces={workspaces ?? []} />
							) : visibleGroups.length === 0 ? (
								<div className="flex flex-col items-center gap-1 py-16 text-center">
									<p className="text-control font-medium text-foreground">{t("artifacts.noMatches")}</p>
									<Button variant="ghost" size="sm" className="text-muted-foreground" onClick={clearFilters}>
										{t("artifacts.clearFilters")}
									</Button>
								</div>
							) : (
								<div>
									{pinnedCards.length > 0 ? <ArtifactSection sectionId="pinned" name={t("artifacts.pinned")} icon={Pin} view={view}>
										{pinnedCards.map(({ row, projectName }) => (
											<ArtifactCard key={artifactKey(row)} row={row} projectName={projectName} view={view} showProjectName previewLive={livePreviewSections.get(artifactKey(row)) === "pinned"} onPreview={openPreview} />
										))}
									</ArtifactSection> : null}
									{showProjectSections ? <ArtifactSection sectionId="recent" name={t("artifacts.recent")} icon={Clock} view={view}>
										{cards.slice(0, 3).map(({ row, projectName }) => (
											<ArtifactCard key={`${row.sessionId}:${row.path}`} row={row} projectName={projectName} view={view} showProjectName previewLive={livePreviewSections.get(artifactKey(row)) === "recent"} onPreview={openPreview} />
										))}
									</ArtifactSection> : null}
									{visibleGroups.map((group) => {
										const projectName = group.isScratchpad ? t("standalone.workspaceName") : group.name;
										return (
											<ArtifactSection
												key={group.id}
												sectionId={`project:${group.id}`}
												name={projectName}
												icon={group.isScratchpad ? File : group.cloud ? Cloud : Folder}
												view={view}
												collapsible={showProjectSections}
												updatedAt={groups.find((project) => project.id === group.id)?.rows[0]?.updatedAt}
											>
												{group.rows.map((row) => (
													<ArtifactCard key={`${row.sessionId}:${row.path}`} row={row} projectName={projectName} view={view} showProjectName={!showProjectSections} previewLive={livePreviewSections.get(artifactKey(row)) === `project:${group.id}`} onPreview={openPreview} />
												))}
											</ArtifactSection>
										);
									})}
								</div>
							)}
						</div>
					</ScrollArea.Viewport>
					<ScrollArea.Scrollbar orientation="vertical" className="z-10 flex w-2 touch-none select-none p-0.5">
						<ScrollArea.Thumb className="relative flex-1 rounded-full bg-[color-mix(in_srgb,var(--color-scrollbar)_42%,transparent)] hover:bg-[color-mix(in_srgb,var(--color-scrollbar)_56%,transparent)]" />
					</ScrollArea.Scrollbar>
				</main>
			</ScrollArea.Root>
			<ArtifactPreviewModal artifact={preview?.row ?? null} projectName={preview?.projectName ?? ""} onClose={() => setPreview(null)} />
		</div>
	);
}

function ArtifactSection({ sectionId, name, icon: Icon, updatedAt, children, view, collapsible = true }: {
	sectionId: string;
	name: string;
	icon: typeof Folder;
	updatedAt?: string;
	children: ReactNode;
	view: ArtifactViewMode;
	collapsible?: boolean;
}) {
	const { t } = useTranslation();
	const expanded = useArtifactsViewStore((state) => !collapsible || !state.collapsedSections.includes(sectionId));
	const toggleSection = useArtifactsViewStore((state) => state.toggleSection);
	const gridId = useId();
	const headerContent = <>
		<span className={cn(topbarProjectLabelClass, "inline-flex min-w-0 items-center gap-1.5")}>
			<Icon aria-hidden="true" className="size-icon-md shrink-0" />
			<span className="truncate">{name}</span>
		</span>
		{updatedAt ? (
			<time dateTime={updatedAt} title={new Date(updatedAt).toLocaleString()} className="shrink-0 text-caption font-normal tabular-nums text-muted-foreground">
				{t("shell.updatedAt", { time: formatTimeCompact(updatedAt) })}
			</time>
		) : null}
	</>;
	return (
		<section aria-label={name} className={expanded ? "mb-3" : undefined}>
			<header>
				<h2 aria-label={name}>
					{collapsible ? <button
						type="button"
						aria-label={name}
						aria-expanded={expanded}
						aria-controls={gridId}
						onClick={() => toggleSection(sectionId)}
						className="flex min-h-11 w-full min-w-0 cursor-pointer items-center gap-3 rounded-md text-left transition-none focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring/60"
					>
						{headerContent}
						<ChevronDown aria-hidden="true" className={cn("size-icon-sm shrink-0 text-muted-foreground", !expanded && "-rotate-90")} />
					</button> : <div className="flex min-h-11 min-w-0 items-center gap-3">{headerContent}</div>}
				</h2>
			</header>
			<ul id={gridId} aria-label={name} hidden={!expanded} className={expanded ? (view === "gallery" ? ARTIFACT_GRID_CLASS : "grid gap-2") : "hidden"}>
				{children}
			</ul>
		</section>
	);
}

function ArtifactCard({ row, projectName, view, showProjectName = false, previewLive = true, onPreview }: { row: ArtifactRow; projectName: string; view: ArtifactViewMode; showProjectName?: boolean; previewLive?: boolean; onPreview: (row: ArtifactRow, projectName: string) => void }) {
	const { t } = useTranslation();
	const id = artifactKey(row);
	const pinned = useArtifactsViewStore((state) => state.pinnedArtifactIds.includes(id));
	const togglePinnedArtifact = useArtifactsViewStore((state) => state.togglePinnedArtifact);
	const requestWorkspaceFileOpen = useUiStore((state) => state.requestWorkspaceFileOpen);
	const navigateToSession = useNavigateToSession();
	const KindIcon = row.kind === "html" ? BrowserIcon : row.kind === "markdown" ? FileText : File;

	const open = () => {
		onPreview(row, projectName);
	};
	const openInSession = () => {
		requestWorkspaceFileOpen(row.sessionId, row.path, undefined, "artifact");
		navigateToSession(row.projectId, row.sessionId);
	};

	if (view === "list") {
		return (
			<li>
				<div className="group/artifact relative flex h-11 items-center gap-2.5 rounded-md bg-card px-3 transition-none hover:bg-accent focus-within:bg-accent">
					<button
						type="button"
						className="flex min-w-0 flex-1 items-center gap-2.5 self-stretch text-left outline-none after:absolute after:inset-0 after:z-[1] after:rounded-md focus-visible:after:ring-2 focus-visible:after:ring-ring/60"
						onClick={open}
						title={`${row.sessionTitle} · ${row.path}`}
					>
						{row.generating ? (
							<Loader2 aria-label={t("artifacts.generating")} className="relative z-[1] size-icon-sm shrink-0 animate-spin text-status-working motion-reduce:animate-none" />
						) : <KindIcon aria-hidden="true" className="relative z-[1] size-icon-sm shrink-0 text-muted-foreground" />}
						<span className="relative z-[1] min-w-0 shrink truncate text-control font-medium text-foreground">{row.name}</span>
						<span className="relative z-[1] min-w-0 flex-1 truncate text-xs text-muted-foreground">
							{showProjectName ? `${projectName} · ${row.sessionTitle}` : row.sessionTitle}
						</span>
					</button>
					<div className="relative z-[2] ms-auto flex shrink-0 items-center gap-1">
						<ArtifactActionsMenu
							artifact={row}
							session={{ id: row.sessionId, cloud: row.cloud }}
							iconOnly
							onOpenInSession={openInSession}
							pinned={pinned}
							onTogglePin={() => togglePinnedArtifact(id)}
							className="bg-[var(--solid-bg-secondary,var(--color-bg-secondary))] hover:bg-[var(--solid-accent,var(--accent))] dark:hover:bg-[var(--solid-accent,var(--accent))] transition-none duration-0 [@media(hover:hover)]:opacity-0 group-hover/artifact:opacity-100 group-focus-within/artifact:opacity-100 data-[state=open]:opacity-100"
						/>
					</div>
					<time dateTime={row.updatedAt} className="pointer-events-none relative z-[1] w-20 shrink-0 text-right text-xs tabular-nums text-passive">
						{formatTimeCompact(row.updatedAt)}
					</time>
				</div>
			</li>
		);
	}

	return (
		<ArtifactGalleryCard
			row={row}
			previewLive={previewLive}
			context={showProjectName ? `${projectName} · ${row.sessionTitle}` : row.sessionTitle}
			onPreview={open}
			actions={
				<ArtifactActionsMenu
					artifact={row}
					session={{ id: row.sessionId, cloud: row.cloud }}
					iconOnly
					onOpenInSession={openInSession}
					pinned={pinned}
					onTogglePin={() => togglePinnedArtifact(id)}
					className="bg-[var(--solid-bg-secondary,var(--color-bg-secondary))] hover:bg-[var(--solid-accent,var(--accent))] dark:hover:bg-[var(--solid-accent,var(--accent))] transition-none duration-0 [@media(hover:hover)]:opacity-0 group-hover:opacity-100 group-focus-within:opacity-100 data-[state=open]:opacity-100"
				/>
			}
		/>
	);
}

function EmptyArtifacts({ workspaces }: { workspaces: readonly WorkspaceSummary[] }) {
	const { t } = useTranslation();
	const openGlobalSettings = useUiStore((state) => state.openGlobalSettings);
	// Developer mode moves account management to the Harness page.
	const agentsSection = useUiStore((state) => state.developerMode) ? "harness" : "agents";
	const { agentReady, create, pending } = useCreateArtifactSession();
	const hasCloudProject = workspaces.some((workspace) => workspace.kind === CLOUD_PROJECT_KIND && Boolean(workspace.cloudOrgId));

	// Without a local agent a cloud project, which runs its own, can still start one.
	if (!agentReady && !hasCloudProject) {
		return (
			<div className="flex flex-col items-center gap-1 py-24 text-center">
				<h2 className="text-heading-sm font-semibold text-foreground">{t("artifacts.noAgent.title")}</h2>
				<p className="max-w-72 text-xs text-muted-foreground">{t("artifacts.noAgent.description")}</p>
				<Button variant="ghost" size="sm" className="mt-2" onClick={() => openGlobalSettings(agentsSection)}>
					{t("artifacts.noAgent.action")}
				</Button>
			</div>
		);
	}
	return (
		<div className="flex flex-col items-center gap-1 py-24 text-center">
			<h2 className="text-heading-sm font-semibold text-foreground">{t("artifacts.empty.title")}</h2>
			<p className="max-w-72 text-xs text-muted-foreground">{t("artifacts.empty.description")}</p>
			<CreateArtifactMenu
				workspaces={workspaces}
				create={create}
				pending={pending}
				className="mt-3"
				align="center"
				localDisabledReason={agentReady ? undefined : t("artifacts.noAgent.description")}
			/>
		</div>
	);
}

/**
 * The Create artifact trigger and its Scratchpad / project menu. The empty
 * state and the list header share it so both start the same session.
 */
function CreateArtifactMenu({
	workspaces,
	create,
	pending,
	label = "create",
	align,
	className,
	localDisabledReason,
}: {
	workspaces: readonly WorkspaceSummary[];
	create: (projectId?: string, cloudOrgId?: string) => Promise<void>;
	pending: boolean;
	label?: "create" | "new";
	align: "center" | "end";
	className?: string;
	/** Why Scratchpad and local projects cannot start a session; cloud projects run their own agent. */
	localDisabledReason?: string;
}) {
	const { t } = useTranslation();
	// Cloud workspaces are listed only when the control plane keeps artifacts.
	const projects = workspaces.filter(
		(workspace) =>
			workspace.id !== STANDALONE_WORKSPACE_ID &&
			!workspace.hostId &&
			(workspace.kind !== CLOUD_PROJECT_KIND || Boolean(workspace.cloudOrgId)),
	);
	const localBlocked = Boolean(localDisabledReason);
	const hasCloudProject = projects.some((project) => project.kind === CLOUD_PROJECT_KIND);
	const disabledReason = localBlocked && !hasCloudProject ? localDisabledReason : undefined;
	const disabled = pending || Boolean(disabledReason);
	const trigger =
		label === "new" ? (
			// Anchors the menu, so it must not scale on press.
			<TopbarButton
				variant="primary"
				className={cn("topbar-control--labeled topbar-control--static-press", className)}
				disabled={disabled}
				aria-busy={pending}
			>
				{pending ? (
					<Loader2 aria-hidden="true" className="size-icon-md animate-spin motion-reduce:animate-none" />
				) : (
					<Plus aria-hidden="true" className="size-icon-md" />
				)}
				{pending ? t("artifacts.creating") : t("artifacts.new")}
			</TopbarButton>
		) : (
			<Button size="sm" className={className} disabled={disabled} aria-busy={pending}>
				{pending ? <Loader2 aria-hidden="true" className="size-icon-sm animate-spin text-status-working motion-reduce:animate-none" /> : null}
				{pending ? t("artifacts.creating") : t("artifacts.create")}
				{pending ? null : <ChevronDown aria-hidden="true" className="size-icon-sm" />}
			</Button>
		);
	if (disabledReason) {
		return (
			<Tooltip>
				<TooltipTrigger asChild>
					<span tabIndex={0} className="inline-flex rounded-md focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring/60">
						{trigger}
					</span>
				</TooltipTrigger>
				<TooltipContent>{disabledReason}</TooltipContent>
			</Tooltip>
		);
	}
	return (
		<DropdownMenu>
			<DropdownMenuTrigger asChild>{trigger}</DropdownMenuTrigger>
			<DropdownMenuContent align={align} className="min-w-56">
				{/* Says why Scratchpad and local projects are disabled while cloud ones are not. */}
				{localBlocked ? <DropdownMenuLabel className="max-w-64 text-xs font-normal text-muted-foreground">{localDisabledReason}</DropdownMenuLabel> : null}
				<DropdownMenuItem disabled={localBlocked} onSelect={() => void create()}>
					<File aria-hidden="true" className="size-icon-sm" />
					{t("standalone.workspaceName")}
				</DropdownMenuItem>
				{projects.length > 0 ? <DropdownMenuSeparator /> : null}
				{projects.map((project) => (
					<DropdownMenuItem
						key={project.id}
						disabled={localBlocked && project.kind !== CLOUD_PROJECT_KIND}
						onSelect={() => void create(project.id, project.kind === CLOUD_PROJECT_KIND ? project.cloudOrgId : undefined)}
					>
						{project.kind === CLOUD_PROJECT_KIND ? (
							<Cloud aria-hidden="true" className="size-icon-sm" />
						) : (
							<Folder aria-hidden="true" className="size-icon-sm" />
						)}
						<span className="truncate">{project.name}</span>
					</DropdownMenuItem>
				))}
			</DropdownMenuContent>
		</DropdownMenu>
	);
}
