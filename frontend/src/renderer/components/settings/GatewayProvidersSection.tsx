import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { useState } from "react";
import { useTranslation } from "react-i18next";
import { PlugZap } from "lucide-react";
import type { components } from "../../../api/schema";
import { apiClient, apiErrorMessage } from "../../lib/api-client";
import { Button } from "../ui/button";
import { Input } from "../ui/input";
import { GatewayApplySessionsDialog } from "./GatewayApplySessionsDialog";
import { SettingsRow } from "./SettingsRow";
import { SettingsSection } from "./SettingsSection";

type GatewayConfig = components["schemas"]["ControllersGatewayConfigResponse"];
type GatewayProbeResponse =
	components["schemas"]["ControllersGatewayProbeResponse"];
type Staleness = components["schemas"]["SessionProviderStaleness"];

// hostId scopes the cache per daemon: project settings on a remote host read
// that host's gateway config, while the global Settings section stays local.
export const gatewayConfigQueryKey = (projectId?: string, hostId?: string) =>
	["settings", "gateway", hostId ?? "local", projectId ?? "app"] as const;

// ScopeConfig is the editable form state for one scope's entry.
type ScopeForm = { baseUrl: string; token: string; model: string };

function formFromScope(
	scope: components["schemas"]["GatewayScopeValue"] | undefined,
): ScopeForm {
	return {
		baseUrl: scope?.baseUrl ?? "",
		token: "",
		model: scope?.model ?? "",
	};
}

/**
 * Settings screen for Anthropic-compatible gateway providers (any
 * ANTHROPIC_BASE_URL-fronted endpoint). Writes through the same Claude
 * settings files the daemon's resolution chain reads, so a saved entry and a
 * hand-edited file can never disagree. Without a projectId only the app-wide
 * entry is editable; inside a project's settings the project override is,
 * with the app-wide values shown as inherited.
 */
export function GatewayProvidersSection({
	titleHidden,
	projectId,
}: {
	titleHidden?: boolean;
	projectId?: string;
}) {
	const { t } = useTranslation();
	const queryClient = useQueryClient();
	const [appForm, setAppForm] = useState<ScopeForm | null>(null);
	const [projectForm, setProjectForm] = useState<ScopeForm | null>(null);
	const [probe, setProbe] = useState<{
		scope: string;
		result?: GatewayProbeResponse;
		error?: string;
	} | null>(null);
	const [stale, setStale] = useState<Staleness[] | null>(null);

	const configQuery = useQuery({
		queryKey: gatewayConfigQueryKey(projectId),
		queryFn: async () => {
			const { data, error } = await apiClient.GET("/api/v1/settings/gateway", {
				params: projectId ? { query: { projectId } } : undefined,
			});
			if (error) throw new Error(apiErrorMessage(error));
			return data satisfies GatewayConfig;
		},
	});

	// Form state initializes from the fetched config and is then user-owned,
	// so a background refetch never clobbers typing.
	const config = configQuery.data;
	const app = appForm ?? formFromScope(config?.app);
	const project = projectForm ?? formFromScope(config?.project ?? undefined);

	const refresh = () =>
		queryClient.invalidateQueries({
			queryKey: gatewayConfigQueryKey(projectId),
		});

	const save = useMutation({
		mutationFn: async (input: {
			scope: "app" | "project";
			form: ScopeForm;
			saved?: components["schemas"]["GatewayScopeValue"];
		}) => {
			const { error } = await apiClient.PUT("/api/v1/settings/gateway", {
				body: {
					scope: input.scope,
					...(projectId && input.scope === "project" ? { projectId } : {}),
					// Keys are tri-state on the backend: omitted = leave the stored
					// value, "" = clear. Only changed keys are sent, so rotating the
					// token alone never wipes the stored base URL or model.
					...(input.form.baseUrl !== (input.saved?.baseUrl ?? "")
						? { baseUrl: input.form.baseUrl }
						: {}),
					...(input.form.token ? { token: input.form.token } : {}),
					...(input.form.model !== (input.saved?.model ?? "")
						? { model: input.form.model }
						: {}),
				},
			});
			if (error) throw new Error(apiErrorMessage(error));
		},
		onSuccess: () => {
			void refresh();
			// Fetch-on-save: no query-cache entry for staleness, the dialog is
			// driven entirely by this one response. ponytail: if another surface
			// needs staleness, promote it to a useQuery with this key.
			void apiClient
				.GET("/api/v1/sessions/provider-staleness")
				.then(({ data }) => {
					if (data && data.sessions.length > 0) setStale(data.sessions);
				})
				.catch(() => undefined);
		},
	});

	const runProbe = useMutation({
		mutationFn: async (input: {
			scope: "app" | "project";
			form: ScopeForm;
		}) => {
			const { data, error } = await apiClient.POST(
				"/api/v1/settings/gateway/probe",
				{
					body: { baseUrl: input.form.baseUrl, token: input.form.token },
				},
			);
			if (error) throw new Error(apiErrorMessage(error));
			return data satisfies GatewayProbeResponse;
		},
		onSuccess: (result, input) => setProbe({ scope: input.scope, result }),
		onError: (error: Error, input) =>
			setProbe({ scope: input.scope, error: error.message }),
	});

	if (configQuery.isError) {
		return (
			<SettingsSection
				title={t("settings.gateway.title")}
				sectionId="gateway"
				titleHidden={titleHidden}
			>
				<p className="text-micro text-destructive">
					{apiErrorMessage(configQuery.error)}
				</p>
			</SettingsSection>
		);
	}

	const scopeEditor = (
		scope: "app" | "project",
		form: ScopeForm,
		setForm: (next: ScopeForm) => void,
	) => {
		const saved = config && (scope === "app" ? config.app : config.project);
		const dirty =
			form.baseUrl !== (saved?.baseUrl ?? "") ||
			form.model !== (saved?.model ?? "") ||
			form.token !== "";
		const verdict = probe?.scope === scope ? probe.result : undefined;
		return (
			<div className="flex flex-col gap-1.5">
				<SettingsRow
					label={t("settings.gateway.baseUrl")}
					description={t("settings.gateway.baseUrlHint")}
				>
					<Input
						className="max-w-96"
						placeholder="https://gateway.example.com"
						aria-label={t("settings.gateway.baseUrl")}
						onChange={(event) =>
							setForm({ ...form, baseUrl: event.target.value })
						}
						value={form.baseUrl}
					/>
				</SettingsRow>
				<SettingsRow
					label={t("settings.gateway.token")}
					description={
						saved?.tokenSet
							? t("settings.gateway.tokenStored")
							: t("settings.gateway.tokenHint")
					}
				>
					<Input
						className="max-w-96"
						type="password"
						autoComplete="off"
						placeholder={saved?.tokenSet ? "••••••••" : undefined}
						aria-label={t("settings.gateway.token")}
						onChange={(event) =>
							setForm({ ...form, token: event.target.value })
						}
						value={form.token}
					/>
				</SettingsRow>
				<SettingsRow
					label={t("settings.gateway.model")}
					description={t("settings.gateway.modelHint")}
				>
					<Input
						className="max-w-96"
						list={`gateway-models-${scope}`}
						placeholder="claude-sonnet-4-5"
						aria-label={t("settings.gateway.model")}
						onChange={(event) =>
							setForm({ ...form, model: event.target.value })
						}
						value={form.model}
					/>
					<datalist id={`gateway-models-${scope}`}>
						{(verdict?.models ?? []).map((model) => (
							<option key={model.id} value={model.id}>
								{model.displayName || model.id}
							</option>
						))}
					</datalist>
				</SettingsRow>
				<div className="flex items-center gap-2">
					<Button
						type="button"
						variant="outline"
						disabled={!form.baseUrl || !form.token || runProbe.isPending}
						onClick={() => runProbe.mutate({ scope, form })}
					>
						<PlugZap aria-hidden="true" className="size-icon-base" />
						{t("settings.gateway.probe")}
					</Button>
					<Button
						type="button"
						disabled={!dirty || save.isPending}
						onClick={() => save.mutate({ scope, form, saved })}
					>
						{t("settings.gateway.save")}
					</Button>
					{verdict ? (
						<span
							className={
								verdict.state === "valid"
									? "text-micro text-settings-muted"
									: "text-micro text-destructive"
							}
						>
							{verdict.state === "valid"
								? t("settings.gateway.probeOk")
								: verdict.detail || t("settings.gateway.probeFailed")}
						</span>
					) : null}
					{probe?.scope === scope && probe.error ? (
						<span className="text-micro text-destructive">{probe.error}</span>
					) : null}
					{save.isError ? (
						<span className="text-micro text-destructive">
							{apiErrorMessage(save.error)}
						</span>
					) : null}
				</div>
			</div>
		);
	};

	const effective = config?.effective;
	return (
		<SettingsSection
			title={t("settings.gateway.title")}
			sectionId="gateway"
			titleHidden={titleHidden}
		>
			<p className="text-micro text-settings-muted">
				{effective?.source
					? t(
							effective.baseUrl
								? "settings.gateway.effectiveOn"
								: "settings.gateway.effectiveSourceOnly",
							{ baseUrl: effective.baseUrl, source: effective.source },
						)
					: t("settings.gateway.effectiveOff")}
			</p>
			{scopeEditor("app", app, setAppForm)}
			{projectId ? scopeEditor("project", project, setProjectForm) : null}
			{stale ? (
				<GatewayApplySessionsDialog sessions={stale} onClose={() => setStale(null)} />
			) : null}
		</SettingsSection>
	);
}
