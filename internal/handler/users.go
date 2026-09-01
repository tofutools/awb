package handler

import (
	"context"

	"github.com/tofutools/awb/internal/api"
	"github.com/tofutools/awb/internal/awberr"
	"github.com/tofutools/awb/internal/backend"
	"github.com/tofutools/awb/internal/domain"
)

// userResponse is workspaceResponse for a user, whose tag is built from their
// own updated_at: a membership granted or withdrawn does not invalidate it,
// exactly as a new relation does not invalidate an issue's.
func userResponse(user *domain.User) *api.UserHeaders {
	return &api.UserHeaders{
		Etag:     api.NewOptString(backend.ETag(user.UpdatedAt)),
		Response: toUser(user),
	}
}

func (h *Handler) ListUsers(ctx context.Context, params api.ListUsersParams) (
	*api.UserListHeaders, error) {
	page, err := h.backendFor(ctx).ListUsers(ctx, params.Filter.Or(""), optInt(params.Limit), optInt(params.Offset))
	if err != nil {
		return nil, err
	}
	return &api.UserListHeaders{
		XTotalCount: api.NewOptInt(page.Total),
		Response:    toDirectoryUsers(page.Users),
	}, nil
}

func (h *Handler) CreateUser(ctx context.Context, req *api.UserCreate) (
	*api.UserCreatedHeaders, error) {
	user, err := h.backendFor(ctx).CreateUser(ctx, backend.UserCreate{
		Name:           string(req.Name),
		FullName:       string(req.FullName.Or("")),
		Password:       string(req.Password.Or("")),
		PasswordHash:   string(req.PasswordHash.Or("")),
		WorkspaceAdmin: req.WorkspaceAdmin.Or(false),
		UserAdmin:      req.UserAdmin.Or(false),
	})
	if err != nil {
		return nil, err
	}
	return &api.UserCreatedHeaders{
		Etag:     api.NewOptString(backend.ETag(user.UpdatedAt)),
		Location: api.NewOptString("/api/users/" + user.Name),
		Response: toUser(user),
	}, nil
}

func (h *Handler) GetUser(ctx context.Context, params api.GetUserParams) (
	*api.UserHeaders, error) {
	user, err := h.backendFor(ctx).GetUser(ctx, string(params.Name))
	if err != nil {
		return nil, err
	}
	return userResponse(user), nil
}

// UpdateUser replaces each field the body carries and leaves the others alone.
// name may appear but may not change, exactly as a workspace's key may not;
// workspaces and the timestamps are derived and their values ignored, so a UI can
// send back the object it read. As elsewhere, they are still validated against
// the schema declared for each.
func (h *Handler) UpdateUser(ctx context.Context, req *api.UserPatch,
	params api.UpdateUserParams) (*api.UserHeaders, error) {
	name := string(params.Name)
	if sent, ok := req.Name.Get(); ok && string(sent) != name {
		return nil, awberr.Usagef("a username is immutable")
	}

	user, err := h.backendFor(ctx).UpdateUser(ctx, name, backend.UserPatch{
		Password:       optPassword(req.Password),
		PasswordHash:   optPasswordHash(req.PasswordHash),
		FullName:       optUserFullName(req.FullName),
		WorkspaceAdmin: optBool(req.WorkspaceAdmin),
		UserAdmin:      optBool(req.UserAdmin),
	}, params.IfMatch.Or(""))
	if err != nil {
		return nil, err
	}
	return userResponse(user), nil
}

// DeleteUser answers with the user as they were immediately before deletion,
// and carries no ETag: the version it describes is gone.
func (h *Handler) DeleteUser(ctx context.Context, params api.DeleteUserParams) (*api.User, error) {
	deleted, err := h.backendFor(ctx).DeleteUser(ctx, string(params.Name), params.IfMatch.Or(""))
	if err != nil {
		return nil, err
	}
	user := toUser(&deleted.User)
	return &user, nil
}

func (h *Handler) ListWorkspaceMembers(ctx context.Context, params api.ListWorkspaceMembersParams) (
	*api.MembershipListHeaders, error) {
	page, err := h.backendFor(ctx).ListMembers(ctx, string(params.Key),
		optInt(params.Limit), optInt(params.Offset))
	if err != nil {
		return nil, err
	}
	return &api.MembershipListHeaders{
		XTotalCount: api.NewOptInt(page.Total),
		Response:    toMemberships(page.Members),
	}, nil
}

// AddWorkspaceMember creates a membership without replacing a concurrent grant.
func (h *Handler) AddWorkspaceMember(ctx context.Context, req *api.MembershipCreate,
	params api.AddWorkspaceMemberParams) (*api.Membership, error) {
	membership, err := h.backendFor(ctx).AddMember(ctx, string(params.Key),
		string(req.User), domain.Access(req.Access))
	if err != nil {
		return nil, err
	}
	return toMembership(membership), nil
}

// SetWorkspaceMember replaces whatever access the user held in the workspace. The
// two ends may appear in the body but may not disagree with the path, which is
// what identifies the membership.
func (h *Handler) SetWorkspaceMember(ctx context.Context, req *api.MembershipSet,
	params api.SetWorkspaceMemberParams) (*api.Membership, error) {
	workspace, user := string(params.Key), string(params.User)
	if sent, ok := req.Workspace.Get(); ok && string(sent) != workspace {
		return nil, awberr.Usagef("a membership names the workspace in its path")
	}
	if sent, ok := req.User.Get(); ok && string(sent) != user {
		return nil, awberr.Usagef("a membership names the user in its path")
	}

	membership, err := h.backendFor(ctx).SetMember(ctx, workspace, user, domain.Access(req.Access))
	if err != nil {
		return nil, err
	}
	return toMembership(membership), nil
}

// RemoveWorkspaceMember answers with the membership as it was immediately before
// it was withdrawn.
func (h *Handler) RemoveWorkspaceMember(ctx context.Context, params api.RemoveWorkspaceMemberParams) (
	*api.Membership, error) {
	membership, err := h.backendFor(ctx).RemoveMember(ctx, string(params.Key), string(params.User))
	if err != nil {
		return nil, err
	}
	return toMembership(membership), nil
}

func toUser(user *domain.User) api.User {
	return api.User{
		Name:           api.Username(user.Name),
		FullName:       api.UserFullName(user.FullName),
		WorkspaceAdmin: user.WorkspaceAdmin,
		UserAdmin:      user.UserAdmin,
		CreatedAt:      api.Timestamp(user.CreatedAt),
		UpdatedAt:      api.Timestamp(user.UpdatedAt),
		Workspaces:     toMemberships(user.Workspaces),
	}
}

func optUserFullName(value api.OptUserFullName) *string {
	if name, ok := value.Get(); ok {
		plain := string(name)
		return &plain
	}
	return nil
}

func toDirectoryUsers(users []domain.User) []api.UserDirectoryEntry {
	out := make([]api.UserDirectoryEntry, len(users))
	for i := range users {
		user := toUser(&users[i])
		workspaces := make([]api.WorkspaceKey, len(users[i].ActivityWorkspaces))
		for j := range users[i].ActivityWorkspaces {
			workspaces[j] = api.WorkspaceKey(users[i].ActivityWorkspaces[j])
		}
		out[i] = api.UserDirectoryEntry{
			Name:               user.Name,
			FullName:           user.FullName,
			WorkspaceAdmin:     user.WorkspaceAdmin,
			UserAdmin:          user.UserAdmin,
			CreatedAt:          user.CreatedAt,
			UpdatedAt:          user.UpdatedAt,
			Workspaces:         user.Workspaces,
			ActivityWorkspaces: workspaces,
		}
	}
	return out
}

func toMembership(m *domain.Membership) *api.Membership {
	return &api.Membership{
		Workspace: api.WorkspaceKey(m.Workspace),
		User:      api.Username(m.User),
		Access:    api.Access(m.Access),
	}
}

func toMemberships(members []domain.Membership) []api.Membership {
	out := make([]api.Membership, len(members))
	for i := range members {
		out[i] = *toMembership(&members[i])
	}
	return out
}

func optBool(o api.OptBool) *bool {
	if value, ok := o.Get(); ok {
		return &value
	}
	return nil
}

func optPassword(o api.OptPassword) *string {
	if value, ok := o.Get(); ok {
		password := string(value)
		return &password
	}
	return nil
}

func optPasswordHash(o api.OptPasswordHash) *string {
	if value, ok := o.Get(); ok {
		hash := string(value)
		return &hash
	}
	return nil
}
