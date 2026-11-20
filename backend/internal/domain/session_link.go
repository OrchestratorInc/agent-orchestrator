package domain

import "net/url"

// StandaloneLinkProjectID is the project segment of a standalone session's
// in-app link. A standalone session has no project (its ProjectID is empty),
// and the desktop app lists it in a workspace with this id.
const StandaloneLinkProjectID = "__standalone__"

// SessionLink is the canonical in-app link to a session,
// ao://sessions/{project}/{session}, with StandaloneLinkProjectID standing in
// for a standalone session's empty project. Each segment is percent-encoded.
func SessionLink(project ProjectID, id SessionID) string {
	if id == "" {
		return ""
	}
	if project == "" {
		project = StandaloneLinkProjectID
	}
	return "ao://sessions/" + url.PathEscape(string(project)) + "/" + url.PathEscape(string(id))
}
