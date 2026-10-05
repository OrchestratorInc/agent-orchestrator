import type { ServerConfig } from "../config";
import type { SourceRef } from "./scopedBoard";

/** Resolve a Local action through that host's verified pairing, never the selected host. */
export function localConfigForSource(
	source: SourceRef,
	configForHost: (hostId: string) => ServerConfig | null,
): ServerConfig | null {
	if (source.kind !== "local") return null;
	const config = configForHost(source.id);
	return config?.hostId === source.id ? config : null;
}
