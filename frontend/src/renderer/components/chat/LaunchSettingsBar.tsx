import type { QueryClient } from "@tanstack/react-query";
import { sessionUiKey } from "../../lib/hosts";
import type { ChatConfigOption, ChatModel, TurnSettings } from "../../types/conversation";
import { TurnSettingsBar } from "./TurnSettingsBar";

/**
 * What a new task was started with, exactly as the new chat page's settings bar
 * drew it: the agent's own options when a session of it has reported them, else
 * AO's model list.
 */
export type LaunchSettings = {
	harness: string;
	/** Where the agent's model list is read, for the picker the session's bar uses. */
	projectId: string;
	hostId?: string;
	models: ChatModel[];
	settings: TurnSettings;
	configOptions?: ChatConfigOption[];
	showApprovalMode?: boolean;
};

const launchSettingsKey = (sessionId: string, hostId?: string) => ["session-launch-settings", sessionUiKey(sessionId, hostId)] as const;

// Nothing observes these entries, so the default five minutes could drop them
// mid-way through a long setup; two hours covers a slow start without keeping a
// hand-off that nothing ever opened for the life of the app.
const keep = (queryClient: QueryClient, key: readonly string[]) => queryClient.setQueryDefaults(key, { gcTime: 2 * 60 * 60 * 1000 });

export function rememberLaunchSettings(queryClient: QueryClient, sessionId: string, hostId: string | undefined, settings: LaunchSettings): void {
	keep(queryClient, ["session-launch-settings"]);
	queryClient.setQueryData(launchSettingsKey(sessionId, hostId), settings);
}

export function forgetLaunchSettings(queryClient: QueryClient, sessionId: string, hostId?: string): void {
	queryClient.removeQueries({ queryKey: launchSettingsKey(sessionId, hostId), exact: true });
}

const openingStartKey = (sessionId: string, hostId?: string) => ["session-opening-started", sessionUiKey(sessionId, hostId)] as const;

/** When the brief that created this session was sent from the new chat page. */
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

// The new chat page's settings bar, held still while the task starts. The
// session's own bar replaces it, in the same place, once its agent is ready.
export function LaunchSettingsBar({ harness, projectId, hostId, models, settings, configOptions, showApprovalMode }: LaunchSettings) {
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
				agentSwitch={{ projectId, hostId, disabled: true, onPendingChange: ignorePending }}
				disabled
			/>
		</div>
	);
}
