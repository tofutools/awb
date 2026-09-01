import { ApiError, type User, type UserPatch } from "./api.js";

/** Account roles are independent capabilities, so an administrator who holds
 * both sees both rather than one role hiding the other. */
export function accountRoles(user: Pick<User, "project_admin" | "user_admin">): string[] {
  const roles: string[] = [];
  if (user.user_admin) roles.push("User administrator");
  if (user.project_admin) roles.push("Workspace administrator");
  if (roles.length === 0) roles.push("Regular user");
  return roles;
}

export interface ProfileIdentity {
  heading: string;
  detail: string;
  updated: string;
}

/** profileIdentity is the profile text that changes with a successful name
 * edit. Keeping it together prevents the heading and timestamp from showing
 * different versions of the same account. */
export function profileIdentity(
  user: Pick<User, "name" | "full_name" | "updated_at">,
): ProfileIdentity {
  return {
    heading: user.full_name || `@${user.name}`,
    detail: user.full_name === ""
      ? "Your account and access"
      : `@${user.name} · Your account and access`,
    updated: user.updated_at,
  };
}

type UpdateUser = (name: string, patch: UserPatch) => Promise<User>;

export type FullNameSaveResult =
  | { ok: true; user: User; message: string }
  | { ok: false; user: User; message: string };

/** saveProfileFullName owns the user-visible result of one save. It returns
 * the original account on failure so rendering an error cannot partly apply
 * a value the server refused. */
export async function saveProfileFullName(
  user: User,
  fullName: string,
  updateUser: UpdateUser,
): Promise<FullNameSaveResult> {
  try {
    return {
      ok: true,
      user: await updateUser(user.name, { full_name: fullName }),
      message: "Full name saved.",
    };
  } catch (error) {
    return {
      ok: false,
      user,
      message: error instanceof ApiError ? error.message : String(error),
    };
  }
}
