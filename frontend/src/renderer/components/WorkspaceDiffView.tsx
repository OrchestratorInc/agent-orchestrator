import {
	memo,
	useCallback,
	useEffect,
	useLayoutEffect,
	useMemo,
	useRef,
	useState,
	type ReactNode,
	type RefObject,
} from "react";
import { useVirtualizer } from "@tanstack/react-virtual";
import type { TFunction } from "i18next";
import { useTranslation } from "react-i18next";
import { Check, LoaderCircle, Plus, Send as SendIcon, type AppIcon } from "@/components/app-icons";
import type { FileAnnotationTarget } from "../../shared/file-annotations";
import {
	type WorkspaceCompareMode,
	type WorkspaceFileDetail,
	type WorkspaceFileSummary,
} from "../hooks/useSessionWorkspaceFiles";
import { useParsedDiff } from "../hooks/useParsedDiff";
import { useDiffHighlight } from "../hooks/useDiffHighlight";
import { aoBridge } from "../lib/bridge";
import { cn } from "../lib/utils";
import { statusLabel, statusTone } from "../lib/workspace-file-status";
import type { DiffRow, DiffRowKind } from "../lib/diff-parser";
import type { DiffRun } from "../lib/diff-highlight";
import { Button } from "./ui/button";
import { ImageDiffView } from "./ImageDiffView";
import "./chat/code-theme.css";

type WorkspaceFileStatus = WorkspaceFileSummary["status"];

export type ActiveFileAnnotationTarget = FileAnnotationTarget & {
	rowIndex?: number;
	surface?: "focused" | "review";
};
export type FileAnnotationStatus = "idle" | "sending" | "sent" | "error";
export type FileAnnotationModel = {
	targets: ActiveFileAnnotationTarget[];
	status: FileAnnotationStatus;
	statusFor: (target: ActiveFileAnnotationTarget) => FileAnnotationStatus;
	error: string;
	begin: (target: ActiveFileAnnotationTarget) => void;
	draftFor: (target: ActiveFileAnnotationTarget) => string;
	setDraft: (target: ActiveFileAnnotationTarget, draft: string) => void;
	cancel: (target?: ActiveFileAnnotationTarget) => void;
	submit: (target?: ActiveFileAnnotationTarget, text?: string) => Promise<void>;
};

export function cancelFileAnnotations(annotation: FileAnnotationModel, match: (target: ActiveFileAnnotationTarget) => boolean): void {
	for (const target of annotation.targets.filter(match)) annotation.cancel(target);
}

export function fileAnnotationKey(target: ActiveFileAnnotationTarget): string {
	return [target.surface ?? "", target.scope ?? "", target.side, target.line ?? "", target.path, target.quote ?? ""].join("\u0000");
}

export function canSplitCompare(status: WorkspaceFileStatus): boolean {
	return status === "modified" || status === "renamed";
}


function emptyDiffMessage(compareMode: WorkspaceCompareMode | undefined, t: TFunction): string {
	return compareMode === "base" ? t("files.noChangesBase") : t("files.noChangesHead");
}


export function ReviewDiffBody({
	annotation,
	detail,
	detailLoadedAt,
	emptyFallback,
	filePath,
	onActiveSelectionChange,
	sessionId,
	hostId,
	split,
	wrap,
}: {
	annotation: FileAnnotationModel;
	detail: WorkspaceFileDetail;
	detailLoadedAt: number;
	emptyFallback?: ReactNode;
	filePath: string;
	onActiveSelectionChange: (active: boolean) => void;
	sessionId: string;
	hostId?: string;
	split: boolean;
	wrap: boolean;
}) {
	const { t } = useTranslation();
	void filePath;
	const { rows, pending } = useParsedDiff(detail.diff);
	if (detail.imageMediaType) {
		return (
			<ImageDiffView
				path={detail.path}
				sessionId={sessionId}
				hostId={hostId}
				split={split}
				status={detail.status}
				version={detailLoadedAt}
			/>
		);
	}
	if (detail.binary) {
		return <PanelMessage compact>{t("files.binaryUnavailable")}</PanelMessage>;
	}
	if (pending) {
		return <PanelMessage compact>{t("files.loadingDiff")}</PanelMessage>;
	}
	if (rows.length === 0) {
		return emptyFallback ?? <PanelMessage compact>{emptyDiffMessage(detail.compareMode, t)}</PanelMessage>;
	}
	return (
		<DiffView
			annotation={annotation}
			onActiveSelectionChange={onActiveSelectionChange}
			path={detail.path}
			previousPath={detail.previousPath}
			rows={rows}
			split={split}
			truncated={detail.diffTruncated}
			wrap={wrap}
		/>
	);
}

const diffRowTone: Record<Exclude<DiffRowKind, "hunk">, string> = {
	add: "bg-success/10",
	del: "bg-error/10",
	context: "",
};

const diffMarkerGlyph: Record<Exclude<DiffRowKind, "hunk">, string> = {
	add: "+",
	del: "-",
	context: " ",
};

function isSelectionActiveIn(selection: Selection | null, container: HTMLElement | null): boolean {
	if (!selection || !container || selection.isCollapsed) return false;
	return selection.anchorNode !== null && container.contains(selection.anchorNode);
}

const ROW_VIRTUALIZE_THRESHOLD = 150;
const ESTIMATED_ROW_HEIGHT = 20;

function useSharedScrollRowVirtualizer(
	scrollContainerRef: RefObject<HTMLElement | null>,
	count: number,
	enabled: boolean,
) {
	const listRef = useRef<HTMLDivElement>(null);
	const [scrollElement, setScrollElement] = useState<HTMLElement | null>(null);
	const [scrollMargin, setScrollMargin] = useState(0);

	useEffect(() => {
		if (!enabled) return;
		setScrollElement(scrollContainerRef.current?.closest<HTMLElement>("[data-files-scroll-root]") ?? null);
	}, [enabled, scrollContainerRef]);

	useEffect(() => {
		if (!enabled || !scrollElement || !listRef.current) return;
		const measure = () => {
			if (!listRef.current) return;
			const listRect = listRef.current.getBoundingClientRect();
			const scrollRect = scrollElement.getBoundingClientRect();
			setScrollMargin(listRect.top - scrollRect.top + scrollElement.scrollTop);
		};
		measure();
		const resizeTarget = scrollElement.querySelector<HTMLElement>(".session-files-review-list") ?? scrollElement;
		const observer = new ResizeObserver(measure);
		observer.observe(resizeTarget);
		return () => observer.disconnect();
	}, [enabled, scrollElement]);

	const virtualizer = useVirtualizer({
		count,
		getScrollElement: () => (enabled ? scrollElement : null),
		estimateSize: () => ESTIMATED_ROW_HEIGHT,
		overscan: 8,
		scrollMargin,
	});

	return { listRef, virtualizer };
}

function DiffView({
	annotation,
	onActiveSelectionChange,
	path,
	previousPath,
	rows,
	split,
	truncated,
	wrap,
}: {
	annotation: FileAnnotationModel;
	onActiveSelectionChange: (active: boolean) => void;
	path: string;
	previousPath?: string;
	rows: DiffRow[];
	split: boolean;
	truncated?: boolean;
	wrap: boolean;
}) {
	const { t } = useTranslation();
	const containerRef = useRef<HTMLDivElement>(null);
	const [hasSelection, setHasSelection] = useState(false);
	const shouldVirtualize = !split && rows.length > ROW_VIRTUALIZE_THRESHOLD;
	const { listRef, virtualizer } = useSharedScrollRowVirtualizer(containerRef, rows.length, shouldVirtualize);
	const highlight = useDiffHighlight(rows, path, previousPath);
	const runs = useMemo(
		() => rows.map((row, index) => (row.kind === "del" ? highlight.oldSide[index] : highlight.newSide[index])),
		[rows, highlight],
	);

	useEffect(() => {
		const onSelectionChange = () => {
			setHasSelection(isSelectionActiveIn(window.getSelection(), containerRef.current));
		};
		document.addEventListener("selectionchange", onSelectionChange);
		return () => document.removeEventListener("selectionchange", onSelectionChange);
	}, []);

	useEffect(() => {
		onActiveSelectionChange(hasSelection);
	}, [hasSelection, onActiveSelectionChange]);

	return (
		<div>
			{truncated ? (
				<div className="shrink-0 border-b border-border bg-warning/10 px-3 py-1.5 text-xs text-warning">
					{t("files.diffTruncated")}
				</div>
			) : null}
			<div
				className="diff-code session-files-diff-scrollbar select-text overflow-x-auto overflow-y-visible bg-terminal font-mono text-xs leading-row text-terminal-foreground"
				ref={containerRef}
			>
				{split ? (
					<SplitDiff
						annotation={annotation}
						newRuns={highlight.newSide}
						oldRuns={highlight.oldSide}
						path={path}
						previousPath={previousPath}
						rows={rows}
						t={t}
					/>
				) : shouldVirtualize ? (
					<div
						className={cn("relative", !wrap && "min-w-max")}
						ref={listRef}
						style={{ height: virtualizer.getTotalSize() }}
					>
						{virtualizer.getVirtualItems().map((virtualRow) => {
							const row = rows[virtualRow.index];
							return (
								<div
									data-index={virtualRow.index}
									key={virtualRow.index}
									ref={virtualizer.measureElement}
									style={{
										position: "absolute",
										top: 0,
										left: 0,
										width: "100%",
										transform: `translateY(${virtualRow.start - virtualizer.options.scrollMargin}px)`,
									}}
								>
									<DiffRowContent
										annotation={annotation}
										index={virtualRow.index}
										path={path}
										previousPath={previousPath}
										row={row}
										runs={runs[virtualRow.index]}
										t={t}
										wrap={wrap}
									/>
								</div>
							);
						})}
					</div>
				) : (
					<div className={cn(!wrap && "min-w-max")}>
						{rows.map((row, index) => (
							<DiffRowContent
								annotation={annotation}
								index={index}
								key={index}
								path={path}
								previousPath={previousPath}
								row={row}
								runs={runs[index]}
								t={t}
								wrap={wrap}
							/>
						))}
					</div>
				)}
			</div>
		</div>
	);
}

const HunkBand = memo(function HunkBand({ row }: { row: DiffRow }) {
	return (
		<div className="flex select-none items-baseline gap-2 bg-surface-faint px-2.5 py-0.75 text-passive">
			<span className="shrink-0 text-passive/70">{row.text}</span>
			{row.section ? <span className="min-w-0 truncate text-passive/90">{row.section}</span> : null}
		</div>
	);
});

type DiffRowContentProps = {
	annotation: FileAnnotationModel;
	index: number;
	path: string;
	previousPath?: string;
	row: DiffRow;
	runs: DiffRun[];
	t: TFunction;
	wrap: boolean;
};

function DiffRowContentInner({ annotation, index, path, previousPath, row, runs, t, wrap }: DiffRowContentProps) {
	if (row.kind === "hunk") return <HunkBand row={row} />;
	return (
		<div>
			<div
				className={cn("group/line relative flex", diffRowTone[row.kind])}
				data-diff-row=""
				data-kind={row.kind}
				data-new-no={row.newNo ?? ""}
				data-old-no={row.oldNo ?? ""}
				data-row-index={index}
			>
				<LineFeedbackButton
					active={rowAnnotationTargets(annotation.targets, path, index).length > 0}
					onClick={() => annotation.begin(lineAnnotationTarget(path, previousPath, row, index))}
					t={t}
					target={lineAnnotationTarget(path, previousPath, row, index)}
				/>
				<span className="w-9 shrink-0 select-none border-r border-border/50 bg-terminal px-1.5 text-right text-passive/70 tabular-nums">
					{row.newNo ?? row.oldNo ?? ""}
				</span>
				<span
					className={cn(
						"w-4 shrink-0 select-none text-center",
						row.kind === "add" && "text-success",
						row.kind === "del" && "text-error",
					)}
				>
					{diffMarkerGlyph[row.kind]}
				</span>
				<span className={cn("pr-3", wrap ? "whitespace-pre-wrap break-all" : "whitespace-pre")}>
					{renderDiffRuns(runs, row.kind === "add")}
				</span>
			</div>
			{rowAnnotationTargets(annotation.targets, path, index).map((target) => (
				<FileAnnotationComposer annotation={annotation} key={fileAnnotationKey(target)} target={target} />
			))}
		</div>
	);
}

const DiffRowContent = memo(DiffRowContentInner, (prev, next) => {
	if (
		prev.row !== next.row ||
		prev.index !== next.index ||
		prev.path !== next.path ||
		prev.previousPath !== next.previousPath ||
		prev.runs !== next.runs ||
		prev.wrap !== next.wrap
	) {
		return false;
	}
	const prevActive = rowAnnotationTargets(prev.annotation.targets, prev.path, prev.index).length > 0;
	const nextActive = rowAnnotationTargets(next.annotation.targets, next.path, next.index).length > 0;
	return !prevActive && !nextActive;
});

type SplitRow =
	| { kind: "hunk"; row: DiffRow }
	| { kind: "pair"; left: DiffRow | null; leftIndex: number | null; right: DiffRow | null; rightIndex: number | null };

function toSplitRows(rows: DiffRow[]): SplitRow[] {
	const out: SplitRow[] = [];
	let dels: Array<{ row: DiffRow; index: number }> = [];
	let adds: Array<{ row: DiffRow; index: number }> = [];
	const flush = () => {
		const count = Math.max(dels.length, adds.length);
		for (let i = 0; i < count; i += 1) {
			out.push({
				kind: "pair",
				left: dels[i]?.row ?? null,
				leftIndex: dels[i]?.index ?? null,
				right: adds[i]?.row ?? null,
				rightIndex: adds[i]?.index ?? null,
			});
		}
		dels = [];
		adds = [];
	};
	rows.forEach((row, index) => {
		if (row.kind === "del") {
			dels.push({ row, index });
			return;
		}
		if (row.kind === "add") {
			adds.push({ row, index });
			return;
		}
		flush();
		if (row.kind === "hunk") out.push({ kind: "hunk", row });
		else out.push({ kind: "pair", left: row, leftIndex: index, right: row, rightIndex: index });
	});
	flush();
	return out;
}

function SplitDiff({
	annotation,
	newRuns,
	oldRuns,
	path,
	previousPath,
	rows,
	t,
}: {
	annotation: FileAnnotationModel;
	newRuns: DiffRun[][];
	oldRuns: DiffRun[][];
	path: string;
	previousPath?: string;
	rows: DiffRow[];
	t: TFunction;
}) {
	const splitRows = useMemo(() => toSplitRows(rows), [rows]);
	return (
		<div>
			{splitRows.map((splitRow, index) =>
				splitRow.kind === "hunk" ? (
					<HunkBand key={`sh${index}`} row={splitRow.row} />
				) : (
					<div key={`sp${index}`}>
						<div className="grid grid-cols-2 divide-x divide-border/40">
							<SplitSide
								annotation={annotation}
								path={path}
								previousPath={previousPath}
								row={splitRow.left}
								rowIndex={splitRow.leftIndex}
								runs={splitRow.leftIndex === null ? null : oldRuns[splitRow.leftIndex]}
								side="old"
								t={t}
							/>
							<SplitSide
								annotation={annotation}
								path={path}
								previousPath={previousPath}
								row={splitRow.right}
								rowIndex={splitRow.rightIndex}
								runs={splitRow.rightIndex === null ? null : newRuns[splitRow.rightIndex]}
								side="new"
								t={t}
							/>
						</div>
						{[splitRow.leftIndex, splitRow.leftIndex === splitRow.rightIndex ? null : splitRow.rightIndex].flatMap((rowIndex) =>
							rowIndex === null ? [] : rowAnnotationTargets(annotation.targets, path, rowIndex),
						).map((target) => (
							<FileAnnotationComposer annotation={annotation} key={fileAnnotationKey(target)} target={target} />
						))}
					</div>
				),
			)}
		</div>
	);
}

function SplitSide({
	annotation,
	path,
	previousPath,
	row,
	rowIndex,
	runs,
	side,
	t,
}: {
	annotation: FileAnnotationModel;
	path: string;
	previousPath?: string;
	row: DiffRow | null;
	rowIndex: number | null;
	runs: DiffRun[] | null;
	side: "old" | "new";
	t: TFunction;
}) {
	if (!row || rowIndex === null || !runs) return <div className="bg-surface-faint/20" aria-hidden="true" />;
	const lineNo = side === "old" ? row.oldNo : row.newNo;
	const tone = row.kind === "hunk" ? "" : diffRowTone[row.kind];
	const target = lineNo == null ? null : lineAnnotationTarget(path, previousPath, row, rowIndex, side);
	return (
		<div
			className={cn("group/line relative flex min-w-0", tone)}
			data-diff-row=""
			data-kind={row.kind}
			data-new-no={row.newNo ?? ""}
			data-old-no={row.oldNo ?? ""}
			data-row-index={rowIndex}
		>
			{target ? (
				<LineFeedbackButton
					active={rowAnnotationTargets(annotation.targets, path, rowIndex).some((open) => open.side === side)}
					onClick={() => annotation.begin(target)}
					t={t}
					target={target}
				/>
			) : null}
			<span className="w-9 shrink-0 select-none border-r border-border/50 bg-terminal px-1.5 text-right text-passive/70 tabular-nums">
				{lineNo ?? ""}
			</span>
			<span className="min-w-0 whitespace-pre-wrap break-all px-1.5">{renderDiffRuns(runs, row.kind === "add")}</span>
		</div>
	);
}

function lineAnnotationTarget(
	path: string,
	previousPath: string | undefined,
	row: DiffRow,
	rowIndex: number,
	side: "old" | "new" = row.kind === "del" ? "old" : "new",
): ActiveFileAnnotationTarget {
	return {
		path,
		previousPath,
		side,
		line: side === "old" ? (row.oldNo ?? undefined) : (row.newNo ?? undefined),
		oldLine: row.oldNo ?? undefined,
		newLine: row.newNo ?? undefined,
		lineKind: row.kind === "hunk" ? undefined : row.kind,
		lineText: row.text,
		rowIndex,
		surface: "focused",
	};
}

function rowAnnotationTargets(targets: ActiveFileAnnotationTarget[], path: string, rowIndex: number): ActiveFileAnnotationTarget[] {
	return targets.filter((target) => target.surface !== "review" && target.path === path && target.side !== "file" && target.rowIndex === rowIndex);
}

function LineFeedbackButton({
	active,
	onClick,
	t,
	target,
}: {
	active: boolean;
	onClick: () => void;
	t: TFunction;
	target: ActiveFileAnnotationTarget;
}) {
	const side = t(target.side === "old" ? "files.oldSide" : "files.newSide");
	const label = t("files.addLineFeedback", { file: target.path, line: target.line, side });
	return <LineFeedbackButtonControl expanded={active} label={label} onClick={onClick} />;
}

export function LineFeedbackButtonControl({
	expanded = false,
	gutter = false,
	label,
	onClick,
}: {
	expanded?: boolean;
	gutter?: boolean;
	label: string;
	onClick: () => void;
}) {
	return (
		<Button
			aria-label={label}
			aria-expanded={expanded || undefined}
			className={cn(
				"z-20 size-6 rounded-sm border-primary/70 shadow-md shadow-black/30",
				gutter
					? "relative size-5"
					: "absolute inset-y-0 left-6 my-auto opacity-0 transition-opacity focus-visible:opacity-100 group-hover/line:opacity-100",
			)}
			data-utility-button={gutter ? "" : undefined}
			onClick={(event) => {
				event.stopPropagation();
				onClick();
			}}
			size={null}
			type="button"
			variant="primary"
		>
			<Plus className="size-4" aria-hidden="true" />
		</Button>
	);
}

const COMPOSER_MAX_HEIGHT_PX = 160;

export function FileAnnotationComposer({ annotation, target }: { annotation: FileAnnotationModel; target: ActiveFileAnnotationTarget }) {
	const { t } = useTranslation();
	const textareaRef = useRef<HTMLTextAreaElement>(null);
	const [text, setText] = useState(() => annotation.draftFor(target));
	const textRef = useRef(text);
	textRef.current = text;
	const saveDraftRef = useRef(() => annotation.setDraft(target, textRef.current));
	saveDraftRef.current = () => annotation.setDraft(target, textRef.current);
	useEffect(() => () => saveDraftRef.current(), []);
	const newest = annotation.targets[annotation.targets.length - 1] === target;
	useEffect(() => {
		if (!newest) return;
		const frame = window.requestAnimationFrame(() => textareaRef.current?.focus({ preventScroll: true }));
		return () => window.cancelAnimationFrame(frame);
	}, [newest, target]);
	const fitHeight = useCallback(() => {
		const textarea = textareaRef.current;
		if (!textarea) return;
		textarea.style.height = "";
		if (!textarea.value) return;
		textarea.style.height = `${Math.min(textarea.scrollHeight, COMPOSER_MAX_HEIGHT_PX)}px`;
	}, []);
	useLayoutEffect(fitHeight, [fitHeight, text, target]);
	useEffect(() => {
		const textarea = textareaRef.current;
		if (!textarea || typeof ResizeObserver === "undefined") return;
		let width = textarea.clientWidth;
		const observer = new ResizeObserver(() => {
			if (textarea.clientWidth === width) return;
			width = textarea.clientWidth;
			fitHeight();
		});
		observer.observe(textarea);
		return () => observer.disconnect();
	}, [fitHeight, target]);
	const side = target.side === "file" ? "" : t(target.side === "old" ? "files.oldSide" : "files.newSide");
	const targetLabel =
		target.quote != null
			? t("files.selectionFeedbackTarget", { file: target.path })
			: target.side === "file"
			? t("files.fileFeedbackTarget", { file: target.path })
			: t("files.lineFeedbackTarget", { file: target.path, line: target.line, side });
	const quoteToCopy = (box: HTMLTextAreaElement) => (box.selectionStart === box.selectionEnd ? target.quote : undefined);
	const status = annotation.statusFor(target);
	const sending = status === "sending";
	const sent = status === "sent";
	const submit = () => {
		if (!text.trim() || sending || sent) return;
		void annotation.submit(target, text);
	};
	const writtenCount = annotation.targets.filter((open) => (open === target ? text : annotation.draftFor(open)).trim()).length;
	const submitAll = () => {
		if (sending || sent) return;
		annotation.setDraft(target, text);
		void annotation.submit();
	};

	return (
		<div className="p-2 font-sans">
			<form
				className="rounded-2xl border border-border bg-background px-2.5 py-2"
				onSubmit={(event) => {
					event.preventDefault();
					submit();
				}}
			>
				<textarea
					aria-label={t("files.feedbackLabel", { target: targetLabel })}
					className={cn(
						"board-scrollbar block min-h-7 w-full resize-none border-0 bg-transparent py-1 text-[13px] leading-5 text-foreground outline-none placeholder:text-muted-foreground disabled:opacity-60",
						text ? "overflow-y-auto" : "overflow-hidden",
					)}
					disabled={sending || sent}
					onBlur={() => annotation.setDraft(target, text)}
					onCopy={(event) => {
						const quote = quoteToCopy(event.currentTarget);
						if (quote === undefined) return;
						event.preventDefault();
						event.clipboardData.setData("text/plain", quote);
					}}
					onChange={(event) => {
						const next = event.target.value;
						setText(next);
						if (Boolean(next.trim()) !== Boolean(text.trim())) annotation.setDraft(target, next);
					}}
					onKeyDown={(event) => {
						if (event.key === "Escape") {
							event.preventDefault();
							annotation.cancel(target);
						} else if (event.key === "Enter" && (event.metaKey || event.ctrlKey) && !event.nativeEvent.isComposing) {
							event.preventDefault();
							submitAll();
						} else if (event.key.toLowerCase() === "c" && (event.metaKey || event.ctrlKey) && !event.altKey && !event.shiftKey) {
							const quote = quoteToCopy(event.currentTarget);
							if (quote === undefined) return;
							event.preventDefault();
							void aoBridge.clipboard.writeText(quote);
						}
					}}
					placeholder={t("files.feedbackPlaceholder")}
					ref={textareaRef}
					rows={1}
					title={targetLabel}
					value={text}
				/>
				{status === "error" ? (
					<p className="pt-1 text-xs text-error" role="alert">
						{annotation.error}
					</p>
				) : null}
				<div className="mt-1 flex items-center justify-end gap-1">
					<span className="mr-auto min-w-0 truncate text-caption text-passive">{writtenCount > 1 ? t("files.feedbackShortcutAll") : t("files.feedbackShortcut")}</span>
					<Button
						className="px-2"
						disabled={sending}
						onClick={() => annotation.cancel(target)}
						size="sm"
						type="button"
						variant="quiet"
					>
						{t("files.cancelFeedback")}
					</Button>
					<Button
						aria-label={sent ? t("files.feedbackSent") : t("files.sendFeedback")}
						className="disabled:opacity-100"
						disabled={!text.trim() || sending || sent}
						size="icon-sm"
						title={sent ? t("files.feedbackSent") : t("files.sendFeedback")}
						type="submit"
						variant="quiet"
					>
						{sending ? <LoaderCircle aria-hidden="true" className="animate-spin" /> : sent ? <Check aria-hidden="true" className="text-success" /> : <SendIcon aria-hidden="true" className={cn(!text.trim() && "opacity-50")} />}
					</Button>
				</div>
			</form>
		</div>
	);
}

export function FileAnnotationSendBar({ annotation, className, surface }: { annotation: FileAnnotationModel; className?: string; surface: "focused" | "review" }) {
	const { t } = useTranslation();
	const written = annotation.targets.filter((target) => annotation.draftFor(target).trim());
	const here = written.some((target) => (surface === "review" ? target.surface !== "focused" : target.surface !== "review"));
	if (!here || written.length < 2) return null;
	const sending = annotation.status === "sending";
	const sent = annotation.status === "sent";
	return (
		<div className={cn("flex min-h-9 shrink-0 items-center gap-1 border-t border-border bg-background px-3 py-1 font-sans", className)} data-testid="file-feedback-bar">
			<span className="mr-auto min-w-0 truncate text-xs text-muted-foreground" role="status">
				{annotation.status === "error" ? <span className="text-error">{annotation.error}</span> : sent ? t("files.feedbackSent") : null}
			</span>
			<Button className="px-2" disabled={sending || sent} onClick={() => annotation.cancel()} size="sm" type="button" variant="destructive-ghost">
				{t("files.discardFeedback")}
			</Button>
			<Button className="px-2" disabled={sending || sent} onClick={() => void annotation.submit()} size="sm" type="button" variant="primary">
				{sending ? <LoaderCircle aria-hidden="true" className="animate-spin" /> : sent ? <Check aria-hidden="true" /> : <SendIcon aria-hidden="true" />}
				{t("files.sendAllFeedback", { count: written.length })}
			</Button>
		</div>
	);
}

function renderDiffRuns(runs: DiffRun[], add: boolean): ReactNode {
	return runs.map((run, index) =>
		run.changed ? (
			<span className={cn(run.className, "rounded-sm", add ? "bg-success/35" : "bg-error/35")} key={index}>
				{run.text}
			</span>
		) : run.className ? (
			<span className={run.className} key={index}>
				{run.text}
			</span>
		) : (
			run.text
		),
	);
}

export function ChangeBadges({ additions, deletions }: { additions: number; deletions: number }) {
	return (
		<span className="flex shrink-0 items-center gap-1 font-mono text-caption font-medium">
			{additions > 0 ? <span className="px-0.5 text-success">+{additions}</span> : null}
			{deletions > 0 ? <span className="px-0.5 text-error">-{deletions}</span> : null}
		</span>
	);
}

export function PanelMessage({ action, children, compact = false }: { action?: ReactNode; children: ReactNode; compact?: boolean }) {
	return (
		<div
			className={cn(
				"grid place-items-center text-center text-xs text-muted-foreground",
				compact ? "min-h-16 p-3" : "min-h-[180px] p-6",
			)}
		>
			<div className="flex max-w-sm flex-col items-center gap-3">
				<p>{children}</p>
				{action ?? null}
			</div>
		</div>
	);
}

export function EmptyMessage({ icon: Icon, title, action }: { icon: AppIcon; title: ReactNode; action?: ReactNode }) {
	return (
		<div className="flex min-h-[180px] flex-1 flex-col items-center justify-center gap-3 p-6 pb-[15vh] text-center" data-testid="empty-message">
			<span aria-hidden="true" className="flex size-10 items-center justify-center rounded-lg bg-interactive-hover text-muted-foreground">
				<Icon className="size-5" />
			</span>
			<p className="max-w-xs text-heading-sm font-medium text-foreground">{title}</p>
			{action ?? null}
		</div>
	);
}

export function RetryButton({ onClick }: { onClick: () => void }) {
	const { t } = useTranslation();
	return (
		<Button onClick={onClick} size="sm" type="button" variant="secondary">
			{t("files.retry")}
		</Button>
	);
}

export function StatusMark({ status }: { status: WorkspaceFileStatus }) {
	const { t } = useTranslation();
	const label = statusLabel[status];
	return (
		<span
			className={cn(
				"inline-flex w-5 shrink-0 items-center justify-center font-mono text-caption font-medium",
				statusTone[status],
			)}
			title={t(`files.status.${status}`)}
		>
			{label}
		</span>
	);
}
