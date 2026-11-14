import { useQuery } from "@tanstack/react-query";
import { apiClient } from "../lib/api-client";
import { aoBridge } from "../lib/bridge";
import type { MigrationState } from "../../main/app-state";

export const migrationOfferQueryKey = ["migration-offer"] as const;
const usePreviewData = import.meta.env.VITE_NO_ELECTRON === "1";

export interface MigrationOffer {
	show: boolean;
	legacyRoot: string;
	migration: MigrationState;
}

async function fetchMigrationOffer(): Promise<MigrationOffer> {
	const migration = await aoBridge.appState.getMigration();
	if (migration.status === "completed" || migration.status === "declined") {
		return { show: false, legacyRoot: "", migration };
	}
	const { data, error } = await apiClient.GET("/api/v1/import");
	const legacyRoot = data?.legacyRoot ?? "";
	if (error || !data?.available) return { show: false, legacyRoot, migration };
	return { show: true, legacyRoot, migration };
}

export function useMigrationOffer() {
	return useQuery({
		queryKey: migrationOfferQueryKey,
		queryFn: fetchMigrationOffer,
		enabled: !usePreviewData,
		retry: 1,
		throwOnError: false,
	});
}
