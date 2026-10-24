import { useLocalSearchParams, useRouter } from "expo-router";
import { useRef, type ReactNode } from "react";
import { ActivityIndicator, View } from "react-native";
import type { SourceRef } from "../environment/scopedBoard";
import { resolveSessionRouteSource } from "../session/sessionRoute";
import { HostScope, useApp } from "../store";
import { useTheme } from "../ThemeProvider";
import { Button, EmptyState } from "../ui";
import type { PullRequestSource } from "./source";

export type PRRouteOwner = { source: SourceRef; scopeKey?: string };
export function prOwnerParams(owner: PRRouteOwner) {
  return { source: owner.source.kind, sourceId: owner.source.id, ...(owner.scopeKey ? { scopeKey: owner.scopeKey } : {}), ...(owner.source.kind === "local" ? { hostId: owner.source.id } : {}) };
}

/** Resolve ownership before mounting readers or any Local controller hooks. */
export function PRScope({ children }: { children: (source: PullRequestSource | undefined, owner: PRRouteOwner) => ReactNode }) {
  const params = useLocalSearchParams<{ sessionId?: string; prUrl?: string; prNumber?: string; source?: string; sourceId?: string; hostId?: string; scopeKey?: string }>();
  const { scopedBoard, sourceFor, currentHostId, configResolved, cloudBoard } = useApp();
  const router = useRouter(); const t = useTheme();
  const malformed = typeof params.sessionId !== "string" || !params.sessionId.trim() || params.sessionId.trim() !== params.sessionId ||
    params.prUrl !== undefined && (typeof params.prUrl !== "string" || !params.prUrl.trim()) ||
    params.prNumber !== undefined && (typeof params.prNumber !== "string" || !/^[1-9]\d*$/.test(params.prNumber));
  const resolution = resolveSessionRouteSource({ ...params, id: params.sessionId ?? "" }, scopedBoard?.sessions ?? []);
  const legacy = params.source === undefined && params.sourceId === undefined && params.hostId === undefined && resolution.kind === "missing" && currentHostId ? { kind: "local" as const, id: currentHostId } : undefined;
  const ref = resolution.kind === "found" ? resolution.source : legacy;
  const source = ref ? sourceFor?.(ref)?.pullRequests : undefined;
  const generation = useRef({ source, value: 0 });
  if (generation.current.source !== source) generation.current = { source, value: generation.current.value + 1 };
  const unavailable = (title: string, message: string) => <View style={{ flex: 1, backgroundColor: t.bgBase }}><EmptyState icon="git-pull-request" title={title} message={message} action={<Button title="Open Workers" onPress={() => router.navigate("/")} />} /></View>;
  if (!configResolved || ref?.kind === "cloud" && cloudBoard?.loading && !source) return <View style={{ flex: 1, alignItems: "center", justifyContent: "center" }}><ActivityIndicator color={t.accent} /></View>;
  if (malformed || !ref || resolution.kind === "invalid" || resolution.kind === "ambiguous") return unavailable("Pull request source unavailable", "Open this pull request from the workspace that owns it.");
  if (ref.kind === "local" && !source) return unavailable("Machine offline", "Pull requests load once this paired desktop reconnects.");
  if (ref.kind === "cloud" && (!source || !params.scopeKey || params.scopeKey !== source.scopeKey || !source.isCurrent())) return unavailable("Cloud workspace changed", "Sign in to the account that owns this pull request, then reopen it from Pull Requests.");
  const owner = { source: ref, ...(ref.kind === "cloud" ? { scopeKey: params.scopeKey } : {}) };
  const content = <View key={JSON.stringify([ref.kind, ref.id, params.scopeKey, params.sessionId, params.prUrl, params.prNumber, generation.current.value])} style={{ flex: 1 }}>{children(source, owner)}</View>;
  return ref.kind === "local" ? <HostScope hostId={ref.id} key={ref.id}>{content}</HostScope> : content;
}
