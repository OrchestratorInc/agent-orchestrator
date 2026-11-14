import { useSettings } from "./useSettings";

export interface CloudGate {
	cloudEnabled: boolean;
	localEnabled: boolean;
	client: string;
	loading: boolean;
}

export function useCloudGate(enabled = true): CloudGate {
	const { settings, isLoading } = useSettings(undefined, enabled);
	return {
		cloudEnabled: settings?.cloudEnabled ?? false,
		localEnabled: settings?.localEnabled ?? true,
		client: settings?.client ?? "",
		loading: isLoading,
	};
}
