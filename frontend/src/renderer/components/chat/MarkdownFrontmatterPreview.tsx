import { useQuery } from "@tanstack/react-query";
import { HoverCardContent } from "../ui/hover-card";
import { Skeleton } from "../ui/skeleton";
import { useCloudCp } from "../../hooks/useCloudCp";
import { clampPreviewText, parseMarkdownFrontmatter } from "../../lib/markdown-frontmatter";
import { loadMarkdownPreviewText, type MarkdownFilePreviewSource } from "../../lib/markdown-file-preview";

export function MarkdownFrontmatterPreview({ path, source }: { path: string; source: MarkdownFilePreviewSource }) {
	const cloud = useCloudCp(source.kind === "cloud");
	const query = useQuery({
		queryKey: source.kind === "cloud"
			? ["markdown-frontmatter", "cloud", cloud.baseUrl, source.orgId, source.sessionId, path] as const
			: ["markdown-frontmatter", "workspace", source.hostId ?? "", source.sessionId, path] as const,
		enabled: source.kind !== "cloud" || cloud.ready,
		retry: false,
		staleTime: 30_000,
		queryFn: () => loadMarkdownPreviewText(source, path, { client: cloud.client, ready: cloud.ready }),
	});
	if (source.kind === "cloud" && !cloud.ready) return <UnavailableCard />;
	if (query.isPending) return <LoadingCard />;
	if (query.isError || query.data == null) return <UnavailableCard />;
	const meta = parseMarkdownFrontmatter(query.data);
	if (meta.status === "malformed") return <UnavailableCard message="This file's frontmatter could not be read." />;
	const name = meta.name ? clampPreviewText(meta.name) : undefined;
	const description = meta.description ? clampPreviewText(meta.description) : undefined;
	if (!name && !description) return <UnavailableCard message="No name or description." />;
	return (
		<HoverCardContent collisionPadding={8} sideOffset={6} className="max-h-48 overflow-y-auto p-3">
			<div className="space-y-1" role="status" aria-label={[name, description].filter(Boolean).join(". ")}>
				{name ? <p className="truncate text-sm font-semibold text-foreground">{name}</p> : null}
				{description ? <p className="line-clamp-4 break-words text-xs leading-4 text-muted-foreground">{description}</p> : null}
			</div>
		</HoverCardContent>
	);
}

function LoadingCard() {
	return (
		<HoverCardContent collisionPadding={8} sideOffset={6} className="max-h-48 overflow-y-auto p-3">
			<div role="status" aria-label="Loading file preview" className="space-y-2">
				<Skeleton className="h-3 w-32" />
				<Skeleton className="h-2.5 w-44" />
			</div>
		</HoverCardContent>
	);
}

function UnavailableCard({ message = "Unable to load this file." }: { message?: string }) {
	return (
		<HoverCardContent collisionPadding={8} sideOffset={6} className="max-h-48 overflow-y-auto p-3">
			<p role="status" className="text-sm font-medium text-foreground">{message}</p>
		</HoverCardContent>
	);
}
