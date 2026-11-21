-- +goose Up

-- claude-agent-acp 0.70 failed any successful Claude turn whose answer contained
-- "Please run /login" as a refused login, with the answer's last text block as
-- the error (#8427). Those turns kept their answer but also showed it again as an
-- error, a "Stopped" line and the login band, and asked for a login. Mark them
-- completed, as the fixed chat records them, and drop the sign-in demand they
-- left on the conversation if nothing has replaced it since.
--
-- A turn is one of these when its error contains the login advice and ends the
-- last answer the agent gave in that turn, with no tool call after it, ignoring
-- spacing as the chat driver does. A real refusal never shows its text as an
-- answer: the adapter sends it only as the turn's failure. An earlier answer that
-- ended on the refusal text doesn't count, since the refusal can follow it. The
-- raw provider event archive keeps what the adapter reported.
CREATE TEMP TABLE quoted_login_refusals AS
WITH RECURSIVE
  -- What strings.Fields treats as space. The chat driver drops these from both
  -- texts before it compares them.
  spaces(step, ch) AS (VALUES
    (1, char(0x09)), (2, char(0x0A)), (3, char(0x0B)), (4, char(0x0C)), (5, char(0x0D)),
    (6, char(0x20)), (7, char(0x85)), (8, char(0xA0)), (9, char(0x1680)),
    (10, char(0x2000)), (11, char(0x2001)), (12, char(0x2002)), (13, char(0x2003)),
    (14, char(0x2004)), (15, char(0x2005)), (16, char(0x2006)), (17, char(0x2007)),
    (18, char(0x2008)), (19, char(0x2009)), (20, char(0x200A)), (21, char(0x2028)),
    (22, char(0x2029)), (23, char(0x202F)), (24, char(0x205F)), (25, char(0x3000))
  ),
  last_answers(id, conversation_id, error_message, answer, tail, step) AS (
    SELECT t.id, t.conversation_id, t.error_message, m.text, t.error_message, 0
    FROM conversation_turns t
    JOIN conversation_messages m ON m.turn_id = t.id AND m.role = 'assistant'
    WHERE t.state = 'failed'
      AND t.failure_code IN ('', 'auth_required')
      AND instr(t.error_message, 'Please run /login') > 0
      AND m.sequence = (
        SELECT max(last.sequence)
        FROM conversation_messages last
        WHERE last.turn_id = t.id
          AND last.role = 'assistant'
      )
      -- Messages and activities share the conversation's sequence. The driver
      -- stops reading the answer as the result once a tool call starts after it.
      AND NOT EXISTS (
        SELECT 1
        FROM conversation_activities a
        WHERE a.turn_id = t.id
          AND a.kind IN ('command', 'file_change', 'mcp_tool')
          AND a.sequence > m.sequence
      )
    UNION ALL
    SELECT l.id, l.conversation_id, l.error_message, replace(l.answer, s.ch, ''), replace(l.tail, s.ch, ''), s.step
    FROM last_answers l
    JOIN spaces s ON s.step = l.step + 1
  )
SELECT id, conversation_id, error_message
FROM last_answers
WHERE step = (SELECT max(step) FROM spaces)
  AND substr(answer, -length(tail)) = tail;

-- The demand's reason is the error it was recorded from, and every refused turn
-- records the demand again, so the demand belongs to the conversation's latest
-- failed turn with that error. Drop it only when that turn is one repaired here:
-- a later real refusal can repeat the same text and keeps its demand. The
-- failure evidence is kept, as a recovered login keeps it.
UPDATE conversations
SET account_json = json_remove(account_json, '$.authenticationState', '$.reauthRequiredAt', '$.reauthReason')
WHERE account_json IS NOT NULL
  AND json_valid(account_json)
  AND json_extract(account_json, '$.reauthRequiredAt') IS NOT NULL
  AND (
    SELECT t.id
    FROM conversation_turns t
    WHERE t.conversation_id = conversations.id
      AND t.state = 'failed'
      AND t.error_message = json_extract(conversations.account_json, '$.reauthReason')
    ORDER BY t.completed_at DESC, t.requested_at DESC, t.rowid DESC
    LIMIT 1
  ) IN (SELECT r.id FROM quoted_login_refusals r);

UPDATE conversation_turns
SET state = 'completed',
    error_message = '',
    failure_code = ''
WHERE id IN (SELECT id FROM quoted_login_refusals);

DROP TABLE quoted_login_refusals;

-- +goose Down
-- A data repair: the fixed chat records these turns the same way, so there is
-- nothing to undo.
