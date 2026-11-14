import type { QueryClient } from "@tanstack/react-query";
import { sessionUiKey } from "../../lib/hosts";
import type { ApprovalMode, ChatConfigOption, ChatModel, TurnSettings } from "../../types/conversation";
import { TurnSettingsBar } from "./TurnSettingsBar";

export type LaunchSettings = {
	harness: string;
	projectId: string;
	hostId?: string;
	models: ChatModel[];
	settings: TurnSettings;
	configOptions?: ChatConfigOption[];
	showApprovalMode?: boolean;
	unsetModelLabel?: string;
	approvalModes?: ApprovalMode[];
};

const launchSettingsKey = (sessionId: string, hostId?: string) => ["session-launch-settings", sessionUiKey(sessionId, hostId)] as const;

const keep = (queryClient: QueryClient, key: readonly string[]) => queryClient.setQueryDefaults(key, { gcTime: 2 * 60 * 60 * 1000 });

export function rememberLaunchSettings(queryClient: QueryClient, sessionId: string, hostId: string | undefined, settings: LaunchSettings): void {
	keep(queryClient, ["session-launch-settings"]);
	queryClient.setQueryData(launchSettingsKey(sessionId, hostId), settings);
}

export function forgetLaunchSettings(queryClient: QueryClient, sessionId: string, hostId?: string): void {
	queryClient.removeQueries({ queryKey: launchSettingsKey(sessionId, hostId), exact: true });
}

const openingStartKey = (sessionId: string, hostId?: string) => ["session-opening-started", sessionUiKey(sessionId, hostId)] as const;

export function rememberOpeningStart(queryClient: QueryClient, sessionId: string, hostId: string | undefined, startedAt: string): void {
	keep(queryClient, ["session-opening-started"]);
	queryClient.setQueryData(openingStartKey(sessionId, hostId), startedAt);
}

export function forgetOpeningStart(queryClient: QueryClient, sessionId: string, hostId?: string): void {
	queryClient.removeQueries({ queryKey: openingStartKey(sessionId, hostId), exact: true });
}

export function openingStartFor(queryClient: QueryClient, sessionId: string, hostId?: string): string | undefined {
	return queryClient.getQueryData<string>(openingStartKey(sessionId, hostId));
}

export function launchSettingsFor(queryClient: QueryClient, sessionId: string, hostId?: string): LaunchSettings | undefined {
	return queryClient.getQueryData<LaunchSettings>(launchSettingsKey(sessionId, hostId));
}

const ignoreChange = () => undefined;
const ignorePending = () => undefined;

export function LaunchSettingsBar({ harness, projectId, hostId, models, settings, configOptions, showApprovalMode, unsetModelLabel, approvalModes }: LaunchSettings) {
	const providerMode = Boolean(configOptions?.length);
	return (
		<div className="contents [&_button:disabled]:cursor-default [&_button:disabled]:opacity-100">
			<TurnSettingsBar
				models={models}
				settings={settings}
				harness={harness}
				onChange={providerMode ? undefined : ignoreChange}
				configOptions={configOptions}
				onChangeConfigOption={providerMode ? ignoreChange : undefined}
				showApprovalMode={showApprovalMode}
				unsetModelLabel={unsetModelLabel}
				approvalModes={approvalModes}
				agentSwitch={{ projectId, hostId, disabled: true, onPendingChange: ignorePending }}
				disabled
			/>
		</div>
	);
}
