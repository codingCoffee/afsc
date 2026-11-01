package authz

import "testing"

// TestMatrix mirrors the §7.2 table row by row.
func TestMatrix(t *testing.T) {
	rows := []struct {
		cap   Cap
		roles string // admin, editor, author, contributor as a/e/w/c
	}{
		{Self, "aewc"},
		{EditOwn, "aewc"},
		{PublishOwn, "aew"},
		{EditOthers, "ae"},
		{EditPages, "ae"},
		{UploadMedia, "aew"},
		{ModerateComments, "ae"},
		{ManageContent, "ae"},
		{RawHTML, "a"},
		{ManageUsers, "a"},
		{ManageSettings, "a"},
		{ReadAudit, "a"},
	}
	letter := map[string]byte{"admin": 'a', "editor": 'e', "author": 'w', "contributor": 'c'}
	for _, r := range rows {
		for _, role := range Roles {
			want := false
			for i := 0; i < len(r.roles); i++ {
				if r.roles[i] == letter[role] {
					want = true
				}
			}
			if got := Can(role, r.cap); got != want {
				t.Errorf("Can(%s, %s) = %v, want %v", role, r.cap, got, want)
			}
		}
	}
	if !Can("contributor", Public) || Can("nobody", EditOwn) {
		t.Error("public or unknown role")
	}
}
