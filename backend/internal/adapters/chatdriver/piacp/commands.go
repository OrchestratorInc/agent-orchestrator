package piacp

import acpdriver "github.com/aoagents/agent-orchestrator/backend/internal/adapters/chatdriver/acp"

// hiddenCommands are pi-acp built-ins kept out of the `/` menu.
//
// pi-acp runs its built-ins before a same-named prompt template, so a template
// with one of these names was never reachable. /name renames the session, and
// /steering and /follow-up change how mid-turn messages are delivered, both of
// which AO owns. /autocompact flips a pi setting AO does not show.
var hiddenCommands = acpdriver.HideCommands("name", "steering", "follow-up", "autocompact")
