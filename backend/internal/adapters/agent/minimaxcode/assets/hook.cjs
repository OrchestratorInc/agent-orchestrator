// agent-orchestrator: managed MiniMax activity hook and exact-identity guard.
const fs = require('node:fs');
const path = require('node:path');
const {spawnSync} = require('node:child_process');
function refuse(message='AO refused a MiniMax conversation identity change. Restore the exact original conversation.') {
  process.stdout.write(JSON.stringify({continue:false,systemMessage:message})+'\n');
}
try {
  // MiniMax executes cached plugin copies, so __dirname is not the profile.
  // TMPDIR survives its hook environment filter and is immutable per launch.
  const inheritedTmp=process.env.TMPDIR || '';
  if(!path.isAbsolute(inheritedTmp)){refuse();process.exit(0);}
  const tmp=path.resolve(inheritedTmp);
  const launchDir=path.dirname(tmp);
  const launchID=path.basename(launchDir);
  const launches=path.dirname(launchDir);
  if(path.basename(tmp)!=='tmp' || path.basename(launches)!=='launches' || !/^[A-Za-z0-9_-]+$/.test(launchID)){refuse();process.exit(0);}
  const profile=path.dirname(launches);
  const routing=JSON.parse(fs.readFileSync(path.join(launchDir,'routing.json'),'utf8'));
  if(routing.AO_RUNTIME_LAUNCH_ID!==launchID || !/^[A-Za-z0-9_-]+$/.test(routing.AO_SESSION_ID || '') || typeof routing.AO_DATA_DIR!=='string' || !path.isAbsolute(routing.AO_DATA_DIR)){refuse();process.exit(0);}
  if(path.resolve(routing.AO_DATA_DIR,'agents','minimax-code',routing.AO_SESSION_ID)!==profile){refuse();process.exit(0);}
  const hookEnv={...process.env};
  for(const key of ['AO_SESSION_ID','AO_DATA_DIR','AO_RUN_FILE','AO_RUNTIME_LAUNCH_ID']) if(typeof routing[key]==='string') hookEnv[key]=routing[key];
  const payload=JSON.parse(fs.readFileSync(0,'utf8'));
  const event=payload.hook_event_name;
  const nativeID=payload.session_id;
  const identityFile=path.join(profile,'ao-native-id');
  const expected=fs.readFileSync(identityFile,'utf8').trim();
  const rootID=/^mvs_[a-f0-9]{32}$/;
  if (expected && !rootID.test(expected)) { refuse(); process.exit(0); }
  const childToolEvent=new Set(['PreToolUse','PermissionRequest','PostToolUse','PostToolUseFailure']).has(event);
  const childToken=/^[^\x00-\x1f\x7f]{1,256}$/;
  const validChildToken=(value)=>typeof value==='string' && value.trim()===value && childToken.test(value);
  const childProvenance=childToolEvent && validChildToken(nativeID) && validChildToken(payload.agent_id) && validChildToken(payload.agent_type);
  if (expected && nativeID!==expected && childProvenance) {
    // MiniMax keeps the child's session_id on inherited tool hooks. Report the
    // already-pinned root while preserving agent_id/agent_type provenance.
    payload.session_id=expected;
  } else {
    if (!rootID.test(nativeID || '')) { refuse(); process.exit(0); }
    if (expected && nativeID!==expected) { refuse(); process.exit(0); }
  }
  if (!expected) {
    if (event!=='SessionStart') { refuse(); process.exit(0); }
    fs.writeFileSync(identityFile,nativeID+'\n',{mode:0o600});
  }
  const events={SessionStart:'session-start',UserPromptSubmit:'user-prompt-submit',PermissionRequest:'permission-blocked',PreToolUse:'pre-tool-use',PostToolUse:'post-tool-use',PostToolUseFailure:'post-tool-use-failure',Stop:'stop'};
  if (!events[event]) process.exit(0);
  const callback=spawnSync('ao',['hooks','minimax-code',events[event]],{input:JSON.stringify({...payload,launch_id:launchID}),env:hookEnv,stdio:['pipe','ignore','ignore'],timeout:8000});
  if(event==='UserPromptSubmit') {
    if(callback.status!==0){refuse('AO could not verify the current MiniMax runtime generation.');process.exit(0);}
    const file=path.join(launchDir,'standing.txt');
    let context;
    try {
      const info=fs.statSync(file);
      if(!info.isFile() || info.size>512*1024){refuse('AO MiniMax private instructions exceed the supported context limit.');process.exit(0);}
      context=fs.readFileSync(file,'utf8');
    } catch {
      refuse('AO MiniMax private instructions are unavailable for this launch.');process.exit(0);
    }
    if(Buffer.byteLength(context,'utf8')>512*1024){refuse('AO MiniMax private instructions exceed the supported context limit.');process.exit(0);}
    if(context) process.stdout.write(JSON.stringify({hookSpecificOutput:{hookEventName:'UserPromptSubmit',additionalContext:context}})+'\n');
  }
} catch {
  // Missing or unreadable guard state must not authorize a replacement turn.
  refuse();
}
