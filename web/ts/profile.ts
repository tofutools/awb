import type { User } from "./api.js";

/** Account roles are independent capabilities, so an administrator who holds
 * both sees both rather than one role hiding the other. */
export function accountRoles(user: Pick<User, "project_admin" | "user_admin">): string[] {
  const roles: string[] = [];
  if (user.user_admin) roles.push("User administrator");
  if (user.project_admin) roles.push("Project administrator");
  if (roles.length === 0) roles.push("Regular user");
  return roles;
}
