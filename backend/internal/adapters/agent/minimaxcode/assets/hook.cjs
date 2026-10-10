// agent-orchestrator: managed MiniMax activity hook and exact-identity guard.
const fs = require('node:fs');
const path = require('node:path');
const {spawnSync} = require('node:child_process');
const profile = path.resolve(__dirname, '../..');
function refuse() {
  process.stdout.write(JSON.stringify({continue:false,systemMessage:'AO refused a MiniMax conversation identity change. Restore the exact original conversation.'})+'\n');
}
try {
  const payload=JSON.parse(fs.readFileSync(0,'utf8'));
  const event=payload.hook_event_name;
  const nativeID=payload.session_id;
  if (!/^mvs_[a-f0-9]{32}$/.test(nativeID || '')) { refuse(); process.exit(0); }
  const identityFile=path.join(profile,'ao-native-id');
  const expected=fs.readFileSync(identityFile,'utf8').trim();
  if (expected && nativeID!==expected) { refuse(); process.exit(0); }
  if (!expected) {
    if (event!=='SessionStart') { refuse(); process.exit(0); }
    fs.writeFileSync(identityFile,nativeID+'\n',{mode:0o600});
  }
  const events={SessionStart:'session-start',UserPromptSubmit:'user-prompt-submit',PermissionRequest:'permission-blocked',PreToolUse:'pre-tool-use',PostToolUse:'post-tool-use',PostToolUseFailure:'post-tool-use-failure',Stop:'stop'};
  if (!events[event]) process.exit(0);
  // MiniMax preserves TMPDIR when it strips AO_* from hook children. It is
  // fixed in the provider process environment for the lifetime of this launch.
  const tmp=path.resolve(process.env.TMPDIR || '');
  const relative=path.relative(profile,tmp).split(path.sep);
  if(relative.length!==3 || relative[0]!=='launches' || !/^[A-Za-z0-9_-]+$/.test(relative[1]) || relative[2]!=='tmp'){refuse();process.exit(0);}
  const routing=JSON.parse(fs.readFileSync(path.join(tmp,'..','routing.json'),'utf8'));
  if(routing.AO_RUNTIME_LAUNCH_ID!==relative[1]){refuse();process.exit(0);}
  const hookEnv={...process.env};
  for(const key of ['AO_SESSION_ID','AO_DATA_DIR','AO_RUN_FILE','AO_RUNTIME_LAUNCH_ID']) if(typeof routing[key]==='string') hookEnv[key]=routing[key];
  const launchID=hookEnv.AO_RUNTIME_LAUNCH_ID || '';
  if(!launchID || !hookEnv.AO_SESSION_ID || !hookEnv.AO_DATA_DIR) {refuse();process.exit(0);}
  spawnSync('ao',['hooks','minimax-code',events[event]],{input:JSON.stringify({...payload,launch_id:launchID}),env:hookEnv,stdio:['pipe','ignore','ignore'],timeout:8000});
} catch {
  // Missing or unreadable guard state must not authorize a replacement turn.
  refuse();
}
