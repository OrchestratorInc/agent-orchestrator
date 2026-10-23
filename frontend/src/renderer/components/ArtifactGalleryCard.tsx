import { BrowserIcon } from "./icons";
import { File, FileText, Loader2 } from "lucide-react";
import type { ReactNode } from "react";
import { useTranslation } from "react-i18next";
import type { ArtifactRow } from "../lib/artifacts";
import { formatTimeCompact } from "../lib/format-time";
import { ArtifactThumbnail } from "./ArtifactThumbnail";

export function ArtifactGalleryCard({ row, context, onPreview, actions, previewLive = true }: {
	row: ArtifactRow;
	context: string;
	onPreview: () => void;
	actions: ReactNode;
	previewLive?: boolean;
}) {
	const { t } = useTranslation();
	const KindIcon = row.kind === "html" ? BrowserIcon : row.kind === "markdown" ? FileText : File;
	return (
		<li className="group relative min-w-0 rounded-md bg-card ring-1 ring-foreground/10">
			<span className="pointer-events-none absolute inset-x-0 top-0 z-[1] block aspect-[16/9] overflow-hidden rounded-t-md border-b border-border">
				<ArtifactThumbnail key={`${row.rawUrl}:${row.inlineUrl}:${row.updatedAt}`} row={row} previewLive={previewLive} />
			</span>
			<button
				type="button"
				className="block w-full overflow-hidden rounded-md text-left transition-none hover:bg-accent focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring/60"
				onClick={onPreview}
				title={`${row.sessionTitle} · ${row.path}`}
			>
				<span className="relative block aspect-[16/9] border-b border-border bg-[var(--solid-muted,var(--muted))]" />
				<span className="block space-y-1 px-3 py-2.5">
					<span className="flex min-w-0 items-center gap-1.5 text-control font-medium leading-5 text-foreground">
						{row.generating ? (
							<Loader2 aria-label={t("artifacts.generating")} className="size-icon-sm shrink-0 animate-spin text-status-working motion-reduce:animate-none" />
						) : <KindIcon aria-hidden="true" className="size-icon-sm shrink-0 text-muted-foreground" />}
						<span className="truncate">{row.name}</span>
					</span>
					<span className="flex min-w-0 items-center gap-1.5 text-caption leading-4 text-muted-foreground">
						{context ? <><span className="truncate">{context}</span><span aria-hidden="true">·</span></> : null}
						<time dateTime={row.updatedAt} className="shrink-0 tabular-nums">{formatTimeCompact(row.updatedAt)}</time>
					</span>
				</span>
			</button>
			<div className="absolute right-2 top-2 z-[2] flex items-center gap-1">{actions}</div>
		</li>
	);
}
