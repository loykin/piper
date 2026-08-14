package project

// ProjectRef globally identifies a project as the combination of its owning
// Home, the Member that owns its execution state, and the project ID itself
// — the same project ID string may exist under different Members, so the ID
// alone is not a global identifier (fed.md §10.2).
type ProjectRef struct {
	HomeID    string
	MemberID  string
	ProjectID string
}

// localHomeID/localMemberID identify the single-install case: one Home with
// one Local Member in the same process, no enrollment. Real Home/Member
// identifiers arrive with remote Member enrollment (fed.md §13.4).
const (
	LocalHomeID   = "home-local"
	LocalMemberID = "member-local"
)

// LocalRef builds the ProjectRef for the single-install case, where the Home
// always routes to its one Local Member.
func LocalRef(projectID string) ProjectRef {
	return ProjectRef{HomeID: LocalHomeID, MemberID: LocalMemberID, ProjectID: projectID}
}
