import { cn } from "@/lib/utils";
import { connectorIconPaths } from "./connectorIcons";

export function ConnectorIcon({ presetId, name, className }: { presetId?: string; name: string; className?: string }) {
	const path = presetId ? connectorIconPaths[presetId] : undefined;
	if (path) {
		return (
			<svg viewBox="0 0 24 24" fill="currentColor" aria-hidden="true" className={cn("size-icon-lg shrink-0 text-settings-label", className)}>
				<path d={path} />
			</svg>
		);
	}
	return (
		<span aria-hidden="true" className={cn("flex size-icon-lg shrink-0 items-center justify-center rounded-sm bg-foreground/10 text-caption font-medium leading-none text-settings-label", className)}>
			{name.trim().charAt(0).toUpperCase()}
		</span>
	);
}
