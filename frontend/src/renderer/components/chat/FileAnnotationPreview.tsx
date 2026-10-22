import { createContext, useContext, useEffect, useRef, useState } from "react";
import { QueryClientContext, useQuery, useQueryClient } from "@tanstack/react-query";
import { useTranslation } from "react-i18next";
import type { ChatFileReference, OpenFileAnnotation } from "../../../shared/chat-file-reference";
import { CapturedFileAnnotation } from "./CapturedFileAnnotation";
import { clientForSessionHost } from "../../lib/host-clients";
import { subscribeWorkspaceFileChanges } from "../../lib/workspace-file-events";

export const FileAnnotationContext = createContext<{sessionId:string;hostId?:string;onOpen?:OpenFileAnnotation;generation?:{current:number}} | undefined>(undefined);

export function FileAnnotationPreview({reference,onNavigate}:{reference:ChatFileReference;onNavigate?:()=>void}) {
 const context=useContext(FileAnnotationContext);
 const queryClient=useContext(QueryClientContext);
 if(context && queryClient) return <ResolvedPreview reference={reference} context={context} onNavigate={onNavigate} />;
 return <CapturedPreview reference={reference} />;
}
function CapturedPreview({reference,status,onOpen,opening}:{reference:ChatFileReference;status?:"chat.fileAnnotationChanged" | "chat.fileAnnotationUnavailable" | "chat.fileAnnotationInvalid" | "chat.fileAnnotationCheckFailed";onOpen?:()=>void;opening?:boolean}) {
 const {t}=useTranslation();
 return <CapturedFileAnnotation reference={reference} onOpen={onOpen} opening={opening}>
  {status ? <span className="block text-muted-foreground" role="status">{t(status)} {t("chat.fileAnnotationCapturedPreview")}</span>:null}
 </CapturedFileAnnotation>;
}

function ResolvedPreview({reference,context,onNavigate}:{reference:ChatFileReference;context:NonNullable<React.ContextType<typeof FileAnnotationContext>>;onNavigate?:()=>void}) {
 const mounted=useRef(true);
 const [opening,setOpening]=useState(false);
 useEffect(()=>{mounted.current=true;return ()=>{mounted.current=false}},[]);
 const queryClient=useQueryClient();
 const query=useQuery({
  queryKey:context.hostId ? ["file-annotation-source",context.hostId,context.sessionId,reference.id] : ["file-annotation-source",context.sessionId,reference.id],
  queryFn:async({signal})=>{
   if(reference.sessionId!==context.sessionId) throw new Error("CHAT_FILE_REFERENCE_INVALID");
   const {data,error}=await clientForSessionHost(context.hostId).POST("/api/v1/sessions/{sessionId}/conversation/file-references/resolve",{
    params:{path:{sessionId:context.sessionId}},body:reference,signal,
   });
   if(error || !data) throw new Error(error?.code ?? "FILE_REFERENCE_CHECK_FAILED");
   return data;
  },
  retry:false,staleTime:0,refetchOnWindowFocus:false,
 });
 useEffect(()=>subscribeWorkspaceFileChanges(context.sessionId,queryClient,context.hostId),[context.sessionId,context.hostId,queryClient]);
 const state=query.isError ? undefined : query.data?.state;
 const failureStatus=query.error?.message==="CHAT_FILE_REFERENCE_INVALID" ? "chat.fileAnnotationInvalid" : "chat.fileAnnotationCheckFailed";
 return <CapturedPreview reference={reference} status={query.isError ? failureStatus : state==="changed" ? "chat.fileAnnotationChanged" : state==="unavailable" ? "chat.fileAnnotationUnavailable" : undefined} opening={opening}
  onOpen={context.onOpen && (state==="unchanged" || state==="changed") && !reference.truncated ? async()=>{
   const generation=context.generation;
   const token=generation ? ++generation.current : 0;
   setOpening(true);
   try {
    const fresh=await query.refetch();
    if(!mounted.current || (generation && generation.current!==token) || fresh.error || (fresh.data?.state!=="unchanged" && fresh.data?.state!=="changed")) return;
    context.onOpen?.(reference,fresh.data);
    onNavigate?.();
   } finally {if(mounted.current)setOpening(false)}
  }:undefined}
 />;
}
