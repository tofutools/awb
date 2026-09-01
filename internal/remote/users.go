package remote

import (
	"context"
	"net/http"
	"net/url"

	"github.com/tofutools/awb/internal/backend"
	"github.com/tofutools/awb/internal/domain"
)

// The user and membership request bodies. As with the others, they are
// declared here rather than reused from the backend package because the wire
// names are part of the API contract and must not follow a Go field rename.
//
// A password travels out and never comes back: no response shape here carries
// one, and neither does domain.User.

type userCreateBody struct {
	Name           string `json:"name"`
	FullName       string `json:"full_name,omitempty"`
	Password       string `json:"password,omitempty"`
	PasswordHash   string `json:"password_hash,omitempty"`
	WorkspaceAdmin bool   `json:"workspace_admin,omitempty"`
	UserAdmin      bool   `json:"user_admin,omitempty"`
}

type userPatchBody struct {
	Password       *string `json:"password,omitempty"`
	PasswordHash   *string `json:"password_hash,omitempty"`
	FullName       *string `json:"full_name,omitempty"`
	WorkspaceAdmin *bool   `json:"workspace_admin,omitempty"`
	UserAdmin      *bool   `json:"user_admin,omitempty"`
}

type membershipSetBody struct {
	Access domain.Access `json:"access"`
}

type membershipCreateBody struct {
	User   string        `json:"user"`
	Access domain.Access `json:"access"`
}

type directoryUser struct {
	domain.User
	ActivityWorkspaces []string `json:"activity_workspaces"`
}

func (b *Backend) CreateUser(ctx context.Context, req backend.UserCreate) (*domain.User, error) {
	body := userCreateBody{
		Name:           req.Name,
		FullName:       req.FullName,
		Password:       req.Password,
		PasswordHash:   req.PasswordHash,
		WorkspaceAdmin: req.WorkspaceAdmin,
		UserAdmin:      req.UserAdmin,
	}
	return b.userCall(ctx, http.MethodPost, "/api/users", body, "")
}

func (b *Backend) GetUser(ctx context.Context, name string) (*domain.User, error) {
	return b.userCall(ctx, http.MethodGet, "/api/users/"+url.PathEscape(name), nil, "")
}

func (b *Backend) ListUsers(ctx context.Context, filter string, limit, offset *int) (backend.UserPage, error) {
	entries := []directoryUser{}
	query := pageQuery(limit, offset)
	if filter != "" {
		query.Set("filter", filter)
	}
	header, err := b.call(ctx, http.MethodGet,
		b.endpoint("/api/users", query), nil, "", &entries)
	if err != nil {
		return backend.UserPage{}, err
	}
	users := make([]domain.User, len(entries))
	for i := range entries {
		users[i] = entries[i].User
		users[i].ActivityWorkspaces = entries[i].ActivityWorkspaces
	}
	return backend.UserPage{Users: users, Total: totalCount(header, len(users))}, nil
}

func (b *Backend) UpdateUser(ctx context.Context, name string, req backend.UserPatch,
	ifMatch string) (*domain.User, error) {
	body := userPatchBody{
		Password:       req.Password,
		PasswordHash:   req.PasswordHash,
		FullName:       req.FullName,
		WorkspaceAdmin: req.WorkspaceAdmin,
		UserAdmin:      req.UserAdmin,
	}
	return b.userCall(ctx, http.MethodPatch, "/api/users/"+url.PathEscape(name), body, ifMatch)
}

func (b *Backend) DeleteUser(ctx context.Context, name, ifMatch string) (*backend.DeletedUser, error) {
	user, err := b.userCall(ctx, http.MethodDelete, "/api/users/"+url.PathEscape(name), nil, ifMatch)
	if err != nil {
		return nil, err
	}
	return &backend.DeletedUser{User: *user}, nil
}

func (b *Backend) ListMembers(ctx context.Context, workspace string, limit, offset *int) (
	backend.MemberPage, error) {
	members := []domain.Membership{}
	path := "/api/workspaces/" + url.PathEscape(workspace) + "/members"
	header, err := b.call(ctx, http.MethodGet,
		b.endpoint(path, pageQuery(limit, offset)), nil, "", &members)
	if err != nil {
		return backend.MemberPage{}, err
	}
	return backend.MemberPage{Members: members, Total: totalCount(header, len(members))}, nil
}

func (b *Backend) SetMember(ctx context.Context, workspace, user string, access domain.Access) (
	*domain.Membership, error) {
	return b.memberCall(ctx, http.MethodPut, workspace, user, membershipSetBody{Access: access})
}

func (b *Backend) AddMember(ctx context.Context, workspace, user string, access domain.Access) (
	*domain.Membership, error) {
	path := "/api/workspaces/" + url.PathEscape(workspace) + "/members"
	var membership domain.Membership
	if _, err := b.call(ctx, http.MethodPost, b.endpoint(path, nil),
		membershipCreateBody{User: user, Access: access}, "", &membership); err != nil {
		return nil, err
	}
	return &membership, nil
}

func (b *Backend) RemoveMember(ctx context.Context, workspace, user string) (*domain.Membership, error) {
	return b.memberCall(ctx, http.MethodDelete, workspace, user, nil)
}

func (b *Backend) userCall(ctx context.Context, method, path string, body any,
	ifMatch string) (*domain.User, error) {
	var user domain.User
	if _, err := b.call(ctx, method, b.endpoint(path, nil), body, ifMatch, &user); err != nil {
		return nil, err
	}
	return &user, nil
}

func (b *Backend) memberCall(ctx context.Context, method, workspace, user string, body any) (
	*domain.Membership, error) {
	path := "/api/workspaces/" + url.PathEscape(workspace) + "/members/" + url.PathEscape(user)
	var membership domain.Membership
	if _, err := b.call(ctx, method, b.endpoint(path, nil), body, "", &membership); err != nil {
		return nil, err
	}
	return &membership, nil
}
