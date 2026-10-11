import { useEffect, useRef, useState } from "react";
import { useTranslation } from "react-i18next";
import { Loader2, MessageSquare, Play, Plus, TerminalSquare } from "lucide-react";
import { useQueryClient } from "@tanstack/react-query";
import { useUiStore } from "../../stores/ui-store";
import { apiErrorMessage } from "../../lib/api-client";
import { useInvokeCueMutation, useProjectCuesQuery } from "../../hooks/useCuesQuery";
import { fetchProjectCues, projectCuesQueryKey, type CueDTO } from "../../lib/cues";
import { shellTerminalsQueryKey, toShellTerminal, type ShellTerminal } from "../../hooks/useShellTerminals";
import { markTerminalHandleFresh } from "../../lib/fresh-terminal-handles";
import { terminalShellRequestValue, useTerminalShellStore } from "../../stores/terminal-shell-store";
import { TopbarButton } from "../TopbarButton";
import {
	DropdownMenu,
	DropdownMenuContent,
	DropdownMenuItem,
	DropdownMenuSeparator,
	DropdownMenuTrigger,
} from "../ui/dropdown-menu";
import { CreateCueDialog } from "../CuesDialog";
import { readLastRunCue, rememberLastRunCue } from "../../lib/last-run-cue";

// Hovering the trigger opens the menu immediately — it is the discoverability
// affordance, not a tooltip. The close delay only lets the pointer cross the
// gap between the trigger and the menu without the menu vanishing underneath it.
const CUE_MENU_CLOSE_MS = 150;

// The cue runner requires a selected session; boards never render this control.
// Keying by target drops in-flight menu state when switching project or session.
export function CueRunMenu({
	projectId,
	sessionId,
	disabled = false,
}: {
	projectId: string;
	sessionId: string;
	disabled?: boolean;
}) {
	return (
		<CueRunMenuTrigger
			key={JSON.stringify([projectId, sessionId ?? null])}
			projectId={projectId}
			sessionId={sessionId}
			disabled={disabled}
		/>
	);
}

function CueRunMenuTrigger({
	projectId,
	sessionId,
	disabled,
}: {
	projectId: string;
	sessionId: string;
	disabled: boolean;
}) {
	const { t } = useTranslation();
	const showGlobalToast = useUiStore((state) => state.showGlobalToast);
	const queryClient = useQueryClient();
	const setActiveShellTerminal = useUiStore((state) => state.setActiveShellTerminal);
	const [open, setOpen] = useState(false);
	const [creating, setCreating] = useState(false);
	const [runningLastCue, setRunningLastCue] = useState(false);
	const pending = useRef(false);
	const generation = useRef(0);
	const hoverCloseTimer = useRef<number | null>(null);
	const buttonRef = useRef<HTMLButtonElement>(null);
	// The menu is hover-driven, so the pointerdown Radix uses to toggle a menu —
	// and the dismiss layer that same press wakes up — must not close a menu the
	// pointer is still inside. Escape is the keyboard escape hatch.
	const pointerInsideControl = useRef(false);
	const escapeRequested = useRef(false);
	const invokeMutation = useInvokeCueMutation();
	const [invokingId, setInvokingId] = useState<string | null>(null);
	const busy = invokingId !== null;
	const spinner = busy || runningLastCue;

	const cancelHoverClose = () => {
		if (hoverCloseTimer.current === null) return;
		window.clearTimeout(hoverCloseTimer.current);
		hoverCloseTimer.current = null;
	};
	const scheduleHoverClose = () => {
		cancelHoverClose();
		hoverCloseTimer.current = window.setTimeout(() => {
			hoverCloseTimer.current = null;
			setOpen(false);
		}, CUE_MENU_CLOSE_MS);
	};
	useEffect(() => () => {
		generation.current++;
		if (hoverCloseTimer.current !== null) window.clearTimeout(hoverCloseTimer.current);
	}, []);

	const handlePointerEnter = () => {
		pointerInsideControl.current = true;
		cancelHoverClose();
		if (disabled || spinner || creating) return;
		if (!open) openMenu();
	};

	const handlePointerLeave = () => {
		pointerInsideControl.current = false;
		scheduleHoverClose();
	};

	const handleMenuPointerEnter = () => {
		pointerInsideControl.current = true;
		cancelHoverClose();
	};

	// Opening never bumps the generation: with hover opening the menu, a pointer
	// drifting back over the trigger must not invalidate a dispatch that is still
	// in flight and drop its toast or navigation. Target changes and unmounts are
	// what retire stale work — this component is keyed by project and session.
	const openMenu = () => setOpen(true);

	const handleOpenChange = (next: boolean) => {
		if (next) {
			if (creating) return;
			openMenu();
			return;
		}
		if (pointerInsideControl.current && !escapeRequested.current) return;
		escapeRequested.current = false;
		cancelHoverClose();
		setOpen(false);
	};

	const handleInvoke = async (cue: CueDTO) => {
		if (pending.current) return;
		pending.current = true;
		const origin = generation.current;
		setInvokingId(cue.id);
		try {
			await useTerminalShellStore.getState().load();
			if (origin !== generation.current) return;
			const shell = terminalShellRequestValue(useTerminalShellStore.getState().preference);
			const result = await invokeMutation.mutateAsync({ cueId: cue.id, sessionId, shell });
			if (result.kind === "command" && !result.shellTerminal) throw new Error(t("cues.invokeFailed"));
			rememberLastRunCue(projectId, cue.id);
			if (result.kind === "command") {
				if (!result.shellTerminal) throw new Error(t("cues.invokeFailed"));
				const terminal = toShellTerminal(result.shellTerminal);
				if (!queryClient.getQueryData<ShellTerminal[]>(shellTerminalsQueryKey)?.some(
					(item) => item.handleId === terminal.handleId,
				)) {
					markTerminalHandleFresh(terminal.handleId);
				}
				queryClient.setQueryData<ShellTerminal[]>(shellTerminalsQueryKey, (current = []) => [
					terminal,
					...current.filter((item) => item.handleId !== terminal.handleId),
				]);
				// A delayed response must not redirect a different project or session.
				if (origin !== generation.current) return;
				setActiveShellTerminal(terminal.handleId);
				return;
			}
		} catch (error) {
			if (origin !== generation.current) return;
			showGlobalToast(t("cues.invokeFailed"), apiErrorMessage(error, t("cues.invokeFailed")), "error");
		} finally {
			pending.current = false;
			if (origin === generation.current) setInvokingId(null);
		}
	};

	// A pointer click repeats the project's last successfully dispatched cue.
	// Without a valid remembered cue, open the list for an explicit choice.
	const runLastCue = async () => {
		if (disabled || spinner || creating) return;
		const origin = generation.current;
		setRunningLastCue(true);
		try {
			const cues = await queryClient.fetchQuery({
				queryKey: projectCuesQueryKey(projectId),
				queryFn: () => fetchProjectCues(projectId),
				staleTime: 0,
			});
			if (origin !== generation.current) return;
			const lastCue = cues.find((cue) => cue.id === readLastRunCue(projectId));
			// Nothing to run: leave the hover menu up so its empty state can
			// offer creating the first cue.
			if (!lastCue) {
				openMenu();
				return;
			}
			await handleInvoke(lastCue);
		} catch (error) {
			if (origin !== generation.current) return;
			showGlobalToast(t("cues.loadFailed"), apiErrorMessage(error, t("cues.loadFailed")), { tone: "error", dedupeKey: `cues.load.${projectId}` });
		} finally {
			if (origin === generation.current) setRunningLastCue(false);
		}
	};

	return (
		<>
			<DropdownMenu modal={false} open={open} onOpenChange={handleOpenChange}>
				<DropdownMenuTrigger asChild>
					<TopbarButton
						ref={buttonRef}
						type="button"
						aria-label={t("cues.run")}
						// The menu is anchored to this button: pressing must not scale it,
						// or the open menu shifts with the trigger.
						className="topbar-control--static-press"
						data-priority="secondary"
						disabled={disabled || spinner}
						variant="icon"
						onPointerEnter={handlePointerEnter}
						onPointerLeave={handlePointerLeave}
						// Keyboard activation opens the menu (Radix); only pointer
						// presses run the last cue, so Enter/Space stay safe.
						onClick={(event) => {
							if (event.detail === 0) return;
							void runLastCue();
						}}
					>
						{spinner ? (
							<Loader2 className="size-icon-md animate-spin" aria-hidden="true" />
						) : (
							<Play className="size-icon-md" aria-hidden="true" />
						)}
					</TopbarButton>
				</DropdownMenuTrigger>

				{open ? (
					<CueMenuItems
						projectId={projectId}
						busy={spinner}
						onInvoke={handleInvoke}
						onCreate={() => {
							cancelHoverClose();
							pointerInsideControl.current = false;
							setOpen(false);
							setCreating(true);
						}}
						onPointerEnter={handleMenuPointerEnter}
						onPointerLeave={handlePointerLeave}
						onEscapeKeyDown={() => {
							escapeRequested.current = true;
						}}
					/>
				) : null}
			</DropdownMenu>
			<CreateCueDialog projectId={projectId} open={creating} onOpenChange={setCreating}
				onClosed={() => buttonRef.current?.focus()} />
		</>
	);
}

function CueMenuItems({
	projectId,
	busy,
	onInvoke,
	onCreate,
	onPointerEnter,
	onPointerLeave,
	onEscapeKeyDown,
}: {
	projectId: string;
	busy: boolean;
	onInvoke: (cue: CueDTO) => Promise<void>;
	onCreate: () => void;
	onPointerEnter: () => void;
	onPointerLeave: () => void;
	onEscapeKeyDown: () => void;
}) {
	const { t } = useTranslation();
	const query = useProjectCuesQuery(projectId);
	const showGlobalToast = useUiStore((state) => state.showGlobalToast);
	useEffect(() => {
		if (query.isError && !query.isFetching) {
			showGlobalToast(t("cues.loadFailed"), apiErrorMessage(query.error, t("cues.loadFailed")), { tone: "error", dedupeKey: `cues.load.${projectId}` });
		}
	}, [projectId, query.isError, query.isFetching, query.error, showGlobalToast, t]);
	// The menu refreshes on every open, and hover opens it constantly, so cached
	// cues render straight away: the background refresh that keeps them fresh must
	// not blank the list into a loading row first. Loading is only for a cold read.
	const loading = query.isFetching && query.data === undefined && !query.isError;
	const canInvoke = query.isFetchedAfterMount && !query.isFetching && !query.isError;
	return (
		<DropdownMenuContent
			align="end"
			side="bottom"
			onPointerEnter={onPointerEnter}
			onPointerLeave={onPointerLeave}
			onEscapeKeyDown={onEscapeKeyDown}
		>
			{query.isError && !query.isFetching ? (
				<>
					<p role="alert" className="px-2 py-1 text-sm text-destructive">
						{t("cues.loadFailed")}
					</p>
					<DropdownMenuItem
						onSelect={(event) => {
							event.preventDefault();
							void query.refetch();
						}}
					>
						{t("cues.retry")}
					</DropdownMenuItem>
				</>
			) : loading ? (
				<DropdownMenuItem disabled>{t("cues.loading")}</DropdownMenuItem>
			) : query.data?.length ? (
				query.data.map((cue) => <CueMenuItem key={cue.id} cue={cue} busy={busy || !canInvoke} onInvoke={onInvoke} />)
			) : (
				<DropdownMenuItem disabled>{t("cues.emptyMenu")}</DropdownMenuItem>
			)}
			<DropdownMenuSeparator />
			<DropdownMenuItem disabled={busy} onSelect={onCreate}
			>
				<Plus aria-hidden="true" />
				{t("cues.newCue")}
			</DropdownMenuItem>
		</DropdownMenuContent>
	);
}

function CueMenuItem({ cue, busy, onInvoke }: {
	cue: CueDTO;
	busy: boolean;
	onInvoke: (cue: CueDTO) => Promise<void>;
}) {
	return (
		<DropdownMenuItem disabled={busy} onSelect={() => void onInvoke(cue)}>
			{cue.type === "agent" ? <MessageSquare aria-hidden="true" /> : <TerminalSquare aria-hidden="true" />}
			<span className="min-w-0 flex-1 truncate">{cue.name}</span>
		</DropdownMenuItem>
	);
}
