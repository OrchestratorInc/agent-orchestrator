import { useEffect, useLayoutEffect, useRef, useState, type ReactNode } from "react";
import { useMutation, useQueryClient } from "@tanstack/react-query";
import { useTranslation } from "react-i18next";
import { ArrowUpRight, ChevronDown, Copy, Download, Ellipsis, ExternalLink, FolderOpen, MoreVertical, Pencil, Pin, PinOff, Share, SquareTerminal, Trash2 } from "@/components/app-icons";
import type { OpenArtifactsTargetId } from "../../shared/editor-handoff";
import { isBrowserArtifactPath } from "../../shared/editor-handoff";
import { useOpenArtifactInBrowser, useOpenSessionArtifacts } from "../hooks/useEditorHandoff";
import { deleteSessionArtifactFile, sessionArtifactFileQueryOptions } from "../hooks/useSessionWorkspaceFiles";
import { cloudSessionsQueryKey, workspaceQueryKeyForHost } from "../hooks/useWorkspaceQuery";
import { useCloudCp } from "../hooks/useCloudCp";
import { useCloudMeUserId } from "../hooks/useCloudMeUserId";
import { cloudArtifactFileUrl, useCloudArtifactAccess } from "../hooks/useCloudArtifacts";
import { aoBridge } from "../lib/bridge";
import type { CloudCpClient } from "../lib/cloud-cp";
import { artifactChangeError, artifactKey, canChangeSessionArtifacts } from "../lib/artifacts";
import { fileMediaKind } from "../lib/file-media";
import { isMacPlatform, isWindowsPlatform } from "../lib/platform";
import { cn } from "../lib/utils";
import { useArtifactsViewStore } from "../stores/artifacts-view-store";
import type { SessionArtifact, WorkspaceSession } from "../types/workspace";
import { ConfirmDialog } from "./ConfirmDialog";
import { Button } from "./ui/button";
import { ShareArtifactMenuPopover, VIDEO_FILE, useShareAvailable } from "./ShareArtifactPopover";
import type { CloudShareFile } from "../../shared/cloud-share";
import { DropdownMenu, DropdownMenuContent, DropdownMenuItem, DropdownMenuLabel, DropdownMenuSeparator, DropdownMenuTrigger } from "./ui/dropdown-menu";
import { Tooltip, TooltipContent, TooltipTrigger } from "./ui/tooltip";

const ERROR_CLEAR_MS = 5_000;

export function fileManagerName(): string {
	return isMacPlatform() ? "Finder" : isWindowsPlatform() ? "File Explorer" : "File Manager";
}

export function terminalName(): string {
	return isWindowsPlatform() ? "Command Prompt" : "Terminal";
}

/**
 * Copies a cloud session's artifact files to its folder under ~/.ao and opens
 * it in the file manager, or, given `reveal`, copies only that artifact and
 * shows it there: a cloud session's artifacts live in the control plane, so
 * this is the folder a local session's "Show in" opens. With `browser`, it
 * copies them all, so a page's own files load, and opens `reveal` in the
 * default browser.
 */
export async function openCloudArtifactsFolder(
	client: CloudCpClient,
	baseUrl: string,
	orgId: string,
	sessionId: string,
	reveal?: Pick<SessionArtifact, "path" | "updatedAt">,
	browser = false,
): Promise<void> {
	const [files, access] = await Promise.all([
		reveal && !browser ? [reveal] : client.listSessionArtifacts(orgId, sessionId).then(({ artifacts }) => artifacts),
		client.createSessionArtifactAccess(orgId, sessionId),
	]);
	const root = `${baseUrl.replace(/\/+$/, "")}${access.filesPrefix}`;
	await aoBridge.cloudArtifact.openFolder({
		sessionId,
		files: files.map((file) => ({ path: file.path, url: cloudArtifactFileUrl(root, file.path, file.updatedAt) })),
		reveal: reveal?.path,
		...(browser ? { open: "browser" as const } : {}),
	});
}

/**
 * The open artifact's name, as a menu: show it in the file manager or a
 * terminal (local sessions only), copy its contents, or delete it. Another
 * member's cloud session files are view only: the menu then offers none of
 * the actions that change or copy them, and says why.
 */
export function ArtifactActionsMenu({
	artifact,
	downloadUrl,
	hostId,
	session,
	iconOnly = false,
	verticalDots = false,
	share,
	variant = "default",
	onEdit,
	pinInMenu = false,
	pinned = false,
	onTogglePin,
	onOpenInSession,
	onDeleted,
	errorClassName,
	className,
	children,
}: {
	artifact: Pick<SessionArtifact, "name" | "path" | "rawUrl" | "updatedAt">;
	downloadUrl?: string;
	hostId?: string;
	session: Pick<WorkspaceSession, "id" | "cloud">;
	iconOnly?: boolean;
	/** Draws the icon-only trigger as a vertical ellipsis. */
	verticalDots?: boolean;
	/** Adds a Share item to an icon-only menu; it opens the share panel under the trigger. */
	share?: Omit<CloudShareFile, "baseUrl" | "shareId">;
	variant?: "default" | "sidebar";
	onEdit?: () => void;
	pinInMenu?: boolean;
	pinned?: boolean;
	onTogglePin?: () => void;
	onOpenInSession?: () => void;
	onDeleted?: () => void;
	errorClassName?: string;
	className?: string;
	children?: ReactNode;
}) {
	const { t } = useTranslation();
	const queryClient = useQueryClient();
	const open = useOpenSessionArtifacts();
	const browser = useOpenArtifactInBrowser();
	const [copyError, setCopyError] = useState("");
	const [confirmingDelete, setConfirmingDelete] = useState(false);
	const [menuOpen, setMenuOpen] = useState(false);
	const [shareOpen, setShareOpen] = useState(false);
	const sharing = useRef(false);
	const shareAvailable = useShareAvailable();
	const canShare = Boolean(share) && iconOnly && shareAvailable && !VIDEO_FILE.test(artifact.path);
	const actionClassName = cn(className, (menuOpen || shareOpen) && "opacity-100 [@media(hover:hover)]:opacity-100");
	// The artifact directory is on this machine only for a local session.
	const local = !hostId && !session.cloud;
	const sidebar = variant === "sidebar";
	const { client: cloudClient, baseUrl: cloudBaseUrl } = useCloudCp(Boolean(session.cloud));
	const owner = canChangeSessionArtifacts(session.cloud, useCloudMeUserId(Boolean(session.cloud)));
	const access = useCloudArtifactAccess(session, Boolean(session.cloud) && !downloadUrl);
	const cloudFileUrl = session.cloud
		? downloadUrl ?? (access.data ? cloudArtifactFileUrl(access.data, artifact.path, artifact.updatedAt) : undefined)
		: undefined;
	// The deleted file leaves the session's artifact list on the next read.
	const remove = useMutation({
		mutationFn: () => session.cloud
			? cloudClient.deleteSessionArtifact(session.cloud.orgId, session.id, artifact.path).catch((error: unknown) => { throw artifactChangeError(error, t("artifacts.viewOnly")); })
			: deleteSessionArtifactFile({ path: artifact.path, sessionId: session.id, hostId }),
		onSuccess: async () => {
			setConfirmingDelete(false);
			if (!hostId) useArtifactsViewStore.getState().unpinArtifact(artifactKey({ sessionId: session.id, path: artifact.path }));
			onDeleted?.();
			await queryClient.invalidateQueries({ queryKey: session.cloud ? cloudSessionsQueryKey : workspaceQueryKeyForHost(hostId) });
		},
	});
	const markdown = /\.(md|markdown)$/i.test(artifact.path);
	const confirmDelete = () => {
		remove.reset();
		setConfirmingDelete(true);
	};
	const reveal = (targetId: OpenArtifactsTargetId) => {
		browser.reset();
		setCopyError("");
		open.mutate({ sessionId: session.id, targetId, path: artifact.path });
	};
	// The main process resolves the path inside the session's artifact
	// directory; a cloud session's file opens from its copy under ~/.ao.
	// A cloud open copies the session's files first; one at a time.
	const [cloudBrowserOpening, setCloudBrowserOpening] = useState(false);
	const openInBrowser = async () => {
		open.reset();
		setCopyError("");
		if (!session.cloud) {
			browser.mutate({ sessionId: session.id, path: artifact.path });
			return;
		}
		setCloudBrowserOpening(true);
		try {
			await openCloudArtifactsFolder(cloudClient, cloudBaseUrl, session.cloud.orgId, session.id, artifact, true);
		} catch (error) {
			setCopyError(error instanceof Error ? error.message : String(error));
		} finally {
			setCloudBrowserOpening(false);
		}
	};
	const browserOpening = browser.isPending || cloudBrowserOpening;
	const browserReadable = isBrowserArtifactPath(artifact.path);
	const revealCloud = async () => {
		open.reset();
		setCopyError("");
		try {
			if (!session.cloud) return;
			await openCloudArtifactsFolder(cloudClient, cloudBaseUrl, session.cloud.orgId, session.id, artifact);
		} catch (error) {
			setCopyError(error instanceof Error ? error.message : String(error));
		}
	};
	const copy = async () => {
		open.reset();
		setCopyError("");
		try {
			const detail = await queryClient.fetchQuery(sessionArtifactFileQueryOptions(session.id, artifact.path, artifact.rawUrl ?? cloudFileUrl, t("files.error.loadArtifact"), Boolean(session.cloud)));
			// A cloud file is known to be text only once it is read.
			if (session.cloud && detail.binary) throw new Error(t("artifacts.notText"));
			await aoBridge.clipboard.writeText(detail.content);
		} catch (error) {
			setCopyError(error instanceof Error ? error.message : String(error));
		}
	};
	const download = async () => {
		open.reset();
		setCopyError("");
		try {
			if (!cloudFileUrl) throw new Error(t("artifacts.accessError"));
			const bytes = await aoBridge.cloudArtifact.fetch(cloudFileUrl);
			await aoBridge.chatImage.save({ name: artifact.name, bytes });
		} catch (error) {
			setCopyError(error instanceof Error ? error.message : String(error));
		}
	};
	const copyable = owner && !fileMediaKind(artifact.path);

	const error = open.error?.message || browser.error?.message || copyError;
	const resetOpen = open.reset;
	const resetBrowser = browser.reset;
	useEffect(() => {
		if (!error) return;
		const timer = setTimeout(() => {
			resetOpen();
			resetBrowser();
			setCopyError("");
		}, ERROR_CLEAR_MS);
		return () => clearTimeout(timer);
	}, [error, resetBrowser, resetOpen]);
	const menuTrigger = (
		<DropdownMenuTrigger asChild>
			<Button
				aria-label={iconOnly ? `${t("files.moreFileActions")}: ${artifact.name}` : undefined}
				className={cn(!iconOnly && "min-w-0 gap-1 px-1.5", actionClassName)}
				size={iconOnly ? "icon-sm" : "sm"}
				title={iconOnly ? undefined : artifact.path}
				type="button"
				variant={iconOnly ? "quiet" : "ghost"}
			>
				{iconOnly ? (verticalDots ? <MoreVertical aria-hidden="true" className="size-icon-sm" /> : <Ellipsis aria-hidden="true" className="size-icon-sm" />) : (
					<>
						<span className="truncate">{artifact.name}</span>
						<ChevronDown aria-hidden="true" className="size-icon-xs shrink-0 text-muted-foreground" />
					</>
				)}
			</Button>
		</DropdownMenuTrigger>
	);
	return (
		<>
			{sidebar ? (
				<>
					{owner && onEdit && !fileMediaKind(artifact.path) ? (
						<Button aria-label={`${t("files.editFile")}: ${artifact.name}`} className={actionClassName} onClick={onEdit} size="icon-sm" type="button" variant="quiet">
							<Pencil aria-hidden="true" className="size-icon-sm" />
						</Button>
					) : null}
					{owner ? (
						<Button aria-label={`${t("artifacts.delete")}: ${artifact.name}`} className={actionClassName} onClick={confirmDelete} size="icon-sm" type="button" variant="destructive-ghost">
							<Trash2 aria-hidden="true" className="size-icon-sm" />
						</Button>
					) : null}
				</>
			) : null}
			{onTogglePin && !pinInMenu ? (
				<Tooltip>
					<TooltipTrigger asChild>
						<Button
							aria-label={`${t(pinned ? "artifacts.unpin" : "artifacts.pin")}: ${artifact.name}`}
							aria-pressed={pinned}
							className={actionClassName}
							onClick={onTogglePin}
							size="icon-sm"
							type="button"
							variant="quiet"
						>
							{pinned ? <PinOff aria-hidden="true" className="size-icon-sm" /> : <Pin aria-hidden="true" className="size-icon-sm" />}
						</Button>
					</TooltipTrigger>
					<TooltipContent side="bottom">{t(pinned ? "artifacts.unpin" : "artifacts.pin")}</TooltipContent>
				</Tooltip>
			) : null}
			{onOpenInSession ? (
				<Tooltip>
					<TooltipTrigger asChild>
						<Button
							aria-label={`${t("artifacts.openInSession")}: ${artifact.name}`}
							className={actionClassName}
							onClick={onOpenInSession}
							size="icon-sm"
							type="button"
							variant="quiet"
						>
							<ArrowUpRight aria-hidden="true" className="size-icon-sm" />
						</Button>
					</TooltipTrigger>
					<TooltipContent side="bottom">{t("artifacts.openInSession")}</TooltipContent>
				</Tooltip>
			) : null}
			{!sidebar || local || session.cloud ? <DropdownMenu open={menuOpen} onOpenChange={setMenuOpen}>
				{iconOnly ? (
					canShare && share ? (
						<ShareArtifactMenuPopover file={share} name={artifact.name} onOpenChange={setShareOpen} open={shareOpen}>
							<span className="inline-flex">{menuTrigger}</span>
						</ShareArtifactMenuPopover>
					) : <span className="inline-flex">{menuTrigger}</span>
				) : menuTrigger}
				<DropdownMenuContent
					align={iconOnly ? "end" : "start"}
					className="min-w-44"
					data-browser-native-overlay="true"
					// Focus goes to the share panel, not back to the trigger, which would close it.
					onCloseAutoFocus={(event) => {
						if (!sharing.current) return;
						sharing.current = false;
						event.preventDefault();
					}}
				>
					{/* The caller's rows come first and end with their own separator, if any render. */}
					{children}
					{onTogglePin || onOpenInSession || canShare ? (
						<>
							{canShare ? (
								<DropdownMenuItem onSelect={() => { sharing.current = true; setShareOpen(true); }}>
									<Share aria-hidden="true" />
									{t("artifacts.share.publish")}
								</DropdownMenuItem>
							) : null}
							{onTogglePin ? (
								<DropdownMenuItem onSelect={onTogglePin}>
									{pinned ? <PinOff aria-hidden="true" /> : <Pin aria-hidden="true" />}
									{t(pinned ? "artifacts.unpin" : "artifacts.pin")}
								</DropdownMenuItem>
							) : null}
							{onOpenInSession ? (
								<DropdownMenuItem onSelect={onOpenInSession}>
									<ArrowUpRight aria-hidden="true" />
									{t("artifacts.openInSession")}
								</DropdownMenuItem>
							) : null}
							<DropdownMenuSeparator />
						</>
					) : null}
					{local ? (
						<>
							<DropdownMenuItem onSelect={() => reveal("file-manager")}>
								<FolderOpen aria-hidden="true" />
								{t("artifacts.showIn", { name: fileManagerName() })}
							</DropdownMenuItem>
							{!sidebar ? (
								<>
									<DropdownMenuItem onSelect={() => reveal("terminal")}>
										<SquareTerminal aria-hidden="true" />
										{t("artifacts.showIn", { name: terminalName() })}
									</DropdownMenuItem>
									{browserReadable ? (
										<DropdownMenuItem disabled={browserOpening} onSelect={() => void openInBrowser()}>
											<ExternalLink aria-hidden="true" />
											{t("artifacts.openInBrowser")}
										</DropdownMenuItem>
									) : null}
									{copyable || session.cloud ? <DropdownMenuSeparator /> : null}
								</>
							) : null}
						</>
					) : null}
					{session.cloud && owner ? (
						<>
							<DropdownMenuItem onSelect={() => void revealCloud()}>
								<FolderOpen aria-hidden="true" />
								{t("artifacts.showIn", { name: fileManagerName() })}
							</DropdownMenuItem>
							{!sidebar && browserReadable ? (
								<DropdownMenuItem disabled={browserOpening} onSelect={() => void openInBrowser()}>
									<ExternalLink aria-hidden="true" />
									{t("artifacts.openInBrowser")}
								</DropdownMenuItem>
							) : null}
							{!sidebar ? <DropdownMenuSeparator /> : null}
						</>
					) : null}
					{!sidebar && copyable ? (
						<DropdownMenuItem onSelect={() => void copy()}>
							<Copy aria-hidden="true" />
							{t(markdown ? "artifacts.copyMarkdown" : "artifacts.copyContents")}
						</DropdownMenuItem>
					) : null}
					{!sidebar && owner ? (
						<>
							{session.cloud ? (
								<DropdownMenuItem onSelect={() => void download()}>
									<Download aria-hidden="true" />
									{t("artifacts.download")}
								</DropdownMenuItem>
							) : null}
							{local || copyable || session.cloud ? <DropdownMenuSeparator /> : null}
							<DropdownMenuItem
								className="text-destructive focus:text-destructive [&_svg]:text-destructive focus:[&_svg]:text-destructive"
								onSelect={confirmDelete}
							>
								<Trash2 aria-hidden="true" />
								{t("artifacts.delete")}
							</DropdownMenuItem>
						</>
					) : null}
					{owner ? null : (
						<DropdownMenuLabel className="max-w-64 whitespace-normal font-normal text-muted-foreground">{t("artifacts.viewOnly")}</DropdownMenuLabel>
					)}
				</DropdownMenuContent>
			</DropdownMenu> : null}
			<ConfirmDialog
				busy={remove.isPending}
				confirmLabel={t("artifacts.delete")}
				description={t(session.cloud ? "artifacts.deleteCloudDescription" : "artifacts.deleteDescription", { name: artifact.name })}
				destructive
				error={remove.error?.message ?? null}
				onConfirm={() => remove.mutate()}
				onOpenChange={setConfirmingDelete}
				open={confirmingDelete}
				title={t("artifacts.deleteTitle")}
			/>
			{error ? (
				<span className={cn(
					"text-caption text-destructive",
					iconOnly
						? "absolute bottom-full right-0 mb-2 w-64 max-w-[calc(100vw-2rem)] rounded-md bg-popover p-2 shadow-md"
						: "min-w-0 truncate",
					errorClassName,
				)} role="alert" title={error}>
					{error}
				</span>
			) : null}
		</>
	);
}


type ArtifactActionsMenuProps = Parameters<typeof ArtifactActionsMenu>[0];

/**
 * An icon-only ArtifactActionsMenu for one card of a long list. The Artifacts
 * page draws one per artifact, and the menu, its tooltips, share popover and
 * delete dialog cost far more to mount than the buttons a closed menu shows.
 * Until the pointer or focus first enters the card (its `li`), this draws
 * those buttons with the markup the closed menu gives them and starts the same
 * queries; then it mounts the menu in their place and keeps it.
 *
 * Focus on a replaced button moves to its replacement before the next paint,
 * and a press that still reaches a replaced button, as a touch or a scripted
 * click can, is passed on to its replacement.
 */
export function DeferredArtifactActionsMenu(props: ArtifactActionsMenuProps) {
	const { t } = useTranslation();
	const { artifact, session, className, pinned = false, onTogglePin, pinInMenu = false, onOpenInSession, verticalDots = false } = props;
	const [active, setActive] = useState(false);
	const pinRef = useRef<HTMLButtonElement>(null);
	const openRef = useRef<HTMLButtonElement>(null);
	const menuRef = useRef<HTMLButtonElement>(null);
	const cardRef = useRef<HTMLElement | null>(null);
	// Each stand-in's place among its card's buttons, where its replacement goes.
	const places = useRef(new Map<HTMLButtonElement, number>());
	// What reached the stand-ins before the menu replaced them, to repeat on it.
	const replay = useRef<{ focus?: number; presses: Array<[place: number, press: Press]> }>({ presses: [] });
	// The queries the menu starts, so it offers the same items when it first opens.
	useShareAvailable();
	useCloudMeUserId(Boolean(session.cloud));
	useCloudArtifactAccess(session, Boolean(session.cloud) && !props.downloadUrl);

	useEffect(() => {
		const stand = [pinRef.current, openRef.current, menuRef.current].filter((button): button is HTMLButtonElement => Boolean(button));
		const card = menuRef.current?.closest("li") ?? menuRef.current?.parentElement?.parentElement ?? null;
		if (!card) return;
		cardRef.current = card;
		let activated = false;
		const activate = (event: Event) => {
			if (activated) return;
			activated = true;
			const buttons = [...card.querySelectorAll("button")];
			for (const button of stand) places.current.set(button, buttons.indexOf(button));
			const target = event.target instanceof HTMLButtonElement ? places.current.get(event.target) : undefined;
			if (event.type === "focusin" && target !== undefined) replay.current.focus = target;
			card.removeEventListener("pointermove", activate);
			card.removeEventListener("focusin", activate);
			setActive(true);
		};
		const pass = (event: Event) => {
			const button = event.currentTarget as HTMLButtonElement;
			const press: Press = event.type === "click" ? "click" : event as PointerEvent;
			activate(event);
			const place = places.current.get(button) ?? -1;
			// Still in the page, the menu has yet to replace it: repeat the press once it has.
			if (button.isConnected) replay.current.presses.push([place, press]);
			else passOn(card, place, press);
		};
		card.addEventListener("pointermove", activate);
		card.addEventListener("focusin", activate);
		for (const button of stand) {
			button.addEventListener("pointerdown", pass);
			button.addEventListener("click", pass);
		}
		return () => {
			card.removeEventListener("pointermove", activate);
			card.removeEventListener("focusin", activate);
			for (const button of stand) {
				button.removeEventListener("pointerdown", pass);
				button.removeEventListener("click", pass);
			}
		};
	}, []);

	useLayoutEffect(() => {
		const card = cardRef.current;
		if (!active || !card) return;
		const { focus, presses } = replay.current;
		replay.current = { presses: [] };
		if (focus !== undefined) {
			const focused = document.activeElement;
			const button = card.querySelectorAll("button")[focus];
			if (button && (!focused || focused === document.body || !focused.isConnected)) button.focus();
		}
		for (const [place, press] of presses) passOn(card, place, press);
	}, [active]);

	if (active) return <ArtifactActionsMenu {...props} />;
	const actionClassName = cn(className);
	return (
		<>
			{onTogglePin && !pinInMenu ? (
				<Button
					ref={pinRef}
					aria-label={`${t(pinned ? "artifacts.unpin" : "artifacts.pin")}: ${artifact.name}`}
					aria-pressed={pinned}
					className={actionClassName}
					data-slot="tooltip-trigger"
					data-state="closed"
					size="icon-sm"
					type="button"
					variant="quiet"
				>
					{pinned ? <PinOff aria-hidden="true" className="size-icon-sm" /> : <Pin aria-hidden="true" className="size-icon-sm" />}
				</Button>
			) : null}
			{onOpenInSession ? (
				<Button
					ref={openRef}
					aria-label={`${t("artifacts.openInSession")}: ${artifact.name}`}
					className={actionClassName}
					data-slot="tooltip-trigger"
					data-state="closed"
					size="icon-sm"
					type="button"
					variant="quiet"
				>
					<ArrowUpRight aria-hidden="true" className="size-icon-sm" />
				</Button>
			) : null}
			<span className="inline-flex">
				<Button
					ref={menuRef}
					aria-expanded={false}
					aria-haspopup="menu"
					aria-label={`${t("files.moreFileActions")}: ${artifact.name}`}
					className={actionClassName}
					data-state="closed"
					size="icon-sm"
					type="button"
					variant="quiet"
				>
					{verticalDots ? <MoreVertical aria-hidden="true" className="size-icon-sm" /> : <Ellipsis aria-hidden="true" className="size-icon-sm" />}
				</Button>
			</span>
		</>
	);
}

type Press = "click" | PointerEvent;

/** Sends a press that reached a stand-in to the button now in its place. */
function passOn(card: HTMLElement, place: number, event: Press) {
	const button = place < 0 ? undefined : card.querySelectorAll("button")[place];
	if (!button) return;
	if (event === "click") {
		button.click();
		return;
	}
	const Pointer = typeof PointerEvent === "function" ? PointerEvent : MouseEvent;
	button.dispatchEvent(new Pointer("pointerdown", {
		bubbles: true,
		cancelable: true,
		button: event.button,
		ctrlKey: event.ctrlKey,
		metaKey: event.metaKey,
		shiftKey: event.shiftKey,
		altKey: event.altKey,
		clientX: event.clientX,
		clientY: event.clientY,
		...("pointerType" in event ? { pointerType: event.pointerType, pointerId: event.pointerId, isPrimary: event.isPrimary } : {}),
	}));
}
