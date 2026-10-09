-- Widen the sessions.harness CHECK to allow the OpenHands adapter.
-- SQLite cannot ALTER an existing CHECK constraint, so this applies the same
-- surgical sqlite_master rewrite used by the earlier harness migrations.
--
-- Both current constraint variants are rewritten explicitly, matching how the
-- earlier harness migrations did it. A legacy or skipped-migration database whose
-- constraint is missing an earlier harness matches neither string on purpose:
-- migrations run before reconcileSchema, so those databases are widened by that
-- repair instead, which therefore also knows about this harness.

-- +goose NO TRANSACTION
-- +goose Up
-- +goose StatementBegin
PRAGMA writable_schema = ON;
-- +goose StatementEnd
-- +goose StatementBegin
UPDATE sqlite_master
SET sql = replace(
    sql,
    'CHECK (harness IN ('''', ''claude-code'', ''codex'', ''aider'', ''opencode'', ''opencode-v2'', ''grok'', ''droid'', ''amp'', ''agy'', ''crush'', ''cursor'', ''qwen'', ''copilot'', ''goose'', ''auggie'', ''continue'', ''devin'', ''cline'', ''kimi'', ''muse'', ''kiro'', ''kilocode'', ''vibe'', ''pi'', ''kimchi'', ''prime-agent'', ''autohand'', ''gemini'', ''omp'', ''unreal-agent'', ''fx'', ''mimo-code'', ''deepseek-harness'', ''fake''))',
    'CHECK (harness IN ('''', ''claude-code'', ''codex'', ''aider'', ''opencode'', ''opencode-v2'', ''grok'', ''droid'', ''amp'', ''agy'', ''crush'', ''cursor'', ''qwen'', ''copilot'', ''goose'', ''auggie'', ''continue'', ''devin'', ''cline'', ''kimi'', ''muse'', ''kiro'', ''kilocode'', ''vibe'', ''pi'', ''kimchi'', ''prime-agent'', ''autohand'', ''gemini'', ''omp'', ''unreal-agent'', ''fx'', ''mimo-code'', ''deepseek-harness'', ''openhands'', ''fake''))'
)
WHERE type = 'table' AND name = 'sessions';
-- +goose StatementEnd
-- +goose StatementBegin
UPDATE sqlite_master
SET sql = replace(
    sql,
    'CHECK (harness IN ('''', ''claude-code'', ''codex'', ''aider'', ''opencode'', ''opencode-v2'', ''grok'', ''droid'', ''amp'', ''agy'', ''crush'', ''cursor'', ''qwen'', ''copilot'', ''goose'', ''auggie'', ''continue'', ''devin'', ''cline'', ''kimi'', ''muse'', ''kiro'', ''kilocode'', ''vibe'', ''pi'', ''kimchi'', ''prime-agent'', ''autohand'', ''gemini'', ''omp'', ''unreal-agent'', ''fx'', ''mimo-code'', ''qm'', ''deepseek-harness'', ''fake''))',
    'CHECK (harness IN ('''', ''claude-code'', ''codex'', ''aider'', ''opencode'', ''opencode-v2'', ''grok'', ''droid'', ''amp'', ''agy'', ''crush'', ''cursor'', ''qwen'', ''copilot'', ''goose'', ''auggie'', ''continue'', ''devin'', ''cline'', ''kimi'', ''muse'', ''kiro'', ''kilocode'', ''vibe'', ''pi'', ''kimchi'', ''prime-agent'', ''autohand'', ''gemini'', ''omp'', ''unreal-agent'', ''fx'', ''mimo-code'', ''qm'', ''deepseek-harness'', ''openhands'', ''fake''))'
)
WHERE type = 'table' AND name = 'sessions';
-- +goose StatementEnd
-- +goose StatementBegin
PRAGMA writable_schema = RESET;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
PRAGMA writable_schema = ON;
-- +goose StatementEnd
-- +goose StatementBegin
UPDATE sqlite_master
SET sql = replace(
    sql,
    'CHECK (harness IN ('''', ''claude-code'', ''codex'', ''aider'', ''opencode'', ''opencode-v2'', ''grok'', ''droid'', ''amp'', ''agy'', ''crush'', ''cursor'', ''qwen'', ''copilot'', ''goose'', ''auggie'', ''continue'', ''devin'', ''cline'', ''kimi'', ''muse'', ''kiro'', ''kilocode'', ''vibe'', ''pi'', ''kimchi'', ''prime-agent'', ''autohand'', ''gemini'', ''omp'', ''unreal-agent'', ''fx'', ''mimo-code'', ''deepseek-harness'', ''openhands'', ''fake''))',
    'CHECK (harness IN ('''', ''claude-code'', ''codex'', ''aider'', ''opencode'', ''opencode-v2'', ''grok'', ''droid'', ''amp'', ''agy'', ''crush'', ''cursor'', ''qwen'', ''copilot'', ''goose'', ''auggie'', ''continue'', ''devin'', ''cline'', ''kimi'', ''muse'', ''kiro'', ''kilocode'', ''vibe'', ''pi'', ''kimchi'', ''prime-agent'', ''autohand'', ''gemini'', ''omp'', ''unreal-agent'', ''fx'', ''mimo-code'', ''deepseek-harness'', ''fake''))'
)
WHERE type = 'table' AND name = 'sessions';
-- +goose StatementEnd
-- +goose StatementBegin
UPDATE sqlite_master
SET sql = replace(
    sql,
    'CHECK (harness IN ('''', ''claude-code'', ''codex'', ''aider'', ''opencode'', ''opencode-v2'', ''grok'', ''droid'', ''amp'', ''agy'', ''crush'', ''cursor'', ''qwen'', ''copilot'', ''goose'', ''auggie'', ''continue'', ''devin'', ''cline'', ''kimi'', ''muse'', ''kiro'', ''kilocode'', ''vibe'', ''pi'', ''kimchi'', ''prime-agent'', ''autohand'', ''gemini'', ''omp'', ''unreal-agent'', ''fx'', ''mimo-code'', ''qm'', ''deepseek-harness'', ''openhands'', ''fake''))',
    'CHECK (harness IN ('''', ''claude-code'', ''codex'', ''aider'', ''opencode'', ''opencode-v2'', ''grok'', ''droid'', ''amp'', ''agy'', ''crush'', ''cursor'', ''qwen'', ''copilot'', ''goose'', ''auggie'', ''continue'', ''devin'', ''cline'', ''kimi'', ''muse'', ''kiro'', ''kilocode'', ''vibe'', ''pi'', ''kimchi'', ''prime-agent'', ''autohand'', ''gemini'', ''omp'', ''unreal-agent'', ''fx'', ''mimo-code'', ''qm'', ''deepseek-harness'', ''fake''))'
)
WHERE type = 'table' AND name = 'sessions';
-- +goose StatementEnd
-- +goose StatementBegin
PRAGMA writable_schema = RESET;
-- +goose StatementEnd
