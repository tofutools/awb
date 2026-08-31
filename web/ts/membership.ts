import type { DirectoryUser, Membership, User } from "./api.js";
import type { Suggestion } from "./autocomplete.js";

/** Project administrators hold admin access everywhere without a membership
 * row; otherwise the caller needs an admin membership in this project. An
 * empty identity is the server's unrestricted bootstrap/direct-style mode. */
export function mayManageProjectMembership(
  identity: string,
  user: User | null,
  project: string,
  members: Membership[] = [],
): boolean {
  if (identity === "") return true;
  if (user?.project_admin === true) return true;
  return user?.projects.some((membership) =>
    membership.project === project && membership.access === "admin") === true
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

/** The add form never doubles as an access editor. Besides making duplicate
 * intent explicit, this prevents a stale page from recreating a membership
 * another administrator has just revoked. */
export function membershipAdditionError(user: string, members: Membership[]): string | null {
  const existing = members.find((membership) => membership.user === user);
  if (existing === undefined) return null;
  return `@${user} already has ${existing.access} access. `
    + "Remove that membership before granting different access.";
}

/** Membership changes deliberately allow the last stored administrator to
 * leave: a global project administrator or direct database mode is the
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
  const action = removing ? `remove @${member.user} from this project` : `change @${member.user} to regular access`;

  if (lastStoredAdmin) {
    const selfWarning = self ? " You will lose access to this membership page." : "";
    return `This is the last stored project administrator.${selfWarning} `
      + "Only a global project administrator or direct database access can restore project administration. "
      + `Do you want to ${action}?`;
  }
  if (self) return `Do you want to ${action}? You may lose access to this project.`;
  return `Do you want to ${action}?`;
}
