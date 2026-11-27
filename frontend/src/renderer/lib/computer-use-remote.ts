import type { components } from "../../api/schema";
import { apiClient } from "./api-client";
import { apiErrorException } from "./api-error-exception";

export type ComputerUseRemoteSettings = components["schemas"]["ComputerUseRemoteSettings"];
export type ComputerUseRemoteActivity = components["schemas"]["ComputerUseRemoteActivity"];

/** Both remote computer-use settings and who is using this computer now, from the local daemon. */
export const computerUseRemoteQueryKey = ["computer-use", "remote"] as const;

export async function fetchComputerUseRemote(): Promise<ComputerUseRemoteSettings> {
	const { data, error } = await apiClient.GET("/api/v1/computer-use/remote");
	if (error || !data) throw apiErrorException(error);
	return data;
}

/** How often to check who is using this computer while other computers may. */
export const REMOTE_ACTIVITY_POLL_MS = 5_000;
