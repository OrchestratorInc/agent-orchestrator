package hermesacp

import acpdriver "github.com/OrchestratorInc/agent-orchestrator/backend/internal/adapters/chatdriver/acp"

// hiddenCommands are Hermes built-ins kept out of the `/` menu.
//
// /model and /title change what AO's model picker and thread name show. AO
// sends /steer itself, and /queue reorders the same mid-turn delivery. /undo
// and /retry rewind Hermes' history without AO's transcript following. /reset
// stays: AO handles it (see resetsConversation). /compress stays because it is
// Hermes' only compaction.
var hiddenCommands = acpdriver.HideCommands("model", "title", "steer", "queue", "undo", "retry")
