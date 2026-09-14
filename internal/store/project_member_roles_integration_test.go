package store_test

import (
	"errors"
	"testing"

	"github.com/google/uuid"

	"github.com/bradleymackey/track-slash/internal/model"
	"github.com/bradleymackey/track-slash/internal/store"
)

func TestProjectMemberRolesAndPermissions(t *testing.T) {
	t.Parallel()
	env := newSprintsEnv(t)
	project, err := env.store.GetProject(env.ctx, env.projectID)
	if err != nil {
		t.Fatalf("GetProject: %v", err)
	}
	owner, err := env.store.GetUser(env.ctx, project.OwnerID)
	if err != nil {
		t.Fatalf("GetUser owner: %v", err)
	}
	member, err := env.store.CreateUserProfile(env.ctx, "role-member-"+uniqueProjectKey(t), "role-member@example.com", "Role Member")
	if err != nil {
		t.Fatalf("CreateUserProfile member: %v", err)
	}
	readonly, err := env.store.CreateUserProfile(env.ctx, "role-readonly-"+uniqueProjectKey(t), "role-readonly@example.com", "Role Readonly")
	if err != nil {
		t.Fatalf("CreateUserProfile readonly: %v", err)
	}
	outsider, err := env.store.CreateUserProfile(env.ctx, "role-outsider-"+uniqueProjectKey(t), "role-outsider@example.com", "Role Outsider")
	if err != nil {
		t.Fatalf("CreateUserProfile outsider: %v", err)
	}

	gotMember, err := env.store.SetProjectMemberRole(env.ctx, env.projectID, member.ID, model.ProjectMemberRoleMember)
	if err != nil {
		t.Fatalf("SetProjectMemberRole member: %v", err)
	}
	if gotMember.Role != model.ProjectMemberRoleMember || gotMember.Username != member.Username || gotMember.Name != member.Name {
		t.Fatalf("member = %+v", gotMember)
	}
	gotReadonly, err := env.store.SetProjectMemberRole(env.ctx, env.projectID, readonly.ID, model.ProjectMemberRoleReadonly)
	if err != nil {
		t.Fatalf("SetProjectMemberRole readonly: %v", err)
	}
	if gotReadonly.Role != model.ProjectMemberRoleReadonly || gotReadonly.Username != readonly.Username {
		t.Fatalf("readonly = %+v", gotReadonly)
	}
	if again, err := env.store.SetProjectMemberRole(env.ctx, env.projectID, readonly.ID, model.ProjectMemberRoleReadonly); err != nil || !again.CreatedAt.Equal(gotReadonly.CreatedAt) {
		t.Fatalf("idempotent SetProjectMemberRole = %+v, %v", again, err)
	}
	members, err := env.store.ListProjectMembers(env.ctx, env.projectID)
	if err != nil {
		t.Fatalf("ListProjectMembers: %v", err)
	}
	if len(members) != 3 || members[0].UserID != owner.ID || members[1].Username == "" || members[2].Username == "" {
		t.Fatalf("members = %+v", members)
	}

	for _, test := range []struct {
		name   string
		user   model.User
		read   bool
		write  bool
		manage bool
		remove bool
	}{
		{name: "owner", user: owner, read: true, write: true, manage: true, remove: true},
		{name: "member", user: member, read: true, write: true},
		{name: "readonly", user: readonly, read: true},
		{name: "outsider", user: outsider},
	} {
		t.Run(test.name, func(t *testing.T) {
			permissions, err := env.store.ProjectPermissionsForUser(env.ctx, test.user, env.projectID)
			if err != nil {
				t.Fatalf("ProjectPermissionsForUser: %v", err)
			}
			if permissions.CanRead != test.read || permissions.CanWrite != test.write || permissions.CanManageMembers != test.manage || permissions.CanDelete != test.remove {
				t.Fatalf("permissions = %+v", permissions)
			}
			canDelete, err := env.store.UserCanDeleteProject(env.ctx, test.user, env.projectID)
			if err != nil || canDelete != test.remove {
				t.Fatalf("UserCanDeleteProject = %v, %v", canDelete, err)
			}
			canRead, err := env.store.UserCanAccessProject(env.ctx, test.user, env.projectID)
			if err != nil || canRead != test.read {
				t.Fatalf("UserCanAccessProject = %v, %v", canRead, err)
			}
			canWrite, err := env.store.UserCanWriteProject(env.ctx, test.user, env.projectID)
			if err != nil || canWrite != test.write {
				t.Fatalf("UserCanWriteProject = %v, %v", canWrite, err)
			}
			canManage, err := env.store.UserCanManageProjectMembers(env.ctx, test.user, env.projectID)
			if err != nil || canManage != test.manage {
				t.Fatalf("UserCanManageProjectMembers = %v, %v", canManage, err)
			}
		})
	}
	admin := outsider
	admin.IsAdmin = true
	adminPermissions, err := env.store.ProjectPermissionsForUser(env.ctx, admin, env.projectID)
	if err != nil || !adminPermissions.CanRead || !adminPermissions.CanWrite || !adminPermissions.CanManageMembers || !adminPermissions.CanDelete {
		t.Fatalf("admin permissions = %+v, %v", adminPermissions, err)
	}

	if _, err := env.store.SetProjectMemberRole(env.ctx, env.projectID, owner.ID, model.ProjectMemberRoleReadonly); !errors.Is(err, store.ErrConflict) {
		t.Fatalf("downgrade owner err = %v, want ErrConflict", err)
	}
	if err := env.store.RevokeProjectAccess(env.ctx, env.projectID, owner.ID); !errors.Is(err, store.ErrConflict) {
		t.Fatalf("remove owner err = %v, want ErrConflict", err)
	}
	if _, err := env.store.SetProjectMemberRole(env.ctx, env.projectID, outsider.ID, model.ProjectMemberRole("invalid")); !errors.Is(err, store.ErrConflict) {
		t.Fatalf("invalid role err = %v, want ErrConflict", err)
	}
	if _, err := env.store.SetProjectMemberRole(env.ctx, uuid.New(), outsider.ID, model.ProjectMemberRoleMember); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("missing project err = %v, want ErrNotFound", err)
	}
	if _, err := env.store.SetProjectMemberRole(env.ctx, env.projectID, uuid.New(), model.ProjectMemberRoleMember); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("missing user err = %v, want ErrNotFound", err)
	}
	if _, err := env.store.ProjectPermissionsForUser(env.ctx, owner, uuid.New()); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("missing project permissions err = %v, want ErrNotFound", err)
	}

	for _, query := range []string{"", "   ", "o"} {
		candidates, err := env.store.SearchAvailableProjectMembers(env.ctx, store.SearchAvailableProjectMembersParams{ProjectID: env.projectID, Query: query, Limit: 10})
		if err != nil || len(candidates) != 0 {
			t.Fatalf("short-query candidates for %q = %+v, %v", query, candidates, err)
		}
	}
	candidates, err := env.store.SearchAvailableProjectMembers(env.ctx, store.SearchAvailableProjectMembersParams{ProjectID: env.projectID, Query: "outsider", Limit: 10})
	if err != nil {
		t.Fatalf("SearchAvailableProjectMembers: %v", err)
	}
	if len(candidates) != 1 || candidates[0].ID != outsider.ID {
		t.Fatalf("candidates = %+v", candidates)
	}
	deletedCandidate, err := env.store.CreateUserProfile(env.ctx, "role-deleted-"+uniqueProjectKey(t), "role-deleted@example.com", "Role Deleted")
	if err != nil {
		t.Fatalf("CreateUserProfile deleted candidate: %v", err)
	}
	if err := env.store.DeleteUser(env.ctx, deletedCandidate.ID); err != nil {
		t.Fatalf("DeleteUser candidate: %v", err)
	}
	deletedCandidates, err := env.store.SearchAvailableProjectMembers(env.ctx, store.SearchAvailableProjectMembersParams{ProjectID: env.projectID, Query: deletedCandidate.Username, Limit: 10})
	if err != nil || len(deletedCandidates) != 0 {
		t.Fatalf("deleted candidates = %+v, %v", deletedCandidates, err)
	}

	updated, err := env.store.SetProjectMemberRole(env.ctx, env.projectID, readonly.ID, model.ProjectMemberRoleMember)
	if err != nil || updated.Role != model.ProjectMemberRoleMember {
		t.Fatalf("updated member = %+v, %v", updated, err)
	}
	if err := env.store.RevokeProjectAccess(env.ctx, env.projectID, member.ID); err != nil {
		t.Fatalf("RevokeProjectAccess member: %v", err)
	}
	canRead, err := env.store.UserCanAccessProject(env.ctx, member, env.projectID)
	if err != nil || canRead {
		t.Fatalf("revoked access = %v, %v", canRead, err)
	}
	if err := env.store.RevokeProjectAccess(env.ctx, env.projectID, member.ID); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("second revoke err = %v, want ErrNotFound", err)
	}
	if granted, err := env.store.GrantProjectAccess(env.ctx, env.projectID, member.ID); err != nil || granted.Role != model.ProjectMemberRoleMember {
		t.Fatalf("GrantProjectAccess = %+v, %v", granted, err)
	}
	entries, _, err := env.store.ListProjectChangelog(env.ctx, store.ListProjectChangelogParams{ProjectID: env.projectID, Limit: 100})
	if err != nil {
		t.Fatalf("ListProjectChangelog: %v", err)
	}
	ops := map[string]int{}
	for _, entry := range entries {
		if entry.Entity == "project_member" {
			ops[entry.Op]++
		}
	}
	if ops["grant"] != 3 || ops["update"] != 1 || ops["revoke"] != 1 {
		t.Fatalf("project member changelog ops = %+v", ops)
	}
}

func TestReadonlyProjectMembersCannotBeNewIssuePeople(t *testing.T) {
	t.Parallel()
	env := newSprintsEnv(t)
	readonly, err := env.store.CreateUserProfile(env.ctx, "readonly-person-"+uniqueProjectKey(t), "readonly-person@example.com", "Readonly Person")
	if err != nil {
		t.Fatalf("CreateUserProfile: %v", err)
	}
	if _, err := env.store.SetProjectMemberRole(env.ctx, env.projectID, readonly.ID, model.ProjectMemberRoleReadonly); err != nil {
		t.Fatalf("SetProjectMemberRole: %v", err)
	}
	if _, err := env.store.CreateIssue(env.ctx, store.CreateIssueParams{ProjectID: env.projectID, Title: "readonly assignee", AssigneeID: &readonly.ID}); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("readonly assignee err = %v, want ErrNotFound", err)
	}
	if _, err := env.store.CreateIssue(env.ctx, store.CreateIssueParams{ProjectID: env.projectID, Title: "readonly reporter", ReporterID: &readonly.ID}); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("readonly reporter err = %v, want ErrNotFound", err)
	}
	users, err := env.store.SearchProjectMembers(env.ctx, store.SearchProjectMembersParams{ProjectID: env.projectID, Query: readonly.Username, Limit: 10})
	if err != nil || len(users) != 1 {
		t.Fatalf("all member search = %+v, %v", users, err)
	}
	writable, err := env.store.SearchProjectMembers(env.ctx, store.SearchProjectMembersParams{ProjectID: env.projectID, Query: readonly.Username, Limit: 10, WritableOnly: true})
	if err != nil || len(writable) != 0 {
		t.Fatalf("writable member search = %+v, %v", writable, err)
	}
}
