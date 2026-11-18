import { artifactFileIcon } from "./artifact-file-icon";
import { ChevronDown, File, Filter, List, Loader2, Package, Pin, Plus, SelectMultiple, Trash2 } from "@/components/app-icons";
import { useProjectAvatars } from "../hooks/useProjectAvatars";
import { ProjectIcon } from "./ProjectIcon";
import { ScrollArea } from "radix-ui";
import { useMutation, useQueryClient } from "@tanstack/react-query";
import { memo, useCallback, useEffect, useId, useMemo, useState, type ReactNode } from "react";
import { useCloudCp } from "../hooks/useCloudCp";
import { useCloudMeUserId } from "../hooks/useCloudMeUserId";
import { useTranslation } from "react-i18next";
import { useCloudOffering } from "../hooks/useCloudOffering";
import { useCreateArtifactSession } from "../hooks/useCreateArtifactSession";
import { deleteSessionArtifactFile } from "../hooks/useSessionWorkspaceFiles";
import { cloudSessionsQueryKey, useRemoteWorkspaces, useWorkspaceQuery, workspaceQueryKeyForHost } from "../hooks/useWorkspaceQuery";
import { keptArtifactsQueryKey, useKeptArtifacts } from "../hooks/useKeptArtifacts";
import { useStartSessionFromArtifact } from "../hooks/useStartSessionFromArtifact";
import { useSharedArtifacts } from "../hooks/useSharedArtifacts";
import {
	ARTIFACT_OWNERS,
	ARTIFACT_SHARINGS,
	SHARED_WITH_ME_GROUP_ID,
	ARTIFACT_TYPES,
	artifactChangeError,
	artifactContext,
	artifactCount,
	artifactKey,
	artifactLocation,
	artifactLocationCounts,
	artifactOwner,
	canChangeSessionArtifacts,
	filterArtifactGroups,
	groupArtifactsByProject,
	hostArtifactLocation,
	sharedArtifactGroup,
	sharedArtifactRow,
	withHostShareLinks,
	withKeptArtifacts,
	withShareAccess,
	type ArtifactGroup,
	type ArtifactLocation,
	type ArtifactOwner,
	type ArtifactRow,
	type ArtifactSharing,
	type ArtifactType,
} from "../lib/artifacts";
import { formatTimeCompact } from "../lib/format-time";
import { LOCAL_HOST } from "../lib/hosts";
import { useShellMaybe } from "../lib/shell-context";
import { useNavigateToSession } from "../lib/navigate-to-session";
import { cn } from "../lib/utils";
import { useArtifactsViewStore, type ArtifactSort, type ArtifactViewMode } from "../stores/artifacts-view-store";
import { useUiStore } from "../stores/ui-store";
import { CLOUD_PROJECT_KIND, STANDALONE_WORKSPACE_ID, type WorkspaceSummary } from "../types/workspace";
import { DeferredArtifactActionsMenu } from "./ArtifactActionsMenu";
import { useArtifactShareFile, useHostShareLinks, useReconciledShareLinks, useShareAccess } from "./ShareArtifactPopover";
import { combinePublishedChecks, deleteTargets, prepareUnpublish, publishedCheck, warnLinksStillLive } from "../lib/unpublish-artifacts";
import { usePublishedLinkCount } from "../hooks/usePublishedLinkCount";
import { ArtifactGalleryCard, type ArtifactCardSelection } from "./ArtifactGalleryCard";
import { ArtifactShareMark } from "./ArtifactShareMark";
import { ConfirmDialog } from "./ConfirmDialog";
import { Checkbox } from "./ui/checkbox";
import { ArtifactPreviewModal } from "./ArtifactPreviewModal";
import { PageTopbar, PageTopbarTitle } from "./PageTopbar";
import { FilterChip, ToolbarSearch } from "./PageToolbar";
import { Button } from "./ui/button";
import {
	DropdownMenu,
	DropdownMenuContent,
	DropdownMenuItem,
	DropdownMenuLabel,
	DropdownMenuCheckboxItem,
	DropdownMenuRadioGroup,
	DropdownMenuRadioItem,
	DropdownMenuSeparator,
	DropdownMenuSub,
	DropdownMenuSubContent,
	DropdownMenuSubTrigger,
	DropdownMenuTrigger,
} from "./ui/dropdown-menu";
import { Pressable } from "./ui/pressable";
import { Tooltip, TooltipContent, TooltipTrigger } from "./ui/tooltip";
import { useEdgeFadeRef } from "../hooks/useEdgeFade";
import { GridSpacer, useVirtualGridRows } from "../hooks/useVirtualGridRows";

// Three previews a row; narrow panels drop to two, then one.
const ARTIFACT_GRID_CLASS = "grid grid-cols-1 gap-4 @lg:grid-cols-2 @2xl:grid-cols-3";
const MAX_LIVE_PREVIEWS = 4;
/** How many selected names the bulk delete confirmation lists before "+N more". */
const MAX_CONFIRM_NAMES = 5;

/**
 * A project's icon as the sidebar draws it: its repository owner's avatar or
 * colour-coded letter tile, or a folder with avatars off.
 */
function ArtifactProjectIcon({ workspace, avatarUrl }: { workspace: Pick<WorkspaceSummary, "id" | "name" | "path">; avatarUrl?: string }) {
	return (
		<span aria-hidden="true" className="inline-flex size-icon-md shrink-0 items-center justify-center">
			<ProjectIcon avatarUrl={avatarUrl} expanded={false} name={workspace.name} seed={workspace.path || workspace.id} />
		</span>
	);
}

/**
 * Every artifact agents have written, grouped by project: on this machine, and
 * in cloud sessions when the control plane keeps artifacts.
 */
export function ArtifactsView() {
	const edgeFadeRef = useEdgeFadeRef<HTMLDivElement>();
	const [scrollElement, setScrollElement] = useState<HTMLDivElement | null>(null);
	const fadeRef = useCallback((node: HTMLDivElement | null) => {
		edgeFadeRef(node);
		setScrollElement(node);
	}, [edgeFadeRef]);
	const { t } = useTranslation();
	const cloudArtifacts = useCloudOffering().artifacts;
	const workspaceQuery = useWorkspaceQuery({ includeCloud: cloudArtifacts });
	const workspaces = workspaceQuery.data;
	// Every saved remote host, so an offline host's last list still shows.
	const remoteHosts = useShellMaybe()?.remoteHosts;
	const remoteHostIds = useMemo(() => remoteHosts?.map((host) => host.hostId), [remoteHosts]);
	const remote = useRemoteWorkspaces({ cachedHostIds: remoteHostIds });
	const remoteWorkspaces = remote.data;
	const hostLabels = useMemo(() => new Map((remoteHosts ?? []).map((host) => [host.hostId, host.label])), [remoteHosts]);
	const orchestratorTitle = t("shell.orchestrator");
	// Published marks and the Sharing filter read each file's shareUrl; bring
	// the publish records behind it up to date first.
	const shareAccess = useReconciledShareLinks();
	const hostShareLinks = useHostShareLinks();
	// Artifacts kept after their session was deleted or their project removed.
	const kept = useKeptArtifacts().data;
	const noProjectName = t("artifacts.noProject");
	// Links other people published that open for this person, listed under Cloud.
	const sharedWithMe = useSharedArtifacts().data;
	const sharedGroupName = t("artifacts.owner.shared");
	const groups = useMemo(() => {
		const own = withShareAccess(withHostShareLinks(withKeptArtifacts(groupArtifactsByProject([...(workspaces ?? []), ...remoteWorkspaces], orchestratorTitle), kept, workspaces ?? [], noProjectName), hostShareLinks), shareAccess);
		const shared = sharedArtifactGroup(sharedWithMe, sharedGroupName);
		return shared ? [...own, shared] : own;
	}, [workspaces, remoteWorkspaces, orchestratorTitle, kept, noProjectName, hostShareLinks, shareAccess, sharedWithMe, sharedGroupName]);
	const [query, setQuery] = useState("");
	// Each filter keeps several values at once; an empty list shows every artifact.
	const [projectIds, setProjectIds] = useState<readonly string[]>([]);
	const [location, setLocation] = useState<ArtifactLocation | undefined>();
	const [types, setTypes] = useState<readonly ArtifactType[]>([]);
	const [sharings, setSharings] = useState<readonly ArtifactSharing[]>([]);
	const [owner, setOwner] = useState<ArtifactOwner | undefined>();
	const [pinnedOnly, setPinnedOnly] = useState(false);
	const [preview, setPreview] = useState<{ row: ArtifactRow; projectName: string } | null>(null);
	const openPreview = useCallback((row: ArtifactRow, projectName: string) => setPreview({ row, projectName }), []);
	// A shared link opened from outside the app (ao-app://shared-artifact) opens here once.
	const openSharedArtifact = useArtifactsViewStore((state) => state.openSharedArtifact);
	useEffect(() => {
		if (!openSharedArtifact) return;
		setPreview({ row: sharedArtifactRow(openSharedArtifact), projectName: t("artifacts.owner.shared") });
		useArtifactsViewStore.getState().requestOpenSharedArtifact(null);
	}, [openSharedArtifact, t]);
	// The open preview follows its row as the list refreshes, so a file an
	// agent rewrites shows its new content.
	const previewRow = useMemo(() => {
		if (!preview) return null;
		const key = artifactKey(preview.row);
		return groups.flatMap((group) => group.rows).find((row) => artifactKey(row) === key) ?? preview.row;
	}, [groups, preview]);
	const view = useArtifactsViewStore((state) => state.view);
	const setView = useArtifactsViewStore((state) => state.setView);
	const pinnedArtifactIds = useArtifactsViewStore((state) => state.pinnedArtifactIds);
	const sort = useArtifactsViewStore((state) => state.sort);
	const setSort = useArtifactsViewStore((state) => state.setSort);
	const scratchpadName = t("standalone.workspaceName");
	const { client: cloudClient } = useCloudCp(cloudArtifacts);
	const cloudUserId = useCloudMeUserId(cloudArtifacts);
	const pinnedKeys = useMemo(() => pinnedOnly ? new Set(pinnedArtifactIds) : undefined, [pinnedOnly, pinnedArtifactIds]);
	const filters = useMemo(
		() => ({ query, projectIds, location, types, sharings, owner, cloudUserId, onlyKeys: pinnedKeys }),
		[query, projectIds, location, types, sharings, owner, cloudUserId, pinnedKeys],
	);
	const visibleGroups = useMemo(() => filterArtifactGroups(groups, filters, scratchpadName), [groups, filters, scratchpadName]);
	// Each location chip counts what choosing it would show under the other filters.
	const locationCounts = useMemo(() => artifactLocationCounts(groups, filters, scratchpadName), [groups, filters, scratchpadName]);
	const hasArtifacts = artifactCount(groups) > 0;
	// One list, newest change (or name) first, with pinned artifacts kept at the
	// top. The filters narrow it; nothing splits it into sections.
	const cards = useMemo(() => {
		const pinned = new Set(pinnedArtifactIds);
		return visibleGroups
			.flatMap((group) => group.rows.map((row) => {
				const place = artifactLocation(row, group);
				return {
					row,
					projectName: withHostLabel(group.isScratchpad ? t("standalone.workspaceName") : group.name, group.hostId && hostLabels.get(group.hostId)),
					placeName: place === "cloud" ? t("artifacts.locationCloud")
						: place === "local" ? t("artifacts.locationLocal")
						: (row.hostId && hostLabels.get(row.hostId)) || t("artifacts.locationRemote"),
					owner: artifactOwner(row, cloudUserId),
					pinned: pinned.has(artifactKey(row)),
				};
			}))
			.sort((left, right) => Number(right.pinned) - Number(left.pinned)
				|| (sort === "name"
					? left.row.name.localeCompare(right.row.name)
					: (Date.parse(right.row.updatedAt) || 0) - (Date.parse(left.row.updatedAt) || 0)));
	}, [visibleGroups, pinnedArtifactIds, hostLabels, cloudUserId, sort, t]);
	// Only the cards on screen and near it are built; a long list would otherwise build every card at once.
	const grid = useVirtualGridRows(cards.length, scrollElement);
	// Only the first few pages and PDFs render live; the rest show their thumbnail.
	const livePreviews = useMemo(() => {
		const live = new Set<string>();
		for (const { row } of cards) {
			if (live.size >= MAX_LIVE_PREVIEWS) break;
			const remote = row.hostId && row.hostId !== LOCAL_HOST;
			const page = row.kind === "html" && (row.cloud || (row.rawUrl && (row.inlineUrl || remote)));
			const pdf = /\.pdf$/i.test(row.path) && (row.cloud || row.rawUrl);
			if (page || pdf) live.add(artifactKey(row));
		}
		return live;
	}, [cards]);
	const createArtifact = useCreateArtifactSession();
	const clearFilters = () => {
		setQuery("");
		setProjectIds([]);
		setLocation(undefined);
		setTypes([]);
		setSharings([]);
		setOwner(undefined);
		setPinnedOnly(false);
	};
	const locationOptions: { value: ArtifactLocation | undefined; label: string }[] = [
		{ value: undefined, label: t("artifacts.locationAll") },
		{ value: "local", label: t("artifacts.locationLocal") },
		// One per saved remote host, under the name the host is listed by.
		...(remoteHosts ?? []).map((host) => ({ value: hostArtifactLocation(host.hostId), label: host.label })),
		// Cloud artifacts show only where the control plane keeps them, or where links are shared with this person.
		...(cloudArtifacts || groups.some((group) => group.id === SHARED_WITH_ME_GROUP_ID) ? [{ value: "cloud" as const, label: t("artifacts.locationCloud") }] : []),
	];
	// Machines whose list did not load, among those shown: said so, with a retry, instead of
	// leaving their artifacts silently missing.
	const failedHosts = (remoteHosts ?? []).filter((host) => remote.failedHostIds.includes(host.hostId)
		&& (location === undefined || location === hostArtifactLocation(host.hostId)));
	const projectName = (group: ArtifactGroup) => withHostLabel(group.isScratchpad ? scratchpadName : group.name, group.hostId && hostLabels.get(group.hostId));
	const selectedProjects = groups.filter((group) => projectIds.includes(group.id));
	const filtering = projectIds.length > 0 || types.length > 0 || sharings.length > 0 || owner !== undefined || pinnedOnly;
	// A submenu row's current value: "Any" for none, the values when few, else their count.
	const chosen = (values: readonly string[], none: string) => values.length === 0
		? none
		: values.length <= 2 ? values.join(", ") : t("artifacts.selectedCount", { count: values.length });
	const ownerLabel = (value: ArtifactOwner) => t(`artifacts.ownerFilter.${value}`);
	const queryClient = useQueryClient();
	// Another member's cloud session files are view only, so they cannot be selected.
	// A bundle row stands for one of its files, so deleting it would leave the
	// rest of the bundle behind; it has no Delete in its menu either.
	const deleteBlockedReason = (row: ArtifactRow) => row.bundle
		? t("artifacts.bundleNotDeletable")
		: row.shared ? t("artifacts.viewOnly") : canChangeSessionArtifacts(row.cloud, cloudUserId) ? undefined : t("artifacts.viewOnly");
	const [selecting, setSelecting] = useState(false);
	const [selectedIds, setSelectedIds] = useState<ReadonlySet<string>>(() => new Set());
	const [confirmingBulkDelete, setConfirmingBulkDelete] = useState(false);
	// A selected file deleted elsewhere leaves the selection as the list
	// refreshes, so a later file at the same path starts unselected. The
	// check runs against every artifact, not the filtered cards: the
	// selection outlives the filters, so the confirmation names every file.
	useEffect(() => {
		const present = new Set(groups.flatMap((group) => group.rows.map(artifactKey)));
		setSelectedIds((current) => {
			const kept = [...current].filter((id) => present.has(id));
			return kept.length === current.size ? current : new Set(kept);
		});
	}, [groups]);
	const selectedRows = useMemo(
		() => groups.flatMap((group) => group.rows).filter((row) => selectedIds.has(artifactKey(row))),
		[groups, selectedIds],
	);
	const selectableVisibleIds = cards.filter(({ row }) => !deleteBlockedReason(row)).map(({ row }) => artifactKey(row));
	const allVisibleSelected = selectableVisibleIds.length > 0 && selectableVisibleIds.every((id) => selectedIds.has(id));
	const toggleSelected = (id: string) => setSelectedIds((current) => {
		const next = new Set(current);
		if (!next.delete(id)) next.add(id);
		return next;
	});
	const toggleAllVisible = () => setSelectedIds((current) => {
		const next = new Set(current);
		for (const id of selectableVisibleIds) {
			if (allVisibleSelected) next.delete(id);
			else next.add(id);
		}
		return next;
	});
	const stopSelecting = () => {
		setSelecting(false);
		setSelectedIds(new Set());
	};
	// Each file is deleted on its own; the ones that fail stay selected so
	// the person can retry them.
	// A published file's link is looked up before its delete and unpublished
	// once that file is gone; a file that failed to delete keeps its link.
	const { baseUrl: shareBaseUrl } = useShareAccess();
	const selectedPublished = usePublishedLinkCount(
		shareBaseUrl,
		combinePublishedChecks(selectedRows.map((row) => publishedCheck(rowSession(row), [row]))),
		confirmingBulkDelete,
	);
	const bulkDelete = useMutation({
		mutationFn: async (rows: readonly ArtifactRow[]) => {
			const links = await Promise.all(rows.map(async (row) => prepareUnpublish(shareBaseUrl, await deleteTargets(shareBaseUrl, rowSession(row), [row]), {
				deleting: [{ hostId: row.hostId, sessionId: row.sessionId, path: row.path }],
			})));
			const results = await Promise.allSettled(rows.map((row) => row.cloud
				? cloudClient.deleteSessionArtifact(row.cloud.orgId, row.sessionId, row.path).catch((error: unknown) => { throw artifactChangeError(error, t("artifacts.viewOnly")); })
				: deleteSessionArtifactFile({ path: row.path, sessionId: row.sessionId, hostId: row.hostId })));
			const linksLive = await Promise.all(links.map((link, index) => results[index]?.status === "fulfilled" ? link.unpublish() : 0));
			warnLinksStillLive(t, linksLive.reduce((sum, count) => sum + count, 0));
			const deleted = rows.filter((_, index) => results[index]?.status === "fulfilled").map(artifactKey);
			const failures = results.flatMap((result) => result.status === "rejected" ? [result.reason] : []);
			const { unpinArtifact } = useArtifactsViewStore.getState();
			for (const id of deleted) unpinArtifact(id);
			setSelectedIds((current) => new Set([...current].filter((id) => !deleted.includes(id))));
			const hostIds = new Set(rows.flatMap((row) => row.hostId && !row.cloud ? [row.hostId] : []));
			await Promise.all([
				queryClient.invalidateQueries({ queryKey: workspaceQueryKeyForHost() }),
				...(rows.some((row) => row.kept) ? [queryClient.invalidateQueries({ queryKey: keptArtifactsQueryKey })] : []),
				...[...hostIds].map((hostId) => queryClient.invalidateQueries({ queryKey: workspaceQueryKeyForHost(hostId) })),
				...(rows.some((row) => row.cloud) ? [queryClient.invalidateQueries({ queryKey: cloudSessionsQueryKey })] : []),
			]);
			if (failures.length > 0) {
				const reason = failures[0];
				throw new Error(t("artifacts.bulkDeleteFailed", {
					failed: failures.length,
					total: rows.length,
					message: reason instanceof Error ? reason.message : String(reason),
				}));
			}
		},
		onSuccess: () => {
			setConfirmingBulkDelete(false);
			stopSelecting();
		},
	});
	const confirmBulkDelete = () => {
		bulkDelete.reset();
		setConfirmingBulkDelete(true);
	};
	useEffect(() => {
		if (!selecting || confirmingBulkDelete) return;
		const onKeyDown = (event: KeyboardEvent) => {
			// Escape inside search or an open menu closes that first.
			if (event.key !== "Escape" || event.defaultPrevented) return;
			setSelecting(false);
			setSelectedIds(new Set());
		};
		window.addEventListener("keydown", onKeyDown);
		return () => window.removeEventListener("keydown", onKeyDown);
	}, [selecting, confirmingBulkDelete]);
	const selectionFor = (row: ArtifactRow, projectName: string): ArtifactCardSelection | undefined => {
		if (!selecting) return undefined;
		const id = artifactKey(row);
		return {
			selected: selectedIds.has(id),
			disabledReason: deleteBlockedReason(row),
			label: t("artifacts.selectArtifact", { name: row.name }),
			description: `${projectName} · ${row.sessionTitle} · ${row.path}`,
			onToggle: () => toggleSelected(id),
		};
	};

	return (
		<div className="flex min-h-0 flex-1 flex-col bg-background">
			<PageTopbar lead={<PageTopbarTitle as="h1" icon={<Package className="size-icon-md" aria-hidden="true" />}>{t("artifacts.title")}</PageTopbarTitle>} />
			<div className="shrink-0 pt-3 pr-4 pl-(--space-4_5)">
				<div className="flex min-h-control-md items-center gap-2">
					{hasArtifacts ? (
						<div role="group" aria-label={t("artifacts.location")} className="flex min-w-0 items-center gap-1">
							{locationOptions.map((option) => (
								<Button
									key={option.value ?? "all"}
									type="button"
									size="sm"
									variant="ghost"
									aria-pressed={location === option.value}
									onClick={() => setLocation(option.value)}
								>
									{option.label}
									<span aria-hidden="true" className="tabular-nums text-passive">{locationCounts.get(option.value ?? "all") ?? 0}</span>
								</Button>
							))}
						</div>
					) : null}
					<div className="ms-auto flex shrink-0 items-center gap-0.5">
						{hasArtifacts ? (
							<>
								<ToolbarSearch label={t("artifacts.search")} query={query} onQueryChange={setQuery} />
								<DropdownMenu>
									<Tooltip>
										<TooltipTrigger asChild>
											<DropdownMenuTrigger asChild>
												<Button type="button" size="icon-sm" variant="quiet" aria-label={t("artifacts.filter")} data-active={filtering ? "true" : undefined}>
													<Filter aria-hidden="true" className="size-icon-sm" />
												</Button>
											</DropdownMenuTrigger>
										</TooltipTrigger>
										<TooltipContent side="bottom">{t("artifacts.filter")}</TooltipContent>
									</Tooltip>
									<DropdownMenuContent align="end" className="min-w-64" data-browser-native-overlay="true">
										<DropdownMenuSub>
											<FilterMenuRow label={t("artifacts.sortBy")} value={t(`artifacts.sort.${sort}`)} />
											<DropdownMenuSubContent className="min-w-44" data-browser-native-overlay="true">
												<DropdownMenuRadioGroup value={sort} onValueChange={(value) => setSort(value as ArtifactSort)}>
													{(["updated", "name"] as const).map((value) => (
														<DropdownMenuRadioItem key={value} value={value}>{t(`artifacts.sort.${value}`)}</DropdownMenuRadioItem>
													))}
												</DropdownMenuRadioGroup>
											</DropdownMenuSubContent>
										</DropdownMenuSub>
										<DropdownMenuSeparator />
										<DropdownMenuSub>
											<FilterMenuRow label={t("artifacts.filterOwner")} value={owner ? ownerLabel(owner) : t("artifacts.ownerAny")} />
											<DropdownMenuSubContent className="min-w-44" data-browser-native-overlay="true">
												<DropdownMenuRadioGroup value={owner ?? "any"} onValueChange={(value) => setOwner(value === "any" ? undefined : value as ArtifactOwner)}>
													<DropdownMenuRadioItem value="any">{t("artifacts.ownerAny")}</DropdownMenuRadioItem>
													{ARTIFACT_OWNERS.map((value) => (
														<DropdownMenuRadioItem key={value} value={value}>{ownerLabel(value)}</DropdownMenuRadioItem>
													))}
												</DropdownMenuRadioGroup>
											</DropdownMenuSubContent>
										</DropdownMenuSub>
										<DropdownMenuSub>
											<FilterMenuRow label={t("artifacts.filterProject")} value={chosen(selectedProjects.map(projectName), t("artifacts.allProjects"))} />
											<DropdownMenuSubContent className="max-h-[min(30rem,70vh)] min-w-52 overflow-y-auto" data-browser-native-overlay="true">
												<FilterOption checked={projectIds.length === 0} onToggle={() => setProjectIds([])}>{t("artifacts.allProjects")}</FilterOption>
												<DropdownMenuSeparator />
												{groups
													.filter((group) => !location || group.rows.some((row) => artifactLocation(row, group) === location))
													.map((group) => (
														<FilterOption key={group.id} checked={projectIds.includes(group.id)} onToggle={() => setProjectIds(toggled(projectIds, group.id))}>
															<span className="truncate">{projectName(group)}</span>
														</FilterOption>
													))}
											</DropdownMenuSubContent>
										</DropdownMenuSub>
										<DropdownMenuSub>
											<FilterMenuRow label={t("artifacts.filterType")} value={chosen(types.map((value) => t(`artifacts.type.${value}`)), t("artifacts.allTypes"))} />
											<DropdownMenuSubContent className="min-w-48" data-browser-native-overlay="true">
												<FilterOption checked={types.length === 0} onToggle={() => setTypes([])}>{t("artifacts.allTypes")}</FilterOption>
												<DropdownMenuSeparator />
												{ARTIFACT_TYPES.map((value) => (
													<FilterOption key={value} checked={types.includes(value)} onToggle={() => setTypes(toggled(types, value))}>{t(`artifacts.type.${value}`)}</FilterOption>
												))}
											</DropdownMenuSubContent>
										</DropdownMenuSub>
										<DropdownMenuSub>
											<FilterMenuRow label={t("artifacts.filterSharing")} value={chosen(sharings.map((value) => t(`artifacts.sharing.${value}`)), t("artifacts.allSharing"))} />
											<DropdownMenuSubContent className="min-w-48" data-browser-native-overlay="true">
												<FilterOption checked={sharings.length === 0} onToggle={() => setSharings([])}>{t("artifacts.allSharing")}</FilterOption>
												<DropdownMenuSeparator />
												{ARTIFACT_SHARINGS.map((value) => (
													<FilterOption key={value} checked={sharings.includes(value)} onToggle={() => setSharings(toggled(sharings, value))}>{t(`artifacts.sharing.${value}`)}</FilterOption>
												))}
											</DropdownMenuSubContent>
										</DropdownMenuSub>
										<DropdownMenuSeparator />
										<FilterOption checked={pinnedOnly} onToggle={() => setPinnedOnly(!pinnedOnly)}>{t("artifacts.pinned")}</FilterOption>
										{filtering ? (
											<>
												<DropdownMenuSeparator />
												<DropdownMenuItem onSelect={clearFilters}>{t("artifacts.clearFilters")}</DropdownMenuItem>
											</>
										) : null}
									</DropdownMenuContent>
								</DropdownMenu>
								{selecting ? null : (
									<Tooltip>
										<TooltipTrigger asChild>
											<Button type="button" size="icon-sm" variant="quiet"
												aria-label={t("artifacts.listView")}
												aria-pressed={view === "list"}
												onClick={() => setView(view === "list" ? "gallery" : "list")}
											>
												<List aria-hidden="true" className="size-icon-sm" />
											</Button>
										</TooltipTrigger>
										<TooltipContent side="bottom">{view === "list" ? t("artifacts.galleryView") : t("artifacts.listView")}</TooltipContent>
									</Tooltip>
								)}
							</>
						) : null}
						{selecting && hasArtifacts ? (
							<div role="group" aria-label={t("artifacts.selection")} className="ms-1 inline-flex shrink-0 items-center gap-1">
								<span aria-live="polite" className="px-1 text-control tabular-nums text-muted-foreground">
									{t("artifacts.selectedCount", { count: selectedRows.length })}
								</span>
								<Button type="button" size="sm" variant="quiet" disabled={selectableVisibleIds.length === 0} onClick={toggleAllVisible}>
									{t(allVisibleSelected ? "artifacts.deselectAll" : "artifacts.selectAll")}
								</Button>
								<Button type="button" size="sm" variant="destructive" disabled={selectedRows.length === 0} onClick={confirmBulkDelete}>
									<Trash2 aria-hidden="true" className="size-icon-sm" />
									{t("artifacts.deleteSelected")}
								</Button>
								<Button type="button" size="sm" variant="quiet" onClick={stopSelecting}>
									{t("artifacts.cancelSelection")}
								</Button>
							</div>
						) : (
							<>
								{hasArtifacts ? (
									<Tooltip>
										<TooltipTrigger asChild>
											<Button type="button" size="icon-sm" variant="quiet" aria-label={t("artifacts.select")} onClick={() => setSelecting(true)}>
												<SelectMultiple aria-hidden="true" className="size-icon-sm" />
											</Button>
										</TooltipTrigger>
										<TooltipContent side="bottom">{t("artifacts.select")}</TooltipContent>
									</Tooltip>
								) : null}
								<CreateArtifactMenu
									workspaces={workspaces ?? []}
									create={createArtifact.create}
									pending={createArtifact.pending}
									label="new"
									align="end"
									localDisabledReason={createArtifact.agentReady ? undefined : t("artifacts.noAgent.description")}
								/>
							</>
						)}
					</div>
				</div>
				{failedHosts.map((host) => (
					<div key={host.hostId} role="alert" className="mt-2 flex items-center gap-2 text-xs text-destructive">
						<span className="min-w-0 truncate">{t("artifacts.hostLoadFailed", { host: host.label })}</span>
						<Button type="button" size="sm" variant="quiet" onClick={() => void remote.refetch()}>{t("artifacts.hostLoadRetry")}</Button>
					</div>
				))}
				{hasArtifacts && filtering ? (
					<div className="mt-2 flex flex-wrap items-center gap-1.5">
						{([
							[t("artifacts.filterOwner"), owner ? [ownerLabel(owner)] : [], () => setOwner(undefined)],
							[t("artifacts.filterProject"), selectedProjects.map(projectName), () => setProjectIds([])],
							[t("artifacts.filterType"), types.map((value) => t(`artifacts.type.${value}`)), () => setTypes([])],
							[t("artifacts.filterSharing"), sharings.map((value) => t(`artifacts.sharing.${value}`)), () => setSharings([])],
						] as const).map(([label, values, clear]) => values.length > 0 ? (
							<FilterChip
								key={label}
								label={label}
								value={values.join(", ")}
								removeLabel={t("artifacts.removeFilter", { filter: `${label}: ${values.join(", ")}` })}
								onRemove={clear}
							/>
						) : null)}
						{pinnedOnly ? (
							<FilterChip
								label={t("artifacts.pinned")}
								removeLabel={t("artifacts.removeFilter", { filter: t("artifacts.pinned") })}
								onRemove={() => setPinnedOnly(false)}
							/>
						) : null}
					</div>
				) : null}
			</div>
			<ScrollArea.Root asChild type="hover" scrollHideDelay={500}>
				<main className="relative min-h-0 flex-1 overflow-hidden">
					{/* Radix wraps the content in a display:table box that grows to its widest
					    row, so long names never truncate and push each row's time out of
					    view in a narrow panel; a block box keeps the rows to the panel's width. */}
					<ScrollArea.Viewport ref={fadeRef} className="edge-fade [--edge-fade-end-size:0px] h-full w-full overscroll-contain focus-visible:outline-none [&>div]:block!">
						<div className="@container pt-3 pr-4 pb-4 pl-(--space-4_5)">
							{workspaceQuery.isLoading ? (
								<p role="status" className="px-3 py-8 text-center text-sm text-muted-foreground">{t("artifacts.loading")}</p>
							) : workspaceQuery.error ? (
								<p role="alert" className="px-3 py-8 text-center text-sm text-destructive">{workspaceQuery.error.message}</p>
							) : !hasArtifacts ? (
								<EmptyArtifacts workspaces={workspaces ?? []} />
							) : visibleGroups.length === 0 && location !== undefined && failedHosts.length > 0 ? null : visibleGroups.length === 0 ? (
								<div className="flex flex-col items-center gap-1 py-16 text-center">
									<p className="text-control font-medium text-foreground">{t("artifacts.noMatches")}</p>
									<Button variant="quiet" size="sm" onClick={clearFilters}>
										{t("artifacts.clearFilters")}
									</Button>
								</div>
							) : (
								<ul ref={grid.listRef} aria-label={t("artifacts.title")} className={view === "gallery" ? ARTIFACT_GRID_CLASS : "grid grid-cols-[minmax(0,1fr)] gap-1.5"}>
									<GridSpacer height={grid.before} />
									{cards.slice(grid.start, grid.end).map(({ row, projectName, placeName, owner: rowOwner, pinned }) => (
										<ArtifactCard key={artifactKey(row)} row={row} projectName={projectName} placeName={placeName} owner={rowOwner} pinned={pinned} view={view} previewLive={livePreviews.has(artifactKey(row))} onPreview={openPreview} selection={selectionFor(row, projectName)} />
									))}
									<GridSpacer height={grid.after} />
								</ul>
							)}
						</div>
					</ScrollArea.Viewport>
					<ScrollArea.Scrollbar orientation="vertical" className="z-10 flex w-2 touch-none select-none p-0.5">
						<ScrollArea.Thumb className="relative flex-1 rounded-full bg-[color-mix(in_srgb,var(--color-scrollbar)_42%,transparent)] hover:bg-[color-mix(in_srgb,var(--color-scrollbar)_56%,transparent)]" />
					</ScrollArea.Scrollbar>
				</main>
			</ScrollArea.Root>
			<ArtifactPreviewModal artifact={previewRow} projectName={preview?.projectName ?? ""} onClose={() => setPreview(null)} />
			<ConfirmDialog
				busy={bulkDelete.isPending}
				confirmLabel={t("artifacts.bulkDeleteConfirm", { count: selectedRows.length })}
				description={selectedPublished > 0
					? `${t("artifacts.bulkDeleteDescription")} ${t("artifacts.bulkDeletePublished", { count: selectedPublished })}`
					: t("artifacts.bulkDeleteDescription")}
				destructive
				error={bulkDelete.error?.message ?? null}
				onConfirm={() => bulkDelete.mutate(selectedRows)}
				onOpenChange={setConfirmingBulkDelete}
				open={confirmingBulkDelete}
				title={t("artifacts.bulkDeleteTitle", { count: selectedRows.length })}
			>
				<ul aria-label={t("artifacts.selection")} className="space-y-0.5 text-control text-foreground">
					{selectedRows.slice(0, MAX_CONFIRM_NAMES).map((row) => <li key={artifactKey(row)} className="truncate">{row.name}</li>)}
					{selectedRows.length > MAX_CONFIRM_NAMES ? (
						<li className="text-muted-foreground">{t("artifacts.bulkDeleteMore", { count: selectedRows.length - MAX_CONFIRM_NAMES })}</li>
					) : null}
				</ul>
			</ConfirmDialog>
		</div>
	);
}

/** A remote host's project name with the host's, so equal names on two machines tell apart. */
function withHostLabel(name: string, hostLabel: string | false | undefined): string {
	return hostLabel ? `${name} · ${hostLabel}` : name;
}

/** A filter-menu row that opens its options to the side, showing the current value. */
function FilterMenuRow({ label, value }: { label: string; value: string }) {
	return (
		<DropdownMenuSubTrigger>
			<span className="flex-1 text-foreground">{label}</span>
			<span className="max-w-36 truncate ps-4">{value}</span>
		</DropdownMenuSubTrigger>
	);
}

/** `values` with `value` added, or removed when it is already there. */
function toggled<T>(values: readonly T[], value: T): readonly T[] {
	return values.includes(value) ? values.filter((item) => item !== value) : [...values, value];
}

/**
 * One value of a filter. Choosing it keeps the menu open, so several values,
 * in one filter or several, are picked in one go.
 */
function FilterOption({ checked, onToggle, children }: { checked: boolean; onToggle: () => void; children: ReactNode }) {
	return (
		<DropdownMenuCheckboxItem checked={checked} onCheckedChange={onToggle} onSelect={(event) => event.preventDefault()}>
			{children}
		</DropdownMenuCheckboxItem>
	);
}

/**
 * One artifact in the list or the gallery: its name, project and session,
 * Published mark and last change, with Pin, Open in session and its menu.
 * Opening it previews the file on this page; while selecting it toggles.
 */
// Memoized: a row scrolling into view re-renders the list, and only the new cards need to render.
const ArtifactCard = memo(function ArtifactCard({ row, projectName, placeName, owner, pinned, view, previewLive = true, onPreview, selection }: { row: ArtifactRow; projectName: string; placeName: string; owner: ArtifactOwner; pinned: boolean; view: ArtifactViewMode; previewLive?: boolean; onPreview: (row: ArtifactRow, projectName: string) => void; selection?: ArtifactCardSelection }) {
	const { t } = useTranslation();
	const id = artifactKey(row);
	const togglePinnedArtifact = useArtifactsViewStore((state) => state.togglePinnedArtifact);
	const share = useArtifactShareFile(row);
	const requestWorkspaceFileOpen = useUiStore((state) => state.requestWorkspaceFileOpen);
	const navigateToSession = useNavigateToSession();
	const startSession = useStartSessionFromArtifact();
	const KindIcon = artifactFileIcon(row);
	const selectionDescriptionId = useId();
	const context = artifactContext(t, row, placeName, owner);

	const open = () => {
		onPreview(row, projectName);
	};
	// A kept file has no session to open; a new one can start from it.
	const openInSession = () => {
		if (row.kept) {
			void startSession(row);
			return;
		}
		requestWorkspaceFileOpen(row.sessionId, row.path, row.hostId, "artifact");
		navigateToSession(row.projectId, row.sessionId, row.hostId ?? LOCAL_HOST);
	};

	if (view === "list") {
		const toggle = selection && !selection.disabledReason ? selection.onToggle : undefined;
		return (
			<li>
				{/* Name and context truncate; the Published mark, the time and the
				    actions never shrink, so the time stays visible in a narrow
				    panel. The actions keep their space while hidden, so hovering
				    a row moves nothing. Below @3xl the context drops under the
				    name; below @lg Pin and Open in session live in the ⋯ menu. */}
				<div className={cn(
					// The hovered row stacks the interactive layer on the hover surface:
					// the hover surface alone is only a shade off the row's own.
					"group/artifact relative flex min-h-12 items-center gap-3 rounded-md bg-card py-1.5 ps-4 pe-2 transition-none hover:bg-accent focus-within:bg-accent @max-3xl:min-h-14",
					"before:pointer-events-none before:absolute before:inset-0 before:rounded-md hover:before:bg-interactive-active focus-within:before:bg-interactive-active",
					selection?.selected && "ring-2 ring-primary",
				)}>
					{selection ? <span id={selectionDescriptionId} className="sr-only">{selection.description}</span> : null}
					{selection ? (
						<Checkbox
							aria-describedby={selectionDescriptionId}
							aria-label={selection.label}
							checked={selection.selected}
							className="relative z-[2] transition-none duration-0"
							disabled={Boolean(selection.disabledReason)}
							onCheckedChange={toggle}
							title={selection.disabledReason}
						/>
					) : null}
					<Pressable
						className={cn(
							"flex min-w-0 flex-1 items-center gap-3 self-stretch after:absolute after:inset-0 after:z-[1] after:rounded-md focus-visible:after:ring-2 focus-visible:after:ring-ring/60",
							selection?.disabledReason && "cursor-not-allowed",
						)}
						onClick={selection ? toggle : open}
						title={selection?.disabledReason ?? `${projectName} · ${row.sessionTitle} · ${row.path}`}
						{...(selection ? { tabIndex: -1, "aria-hidden": true } : {})}
					>
						<KindIcon aria-hidden="true" className="relative z-[1] size-icon-sm shrink-0 text-muted-foreground" />
						<span className="relative z-[1] flex min-w-0 flex-1 flex-col @3xl:flex-row @3xl:items-baseline @3xl:gap-2.5">
							<span className="flex min-w-0 items-center gap-1.5 @3xl:max-w-[60%] @3xl:shrink-0">
								{pinned ? (
									<span role="img" aria-label={t("artifacts.pinned")} className="inline-flex shrink-0 text-passive">
										<Pin aria-hidden="true" className="size-3" />
									</span>
								) : null}
								<span className="min-w-0 truncate text-control font-medium text-foreground">{row.name}</span>
							</span>
							<span className="min-w-0 truncate text-xs text-muted-foreground">{context}</span>
						</span>
					</Pressable>
					<div className="relative z-[2] flex shrink-0 items-center gap-3">
						{/* Empty for an unshared artifact, so every row's time lines up. */}
						<span className="inline-flex size-icon-sm items-center justify-center">
							<ArtifactShareMark row={row} iconClassName="size-icon-sm" />
						</span>
						{/* "14h ago", and "Updated 14h ago" while the row is hovered or focused.
						    A title, not a Tooltip: one per row costs too much to mount in a long list. */}
						<time dateTime={row.updatedAt} title={new Date(row.updatedAt).toLocaleString()} className="min-w-14 whitespace-nowrap text-right text-xs tabular-nums text-passive group-hover/artifact:text-foreground group-focus-within/artifact:text-foreground">
							<span className="@lg:group-hover/artifact:hidden @lg:group-focus-within/artifact:hidden">{formatTimeCompact(row.updatedAt)}</span>
							<span className="hidden @lg:group-hover/artifact:inline @lg:group-focus-within/artifact:inline">{t("shell.updatedAt", { time: formatTimeCompact(row.updatedAt) })}</span>
						</time>
					</div>
					{selection ? null : <div className="relative z-[2] flex shrink-0 items-center gap-1 @max-lg:[&>button]:hidden">
						{row.bundle || row.shared ? null : <DeferredArtifactActionsMenu
							artifact={row}
							hostId={row.hostId}
							session={{ id: row.sessionId, cloud: row.cloud }}
							iconOnly
							share={share}
							onOpenInSession={openInSession}
							startsSession={Boolean(row.kept)}
							pinned={pinned}
							onTogglePin={() => togglePinnedArtifact(id)}
							className="bg-[var(--solid-bg-secondary,var(--color-bg-secondary))] hover:bg-[var(--solid-accent,var(--accent))] dark:hover:bg-[var(--solid-accent,var(--accent))] transition-none duration-0 [@media(hover:hover)]:opacity-0 group-hover/artifact:opacity-100 group-focus-within/artifact:opacity-100 data-[state=open]:opacity-100"
						/>}
					</div>}
				</div>
			</li>
		);
	}

	return (
		<ArtifactGalleryCard
			row={row}
			previewLive={previewLive}
			context={context}
			onPreview={open}
			selection={selection}
			actions={selection || row.bundle || row.shared ? null : (
				<DeferredArtifactActionsMenu
					artifact={row}
					hostId={row.hostId}
					session={{ id: row.sessionId, cloud: row.cloud }}
					iconOnly
					share={share}
					onOpenInSession={openInSession}
					startsSession={Boolean(row.kept)}
					pinned={pinned}
					onTogglePin={() => togglePinnedArtifact(id)}
					className="bg-[var(--solid-bg-secondary,var(--color-bg-secondary))] hover:bg-[var(--solid-accent,var(--accent))] dark:hover:bg-[var(--solid-accent,var(--accent))] transition-none duration-0 [@media(hover:hover)]:opacity-0 group-hover:opacity-100 group-focus-within:opacity-100 data-[state=open]:opacity-100"
				/>
			)}
		/>
	);
});

function EmptyArtifacts({ workspaces }: { workspaces: readonly WorkspaceSummary[] }) {
	const { t } = useTranslation();
	const openGlobalSettings = useUiStore((state) => state.openGlobalSettings);
	const { agentReady, create, pending } = useCreateArtifactSession();
	const hasCloudProject = workspaces.some((workspace) => workspace.kind === CLOUD_PROJECT_KIND && Boolean(workspace.cloudOrgId));

	// Without a local agent a cloud project, which runs its own, can still start one.
	if (!agentReady && !hasCloudProject) {
		return (
			<div className="flex flex-col items-center gap-1 py-24 text-center">
				<h2 className="text-heading-sm font-semibold text-foreground">{t("artifacts.noAgent.title")}</h2>
				<p className="max-w-72 text-xs text-muted-foreground">{t("artifacts.noAgent.description")}</p>
				<Button variant="ghost" size="sm" className="mt-2" onClick={() => openGlobalSettings("harness")}>
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
	const projectAvatars = useProjectAvatars(workspaces);
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
			<Button type="button" size="icon-sm"
				variant="quiet"
				className={className}
				disabled={disabled}
				aria-busy={pending}
				aria-label={pending ? t("artifacts.creating") : t("artifacts.new")}
			>
				{pending ? (
					<Loader2 aria-hidden="true" className="size-icon-sm animate-spin motion-reduce:animate-none" />
				) : (
					<Plus aria-hidden="true" className="size-icon-sm" />
				)}
			</Button>
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
			{label === "new" ? (
				<Tooltip>
					<TooltipTrigger asChild>
						<DropdownMenuTrigger asChild>{trigger}</DropdownMenuTrigger>
					</TooltipTrigger>
					<TooltipContent side="bottom">{t("artifacts.new")}</TooltipContent>
				</Tooltip>
			) : (
				<DropdownMenuTrigger asChild>{trigger}</DropdownMenuTrigger>
			)}
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
						<ArtifactProjectIcon avatarUrl={projectAvatars.get(project.id)} workspace={project} />
						<span className="truncate">{project.name}</span>
					</DropdownMenuItem>
				))}
			</DropdownMenuContent>
		</DropdownMenu>
	);
}

/** The session of a row, as unpublishing names it. */
function rowSession(row: ArtifactRow) {
	return { id: row.sessionId, cloud: row.cloud, hostId: row.hostId, workspaceId: row.projectId };
}
