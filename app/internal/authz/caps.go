// Package authz defines admin capabilities and the role → capability matrix (§7.2).
package authz

import "slices"

type Cap string

const (
	// Public marks a route that needs no session (sign-in, setup, invite accept, token refresh).
	Public Cap = ""
	// Self is held by every signed-in user: their own profile and sessions.
	Self Cap = "self"

	EditOwn          Cap = "posts.edit_own"
	PublishOwn       Cap = "posts.publish_own"
	EditOthers       Cap = "posts.edit_others"
	EditPages        Cap = "pages.edit"
	UploadMedia      Cap = "media.upload"
	ManageContent    Cap = "content.manage"
	ModerateComments Cap = "comments.moderate"
	RawHTML          Cap = "content.raw_html"
	ManageUsers      Cap = "users.manage"
	ManageSettings   Cap = "settings.manage"
	ReadAudit        Cap = "audit.read"
)

var Roles = []string{"admin", "editor", "author", "contributor"}

var matrix = map[string][]Cap{
	"admin": {EditOwn, PublishOwn, EditOthers, EditPages, UploadMedia, ManageContent, ModerateComments, RawHTML,
		ManageUsers, ManageSettings, ReadAudit},
	"editor":      {EditOwn, PublishOwn, EditOthers, EditPages, UploadMedia, ManageContent, ModerateComments},
	"author":      {EditOwn, PublishOwn, UploadMedia},
	"contributor": {EditOwn},
}

// Can reports whether role holds cap. Public is held by everyone.
func Can(role string, c Cap) bool {
	if c == Public {
		return true
	}
	if c == Self {
		return ValidRole(role)
	}
	return slices.Contains(matrix[role], c)
}

// Caps lists the capabilities of role, for the admin UI.
func Caps(role string) []Cap {
	out := make([]Cap, len(matrix[role]))
	copy(out, matrix[role])
	return out
}

func ValidRole(role string) bool { return slices.Contains(Roles, role) }
