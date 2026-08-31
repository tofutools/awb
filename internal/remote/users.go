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
	Name         string `json:"name"`
	Password     string `json:"password,omitempty"`
	PasswordHash string `json:"password_hash,omitempty"`
	ProjectAdmin bool   `json:"project_admin,omitempty"`
	UserAdmin    bool   `json:"user_admin,omitempty"`
}

type userPatchBody struct {
	Password     *string `json:"password,omitempty"`
	PasswordHash *string `json:"password_hash,omitempty"`
	ProjectAdmin *bool   `json:"project_admin,omitempty"`
	UserAdmin    *bool   `json:"user_admin,omitempty"`
}

type membershipSetBody struct {
	Access domain.Access `json:"access"`
}

type directoryUser struct {
	domain.User
	ActivityProjects []string `json:"activity_projects"`
}

func (b *Backend) CreateUser(ctx context.Context, req backend.UserCreate) (*domain.User, error) {
	body := userCreateBody{
		Name:         req.Name,
		Password:     req.Password,
		PasswordHash: req.PasswordHash,
		ProjectAdmin: req.ProjectAdmin,
		UserAdmin:    req.UserAdmin,
	}
	return b.userCall(ctx, http.MethodPost, "/api/users", body, "")
}

func (b *Backend) GetUser(ctx context.Context, name string) (*domain.User, error) {
	return b.userCall(ctx, http.MethodGet, "/api/users/"+url.PathEscape(name), nil, "")
}

func (b *Backend) ListUsers(ctx context.Context, limit, offset *int) (backend.UserPage, error) {
	entries := []directoryUser{}
	header, err := b.call(ctx, http.MethodGet,
		b.endpoint("/api/users", pageQuery(limit, offset)), nil, "", &entries)
	if err != nil {
		return backend.UserPage{}, err
	}
	users := make([]domain.User, len(entries))
	for i := range entries {
		users[i] = entries[i].User
		users[i].ActivityProjects = entries[i].ActivityProjects
	}
	return backend.UserPage{Users: users, Total: totalCount(header, len(users))}, nil
}

func (b *Backend) UpdateUser(ctx context.Context, name string, req backend.UserPatch,
	ifMatch string) (*domain.User, error) {
	body := userPatchBody{
		Password:     req.Password,
		PasswordHash: req.PasswordHash,
		ProjectAdmin: req.ProjectAdmin,
		UserAdmin:    req.UserAdmin,
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

func (b *Backend) ListMembers(ctx context.Context, project string, limit, offset *int) (
	backend.MemberPage, error) {
	members := []domain.Membership{}
	path := "/api/projects/" + url.PathEscape(project) + "/members"
	header, err := b.call(ctx, http.MethodGet,
		b.endpoint(path, pageQuery(limit, offset)), nil, "", &members)
	if err != nil {
		return backend.MemberPage{}, err
	}
	return backend.MemberPage{Members: members, Total: totalCount(header, len(members))}, nil
}

func (b *Backend) SetMember(ctx context.Context, project, user string, access domain.Access) (
	*domain.Membership, error) {
	return b.memberCall(ctx, http.MethodPut, project, user, membershipSetBody{Access: access})
}

func (b *Backend) RemoveMember(ctx context.Context, project, user string) (*domain.Membership, error) {
	return b.memberCall(ctx, http.MethodDelete, project, user, nil)
}

func (b *Backend) userCall(ctx context.Context, method, path string, body any,
	ifMatch string) (*domain.User, error) {
	var user domain.User
	if _, err := b.call(ctx, method, b.endpoint(path, nil), body, ifMatch, &user); err != nil {
		return nil, err
	}
	return &user, nil
}

func (b *Backend) memberCall(ctx context.Context, method, project, user string, body any) (
	*domain.Membership, error) {
	path := "/api/projects/" + url.PathEscape(project) + "/members/" + url.PathEscape(user)
	var membership domain.Membership
	if _, err := b.call(ctx, method, b.endpoint(path, nil), body, "", &membership); err != nil {
		return nil, err
	}
	return &membership, nil
}
