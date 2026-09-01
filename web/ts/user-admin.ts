import type { DirectoryUser, User } from "./api.js";

export const userCreateHref = "#/users/-/new";

export function userEditorHref(name: string): string {
  return `#/users/${encodeURIComponent(name)}`;
}

/** Hash route segments remain escaped after splitting. Decode exactly once
 * before the API client performs its own path escaping. */
export function userNameFromRouteSegment(segment: string): string {
  return decodeURIComponent(segment);
}

export interface UserDeletionImpact {
  memberships: number;
  self: boolean;
  lastUserAdministrator: boolean;
}

/** Deletion deliberately follows the backend's existing lifecycle semantics:
 * it may remove the caller or the last user administrator. The typed-confirm
 * UI uses these facts to make those recovery consequences explicit. */
export function userDeletionImpact(
  user: User,
  directory: DirectoryUser[],
  identity: string,
): UserDeletionImpact {
  return {
    memberships: user.projects.length,
    self: user.name === identity,
    lastUserAdministrator: user.user_admin
      && directory.filter((candidate) => candidate.user_admin).length === 1,
  };
}

export function userDeletionWarning(user: User, impact: UserDeletionImpact): string {
  const membership = impact.memberships === 1 ? "1 project membership" : `${impact.memberships} project memberships`;
  const warnings = [`Deletes @${user.name} and ${membership}. Assigned issue history remains.`];
  if (impact.self) warnings.push("You are deleting your own account and will be signed out.");
  if (impact.lastUserAdministrator) {
    warnings.push("This is the last user administrator; only direct database access can restore account administration.");
  }
  warnings.push("This cannot be undone.");
  return warnings.join(" ");
}
