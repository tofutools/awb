import type { DirectoryUser, Membership, User } from "./api.js";
import type { Suggestion } from "./autocomplete.js";

/** Workspace administrators hold admin access everywhere without a membership
 * row; otherwise the caller needs an admin membership in this workspace. An
 * empty identity is the server's unrestricted bootstrap/direct-style mode. */
export function mayManageWorkspaceMembership(
  identity: string,
  user: User | null,
  workspace: string,
  members: Membership[] = [],
): boolean {
  if (identity === "") return true;
  if (user?.workspace_admin === true) return true;
  return user?.workspaces.some((membership) =>
    membership.workspace === workspace && membership.access === "admin") === true
    || members.some((membership) => membership.user === identity && membership.access === "admin");
}

/** Suggestions come from the already authorization-scoped user directory and
 * omit current members. Manual entry remains possible for an exact username. */
export function membershipSuggestions(users: DirectoryUser[], members: Membership[]): Suggestion[] {
  const current = new Set(members.map((membership) => membership.user));
  return users.filter((user) => !current.has(user.name)).map((user) => ({
    value: user.name,
    label: user.name,
    detail: user.full_name === "" ? undefined : user.full_name,
  }));
}

/** The add form never doubles as an access editor. Existing access is changed
 * explicitly in that member's row. */
export function membershipAdditionError(user: string, members: Membership[]): string | null {
  const existing = members.find((membership) => membership.user === user);
  if (existing === undefined) return null;
  return `@${user} already has ${existing.access} access. `
    + "Use that member's Access control to grant different access.";
}

/** Membership changes deliberately allow the last stored administrator to
 * leave: a global workspace administrator or direct database mode is the
 * documented recovery path. The UI makes that consequence explicit. */
export function membershipChangeConfirmation(
  member: Membership,
  members: Membership[],
  identity: string,
  nextAccess: Membership["access"] | null,
): string {
  const removing = nextAccess === null;
  const self = member.user === identity;
  const lastStoredAdmin = member.access === "admin"
    && nextAccess !== "admin"
    && members.filter((candidate) => candidate.access === "admin").length === 1;
  const action = removing
    ? `remove @${member.user} from this workspace`
    : `change @${member.user} to ${nextAccess} access`;

  if (lastStoredAdmin) {
    const selfWarning = self
      ? removing
        ? " You will lose access to this membership page."
        : " You will lose the membership management controls unless you are a global workspace administrator."
      : "";
    return `This is the last stored workspace administrator.${selfWarning} `
      + "Only a global workspace administrator or direct database access can restore workspace administration. "
      + `Do you want to ${action}?`;
  }
  if (self && removing) return `Do you want to ${action}? You may lose access to this workspace.`;
  if (self && member.access === "admin" && nextAccess === "regular") {
    return `Do you want to ${action}? You will lose workspace administration.`;
  }
  return `Do you want to ${action}?`;
}
