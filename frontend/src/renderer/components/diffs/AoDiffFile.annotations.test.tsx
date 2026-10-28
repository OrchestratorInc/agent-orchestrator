import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { render, screen, waitFor } from "@testing-library/react";
import { beforeEach, describe, expect, it, vi } from "vitest";
import type { FileAnnotationRange } from "../../../shared/chat-file-reference";
import type { FilesSource, WorkspaceFileDetail } from "../../hooks/useSessionWorkspaceFiles";
import type { FileAnnotationModel } from "../WorkspaceDiffView";
import { AoDiffFile } from "./AoDiffFile";

const fetches = vi.hoisted(() => ({ workspace: vi.fn(), pr: vi.fn() }));
vi.mock("../../hooks/useSessionWorkspaceFiles", () => ({ fetchWorkspaceFileRevision: fetches.workspace, fetchPRFileRevision: fetches.pr }));
vi.mock("@pierre/diffs", () => ({ parsePatchFiles: (patch: string) => [{files:[{name:"a.ts",type:"changed",cacheKey:patch}]}] }));
vi.mock("./trailingContext", () => ({
 endsAtLastHunk: () => false,
 patchIdentity: (file: {cacheKey:string}) => file.cacheKey,
 hydratedCopy: (file: object, contents: {newFile:{contents:string}}) => ({...file,newFile:contents.newFile}),
}));
vi.mock("@pierre/diffs/react", () => ({FileDiff: ({fileDiff}:{fileDiff:{newFile?:{contents:string}}}) => <pre data-testid="hydrated-content">{fileDiff.newFile?.contents ?? "Loading"}</pre>}));
vi.mock("./useFileAnnotationReveal", () => ({useFileAnnotationReveal: () => ({selectedLines:null,changed:false,reveal:()=>{}})}));

const model:FileAnnotationModel={targets:[],status:"idle",error:"",begin:()=>{},draftFor:()=>"",statusFor:()=>"idle",setDraft:()=>{},cancel:()=>{},submit:async()=>{}};
const range:FileAnnotationRange={requestKey:1,side:"new",line:2,endLine:2,lines:[{kind:"add",oldNo:null,newNo:2,text:"captured();"}]};
function detail(patch:string,version:string):WorkspaceFileDetail {
 return {sessionId:"session",path:"a.ts",status:"modified",additions:1,deletions:1,size:50,binary:false,deleted:false,content:"captured();",contentTruncated:false,diff:patch,diffTruncated:false,fileFingerprint:version,workspaceVersion:version};
}
function panel(client:QueryClient, source:FilesSource, file:WorkspaceFileDetail, navigation:FileAnnotationRange=range) {
 return <QueryClientProvider client={client}><AoDiffFile annotation={model} annotationRange={navigation} detail={file} fallback={null} sessionId="session" source={source} split={false} onActiveSelectionChange={()=>{}} /></QueryClientProvider>;
}
beforeEach(()=>{fetches.workspace.mockReset();fetches.pr.mockReset()});
describe("annotation diff content cache",()=>{
 it("reloads full contents when reopening an edited file, even if its visible patch is unchanged",async()=>{
  const client=new QueryClient({defaultOptions:{queries:{retry:false}}});
  let contents="captured();\nold surrounding code";
  fetches.workspace.mockImplementation(async()=>({exists:true,binary:false,truncated:false,size:contents.length,revision:contents,content:contents}));
  const view=render(panel(client,{kind:"workspace"},detail("patch-1","version-1")));
  await waitFor(()=>expect(screen.getByTestId("hydrated-content")).toHaveTextContent("old surrounding code"));
  contents="captured();\nnew surrounding code";
  view.rerender(panel(client,{kind:"workspace"},detail("patch-1","version-2")));
  await waitFor(()=>expect(screen.getByTestId("hydrated-content")).toHaveTextContent("new surrounding code"));
  contents="changed patch and surrounding code";
  view.rerender(panel(client,{kind:"workspace"},detail("patch-2","version-2")));
  await waitFor(()=>expect(screen.getByTestId("hydrated-content")).toHaveTextContent(contents));
  expect(fetches.workspace).toHaveBeenCalledTimes(6);
 });
 it("does not use detail hints as manifest fences and retries hydration for a fresh navigation",async()=>{
  const client=new QueryClient({defaultOptions:{queries:{retry:false}}});
  const file=detail("same patch","detail-hint");
  fetches.workspace.mockRejectedValueOnce(new Error("temporary source failure"));
  fetches.workspace.mockResolvedValue({exists:true,binary:false,truncated:false,size:11,revision:"current",content:"captured();"});
  const view=render(panel(client,{kind:"workspace"},file));
  await waitFor(()=>expect(fetches.workspace).toHaveBeenCalledTimes(2));
  expect(fetches.workspace.mock.calls.every(([request])=>request.workspaceVersion===undefined)).toBe(true);
  view.rerender(panel(client,{kind:"workspace"},file,{...range,requestKey:2}));
  await waitFor(()=>expect(screen.getByTestId("hydrated-content")).toHaveTextContent("captured();"));
  expect(fetches.workspace).toHaveBeenCalledTimes(4);
 });
 it("separates PR snapshots even when the path and patch are the same",async()=>{
  const client=new QueryClient({defaultOptions:{queries:{retry:false}}});
  let contents="first PR snapshot";
  fetches.pr.mockImplementation(async()=>({exists:true,binary:false,truncated:false,size:contents.length,revision:contents,content:contents}));
  const source:FilesSource={kind:"pull_request",number:7,url:"https://example.test/repo/pull/7",label:"PR #7",snapshot:"head-1"};
  const view=render(panel(client,source,detail("same patch","same fingerprint")));
  await waitFor(()=>expect(screen.getByTestId("hydrated-content")).toHaveTextContent(contents));
  contents="second PR snapshot";
  view.rerender(panel(client,{...source,snapshot:"head-2"},detail("same patch","same fingerprint")));
  await waitFor(()=>expect(screen.getByTestId("hydrated-content")).toHaveTextContent(contents));
  expect(fetches.pr).toHaveBeenCalledTimes(4);
 });
});
