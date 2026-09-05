import type { JSX } from "preact";
import { useEffect, useRef, useState } from "preact/hooks";

import {
  api,
  ApiError,
  type DirectoryUser,
  type Membership,
  type Page,
  type User,
  type UserCreate,
  type Workspace,
  type WorkspaceActivity,
  type WorkspaceFilters,
  type WorkspacePreference,
  type UserFilters,
} from "../api.js";
import {
  Avatar,
  Button,
  ErrorMessage,
  Field,
  Loading,
  Markdown,
  MarkdownInput,
  NameLink,
  Pagination,
  SearchInput,
  UpdatedDisplayControl,
  UpdatedTime,
  confirmMutation,
  useApp,
  useMutation,
  useResource,
  listingPageSize,
} from "../components/ui.js";
import { pageNumber, pageWindow, sortState, nextSortValue, withPage } from "../listings.js";
import {
  filterWorkspacePreferences,
  preferenceStorage,
  readPaginationAutoHide,
  rememberPaginationAutoHide,
  workspacePreferenceSummary,
} from "../preferences.js";
import { accountRoles, profileIdentity, saveProfileFullName } from "../profile.js";
import {
  mayManageWorkspaceMembership,
  membershipAdditionError,
  membershipChangeConfirmation,
  membershipSuggestions,
} from "../membership.js";
import { Autocomplete } from "../components/autocomplete.js";
import { replaceRoute, routeHref, type Route } from "../routing/route.js";
import {
  userCreateHref,
  userDeletionImpact,
  userDeletionWarning,
  userEditorHref,
  userNameFromRouteSegment,
} from "../user-admin.js";

interface PageProps { route: Route }

const workspaceSortKeys = ["key", "active", "updated"] as const;
const workspaceColumns = [
  { key: "key", label: "Workspace" },
  { key: "active", label: "Open" },
  { key: "updated", label: "Updated" },
] as const;

function goWithQuery(route: Route, update: (query: URLSearchParams) => void): void {
  const query = new URLSearchParams(route.query);
  update(query);
  query.delete("page");
  location.hash = routeHref(route, query).slice(1);
}

function normalizePage(route: Route, total: number, size: number): number {
  const requested = pageNumber(route.query);
  const normalized = pageWindow(total, requested, size).page;
  if (requested !== normalized) {
    const query = withPage(route.query, normalized);
    replaceRoute(route, query);
  }
  return normalized;
}

function formValue(form: HTMLFormElement, name: string): string {
  return String(new FormData(form).get(name) ?? "");
}

function errorText(error: unknown): string {
  return error instanceof Error ? error.message : String(error);
}

function useWorkspaceManager(identity: string): ReturnType<typeof useResource<boolean>> {
  return useResource(async () => {
    if (identity === "") return true;
    try {
      return (await api.user(identity)).workspace_admin;
    } catch (error) {
      if (error instanceof ApiError && error.status === 404) return true;
      throw error;
    }
  }, [identity]);
}

function WorkspaceSortButton({ route, column }: { route: Route; column: typeof workspaceColumns[number] }) {
  const state = sortState(route.query.get("sort"), workspaceSortKeys, "key");
  const active = state.key === column.key;
  const title = active
    ? `Sorted by ${column.label}, ${state.direction === "asc" ? "ascending" : "descending"}`
    : `Sort by ${column.label}`;
  return <button type="button" class={`sort-button${active ? " active" : ""}`} title={title} onClick={() => {
    goWithQuery(route, (query) => {
      const next = nextSortValue(query.get("sort"), column.key, workspaceSortKeys, "key");
      if (next === null) query.delete("sort"); else query.set("sort", next);
    });
  }}>
    {column.label}<span class={`sort-arrow${active ? "" : " sort-hint"}`} aria-hidden="true">
      {active ? state.direction === "asc" ? "▲" : "▼" : "↕"}
    </span>
  </button>;
}

function MobileWorkspaceSort({ route }: { route: Route }) {
  const values = new Set(workspaceSortKeys.flatMap((key) => [key, `-${key}`]));
  const value = values.has(route.query.get("sort") ?? "") ? route.query.get("sort") ?? "" : "";
  return <label class="mobile-sort-control">Sort
    <select aria-label="Sort listing" value={value} onChange={(event) => {
      const next = event.currentTarget.value;
      goWithQuery(route, (query) => {
        if (next === "") query.delete("sort"); else query.set("sort", next);
      });
    }}>
      <option value="">Natural order</option>
      {workspaceColumns.flatMap((column) => [
        <option value={column.key}>{column.label} ▲</option>,
        <option value={`-${column.key}`}>{column.label} ▼</option>,
      ])}
    </select>
  </label>;
}

function WorkspaceTable({ route, workspaces }: { route: Route; workspaces: Workspace[] }) {
  const state = sortState(route.query.get("sort"), workspaceSortKeys, "key");
  return <table class="listing-table workspace-table">
    <thead><tr>{workspaceColumns.map((column) => <th scope="col" aria-sort={state.key === column.key
      ? state.direction === "asc" ? "ascending" : "descending"
      : undefined}>
      <div class="column-heading">
        <WorkspaceSortButton route={route} column={column} />
        {column.key === "updated" && <UpdatedDisplayControl />}
      </div>
    </th>)}</tr></thead>
    <tbody>{workspaces.map((workspace) => <tr key={workspace.key}>
      <td data-label="Workspace">
        <NameLink href={`#/workspaces/${encodeURIComponent(workspace.key)}`} id={workspace.key} title={workspace.name} />
        {workspace.state === "archived" && <span class="listing-badge archived-badge">Archived</span>}
        {workspace.description !== "" && <Markdown text={workspace.description} className="workspace-description" />}
      </td>
      <td data-label="Open"><span class="open-count">{workspace.active_issues}</span></td>
      <td data-label="Updated"><UpdatedTime timestamp={workspace.updated_at} /></td>
    </tr>)}</tbody>
  </table>;
}

function WorkspaceCreateForm({ hidden }: { hidden: boolean }) {
  const [key, setKey] = useState("");
  const mutation = useMutation();
  const formRef = useRef<HTMLFormElement>(null);
  useEffect(() => { if (!hidden) formRef.current?.querySelector<HTMLInputElement>("input")?.focus(); }, [hidden]);
  return <form ref={formRef} class="edit-panel workspace-create-panel" id="workspace-creator" hidden={hidden} aria-busy={mutation.busy || undefined}
    onSubmit={async (event) => {
      event.preventDefault();
      const form = event.currentTarget;
      let created: Workspace | undefined;
      const ok = await mutation.run(async () => {
        created = await api.createWorkspace({
          key: formValue(form, "key"),
          name: formValue(form, "name"),
          description: formValue(form, "description"),
        });
      });
      if (ok && created !== undefined) location.hash = `#/workspaces/${encodeURIComponent(created.key)}`;
    }}>
    <h2>Create workspace</h2>
    <p class="muted">The key becomes every issue ID prefix in this workspace and cannot be changed. Issues cannot move between workspaces.</p>
    <Field label="Key"><input name="key" required maxLength={16} pattern="[a-z][a-z0-9-]*" value={key}
      onInput={(event) => setKey(event.currentTarget.value)} /></Field>
    <p class="workspace-key-preview muted">{key === "" ? "Issue IDs will use this key as their prefix." : `Issue IDs will start with ${key}-.`}</p>
    <Field label="Name (optional)"><input name="name" maxLength={500} /></Field>
    <Field label="Description (Markdown)"><MarkdownInput name="description" value="" label="Workspace description (Markdown)" /></Field>
    <Button type="submit" class="primary-button" disabled={mutation.busy}>Create workspace</Button>
    {mutation.error !== undefined && <ErrorMessage error={mutation.error} />}
  </form>;
}

async function loadWorkspacePage(route: Route): Promise<Page<Workspace>> {
  const size = listingPageSize(route.query);
  const requested = pageNumber(route.query);
  const lifecycle = route.query.get("state") === "archived" ? "archived" : "active";
  const filters: WorkspaceFilters = { limit: size, offset: (requested - 1) * size, state: lifecycle };
  const filter = route.query.get("filter");
  if (filter !== null && filter !== "") filters.filter = filter;
  const sort = route.query.get("sort");
  if (sort !== null && workspaceSortKeys.flatMap((key) => [key, `-${key}`]).includes(sort)) {
    filters.sort = sort as WorkspaceFilters["sort"];
  }
  let page = await api.workspaces(filters);
  const normalized = normalizePage(route, page.total, size);
  if (filters.offset !== (normalized - 1) * size) {
    filters.offset = (normalized - 1) * size;
    page = await api.workspaces(filters);
  }
  return page;
}

export function WorkspacesPage({ route }: PageProps) {
  const { identity } = useApp();
  const manager = useWorkspaceManager(identity);
  const [creatorOpen, setCreatorOpen] = useState(false);
  const [filter, setFilter] = useState(route.query.get("filter") ?? "");
  const dependency = route.query.toString();
  const resource = useResource(() => loadWorkspacePage(route), [dependency]);
  const lifecycle = route.query.get("state") === "archived" ? "archived" : "active";

  useEffect(() => setFilter(route.query.get("filter") ?? ""), [dependency]);
  useEffect(() => {
    if (filter === (route.query.get("filter") ?? "")) return;
    const timer = setTimeout(() => {
      const query = new URLSearchParams(route.query);
      query.delete("page");
      if (filter === "") query.delete("filter"); else query.set("filter", filter);
      replaceRoute(route, query);
    }, filter === "" ? 0 : 200);
    return () => clearTimeout(timer);
  }, [filter]);
  if (resource.error !== undefined) return <ErrorMessage error={resource.error} />;
  if (manager.error !== undefined) return <ErrorMessage error={manager.error} />;
  if (resource.data === undefined || manager.data === undefined) return <Loading />;
  const page = resource.data;
  const tabHref = (state: "active" | "archived") => {
    const query = new URLSearchParams(route.query);
    query.delete("page");
    if (state === "active") query.delete("state"); else query.set("state", state);
    const suffix = query.toString();
    return `#/workspaces${suffix === "" ? "" : `?${suffix}`}`;
  };
  return <div>
    <div class="workspaces-heading"><h1>Workspaces</h1>{manager.data && <Button class="primary-button"
      aria-controls="workspace-creator" aria-expanded={creatorOpen} onClick={() => setCreatorOpen(!creatorOpen)}>
      {creatorOpen ? "Hide creator" : "New workspace"}
    </Button>}</div>
    <WorkspaceCreateForm hidden={!creatorOpen} />
    <div class="workspace-state-tabs">
      <a href={tabHref("active")} class={lifecycle === "active" ? "active" : undefined} aria-current={lifecycle === "active" ? "page" : undefined}>Active</a>
      <a href={tabHref("archived")} class={lifecycle === "archived" ? "active" : undefined} aria-current={lifecycle === "archived" ? "page" : undefined}>Archived</a>
    </div>
    <div class="listing">
      <div class="listing-tools">
        <SearchInput value={filter} placeholder="Filter all workspaces…" onInput={setFilter} />
        <span class="filter-count">{page.total} workspace{page.total === 1 ? "" : "s"}</span>
        <div class="listing-actions"><MobileWorkspaceSort route={route} /><span class="mobile-updated-display">Updated<UpdatedDisplayControl /></span><Pagination route={route} total={page.total} /></div>
      </div>
      <div class="listing-host">{page.rows.length === 0
        ? <p class="empty">{route.query.get("filter") !== null ? "No workspaces match this filter." : lifecycle === "archived"
          ? "No archived workspaces." : "No workspaces yet. Create one above or with: awb workspace create <key>"}</p>
        : <WorkspaceTable route={route} workspaces={page.rows} />}</div>
    </div>
  </div>;
}

function UserTable({ users, manageable }: { users: DirectoryUser[]; manageable: boolean }) {
  return <table class="listing-table user-table">
    <thead><tr>{["User", "Memberships", "Activity", "Roles"].map((label) => <th scope="col">{label}</th>)}</tr></thead>
    <tbody>{users.map((user) => <tr key={user.name}>
      <td data-label="User">{manageable
        ? <a class="user-name" href={userEditorHref(user.name)}><Avatar name={user.name} /><UserIdentity user={user} /></a>
        : <span class="user-name"><Avatar name={user.name} /><UserIdentity user={user} /></span>}</td>
      <td data-label="Memberships"><div class="user-workspaces">{user.workspaces.length === 0 ? <span class="muted">—</span>
        : user.workspaces.map((membership) => <span class="listing-badge user-workspace" title={`${membership.access} access`}>{membership.workspace}</span>)}</div></td>
      <td data-label="Activity"><div class="user-workspaces">{user.activity_workspaces.length === 0 ? <span class="muted">—</span>
        : user.activity_workspaces.map((workspace) => <span class="listing-badge user-workspace">{workspace}</span>)}</div></td>
      <td data-label="Roles"><div class="user-roles">
        {user.workspace_admin && <span class="listing-badge">workspace admin</span>}
        {user.user_admin && <span class="listing-badge">user admin</span>}
        {!user.workspace_admin && !user.user_admin && <span class="muted">member</span>}
      </div></td>
    </tr>)}</tbody>
  </table>;
}

function UserIdentity({ user }: { user: DirectoryUser }) {
  return <span class="user-identity"><span class="user-full-name">{user.full_name || user.name}</span>
    {user.full_name !== "" && <span class="muted">@{user.name}</span>}</span>;
}

async function loadUsers(route: Route): Promise<Page<DirectoryUser>> {
  const focused = route.query.get("user");
  if (focused !== null) {
    const results = await api.navigation(focused);
    const rows = results.users.filter((user) => user.name === focused);
    return { rows, total: rows.length };
  }
  const size = listingPageSize(route.query);
  const filters: UserFilters = { limit: size, offset: (pageNumber(route.query) - 1) * size };
  const filter = route.query.get("filter");
  if (filter !== null && filter !== "") filters.filter = filter;
  let page = await api.users(filters);
  const normalized = normalizePage(route, page.total, size);
  if (filters.offset !== (normalized - 1) * size) {
    filters.offset = (normalized - 1) * size;
    page = await api.users(filters);
  }
  return page;
}

export function UsersPage({ route }: PageProps) {
  const app = useApp();
  const dependency = route.query.toString();
  const [filter, setFilter] = useState(route.query.get("filter") ?? "");
  useEffect(() => setFilter(route.query.get("filter") ?? ""), [dependency]);
  useEffect(() => {
    if (filter === (route.query.get("filter") ?? "")) return;
    const timer = setTimeout(() => {
      const query = new URLSearchParams(route.query);
      query.delete("page"); query.delete("user");
      if (filter === "") query.delete("filter"); else query.set("filter", filter);
      replaceRoute(route, query);
    }, filter === "" ? 0 : 200);
    return () => clearTimeout(timer);
  }, [filter]);
  const resource = useResource(async () => {
    await app.refreshCaller();
    return loadUsers(route);
  }, [dependency]);
  if (resource.error !== undefined) return <ErrorMessage error={resource.error} />;
  if (resource.data === undefined) return <Loading />;
  return <div>
    <div class="directory-heading"><h1>Users</h1>{app.mayManageUsers && <a href={userCreateHref} class="primary-button">Add user</a>}</div>
    <div class="listing">
      <div class="listing-tools">
        <SearchInput value={filter} placeholder="Filter all users…" onInput={setFilter} />
        <span class="filter-count">{resource.data.total} user{resource.data.total === 1 ? "" : "s"}</span>
        <Pagination route={route} total={resource.data.total} />
      </div>
      <div class="listing-host">{resource.data.rows.length === 0
        ? <p class="empty">{route.query.get("filter") === null ? "No users yet." : "No users match this filter."}</p>
        : <UserTable users={resource.data.rows} manageable={app.mayManageUsers} />}</div>
    </div>
  </div>;
}

function FormStatus({ busy, busyText, error, success }: { busy: boolean; busyText: string; error?: unknown; success?: string }) {
  return <p class={`profile-form-message${error === undefined ? "" : " form-error"}`} aria-live="polite" role={error === undefined ? undefined : "alert"}>
    {busy ? busyText : error === undefined ? success : errorText(error)}
  </p>;
}

function UserAccountForm({ user, directory, reload }: { user: User; directory: DirectoryUser[]; reload: () => Promise<void> }) {
  const app = useApp();
  const [fullName, setFullName] = useState(user.full_name);
  const [workspaceAdmin, setWorkspaceAdmin] = useState(user.workspace_admin);
  const [userAdmin, setUserAdmin] = useState(user.user_admin);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<unknown>();
  const lastUserAdmin = user.user_admin && directory.filter((candidate) => candidate.user_admin).length === 1;
  useEffect(() => {
    setFullName(user.full_name); setWorkspaceAdmin(user.workspace_admin); setUserAdmin(user.user_admin);
  }, [user]);
  return <form class="user-admin-form" aria-busy={busy || undefined} onSubmit={async (event) => {
    event.preventDefault();
    if (lastUserAdmin && !userAdmin && !await confirmMutation(
      "Remove last user administrator?",
      "Only direct database access can restore account administration.",
      undefined,
      true,
    )) return;
    setBusy(true); setError(undefined);
    try {
      const updated = await api.updateUser(user.name, {
        full_name: fullName,
        workspace_admin: workspaceAdmin,
        user_admin: userAdmin,
      });
      await app.refreshCaller();
      app.notify(`@${user.name} was updated.`);
      // Context updates take effect on the next render. Use the mutation's
      // returned account when this edit removes the caller's own permission.
      if (updated.name === app.identity && !updated.user_admin) location.hash = "#/users";
      else await reload();
    } catch (caught) {
      if (caught instanceof ApiError && caught.status === 412) {
        app.notify("This account changed elsewhere. The latest values have been loaded; review them and try again.", true);
        await reload();
      } else setError(caught);
    } finally { setBusy(false); }
  }}>
    <Field label="Full name"><input maxLength={500} autoComplete="name" value={fullName} onInput={(event) => setFullName(event.currentTarget.value)} /></Field>
    <label class="check-field user-role-field"><input type="checkbox" checked={workspaceAdmin} onChange={(event) => setWorkspaceAdmin(event.currentTarget.checked)} /><span>Workspace administrator</span></label>
    <label class="check-field user-role-field"><input type="checkbox" checked={userAdmin} onChange={(event) => setUserAdmin(event.currentTarget.checked)} /><span>User administrator</span></label>
    <p class="profile-form-help">{lastUserAdmin
      ? "This is the last user administrator. Removing that role leaves account administration available only through direct database access."
      : "Administrative roles are independent; grant only what this account needs."}</p>
    <Button type="submit" class="primary-button" disabled={busy}>Save changes</Button>
    <FormStatus busy={busy} busyText="Saving…" error={error} />
  </form>;
}

function UserPasswordResetForm({ user, reload }: { user: User; reload: () => Promise<void> }) {
  const app = useApp();
  const [password, setPassword] = useState("");
  const [confirmation, setConfirmation] = useState("");
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<unknown>();
  const [success, setSuccess] = useState("");
  const confirmationRef = useRef<HTMLInputElement>(null);
  return <form class="user-admin-form" aria-busy={busy || undefined} onSubmit={async (event) => {
    event.preventDefault(); setError(undefined); setSuccess("");
    if (password !== confirmation) {
      setError(new Error("The passwords do not match.")); confirmationRef.current?.focus(); return;
    }
    setBusy(true);
    try {
      await api.updateUser(user.name, { password });
      setPassword(""); setConfirmation(""); setSuccess("Password reset.");
    } catch (caught) {
      if (caught instanceof ApiError && caught.status === 412) {
        app.notify("This account changed elsewhere. The latest values have been loaded; review them and try again.", true);
        await reload();
      } else setError(caught);
    } finally { setBusy(false); }
  }}>
    <p class="profile-form-help">Set a new password. The current password is never shown.</p>
    <Field label="New password"><input type="password" required maxLength={72} autoComplete="new-password" value={password} onInput={(event) => setPassword(event.currentTarget.value)} /></Field>
    <Field label="Confirm new password"><input ref={confirmationRef} type="password" required maxLength={72} autoComplete="new-password" value={confirmation} onInput={(event) => setConfirmation(event.currentTarget.value)} /></Field>
    <Button type="submit" class="secondary-button" disabled={busy}>Reset password</Button>
    <FormStatus busy={busy} busyText="Resetting…" error={error} success={success} />
  </form>;
}

function UserMembershipList({ user }: { user: User }) {
  return <ul class="profile-workspaces">{user.workspaces.length === 0 ? <li class="empty">No workspace memberships.</li>
    : user.workspaces.map((membership) => <li class="profile-workspace" key={membership.workspace}>
      <a href={`#/workspaces/${encodeURIComponent(membership.workspace)}/members`} class="profile-workspace-name">{membership.workspace}</a>
      <span class="profile-workspace-title">Workspace Members page</span>
      <span class="listing-badge">{membership.access}</span>
    </li>)}</ul>;
}

function UserDeleteForm({ user, directory, reload }: { user: User; directory: DirectoryUser[]; reload: () => Promise<void> }) {
  const { identity, notify } = useApp();
  const [confirmation, setConfirmation] = useState("");
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<unknown>();
  const impact = userDeletionImpact(user, directory, identity);
  return <form class="user-delete-form" aria-busy={busy || undefined} onSubmit={async (event) => {
    event.preventDefault();
    if (confirmation !== user.name) return;
    setBusy(true); setError(undefined);
    try {
      await api.deleteUser(user.name);
      notify(`@${user.name} was deleted.`);
      location.hash = "#/users";
    } catch (caught) {
      if (caught instanceof ApiError && caught.status === 412) {
        notify("This account changed elsewhere. The latest values have been loaded; review them and try again.", true);
        await reload();
      } else setError(caught);
    } finally { setBusy(false); }
  }}>
    <p class="user-delete-warning">{userDeletionWarning(user, impact)}</p>
    <p class="profile-form-help">Type {user.name} to confirm.</p>
    <Field label="Username"><input autoComplete="off" placeholder={user.name} value={confirmation} onInput={(event) => setConfirmation(event.currentTarget.value)} /></Field>
    <Button type="submit" class="danger-button" disabled={busy || confirmation !== user.name}>Delete user</Button>
    <FormStatus busy={busy} busyText="Deleting…" error={error} />
  </form>;
}

export function UserPage({ route }: PageProps) {
  const app = useApp();
  const name = userNameFromRouteSegment(route.path[1] ?? "");
  const resource = useResource(async () => {
    await app.refreshCaller();
    if (!app.mayManageUsers) throw new ApiError(403, "Only a user administrator may administer accounts.");
    const [user, directory] = await Promise.all([api.user(name), api.users({})]);
    return { user, directory: directory.rows };
  }, [name, app.mayManageUsers]);
  if (resource.error !== undefined) return <ErrorMessage error={resource.error} />;
  if (resource.data === undefined) return <Loading />;
  const { user, directory } = resource.data;
  return <div class="profile-view user-admin-view">
    <a href="#/users" class="detail-back-link">← Users</a>
    <div class="profile-heading user-admin-heading"><Avatar name={user.name} className="profile-avatar" /><div>
      <h1>{user.full_name || `@${user.name}`}</h1>
      <p class="lede">{user.full_name === "" ? "Account administration" : `@${user.name} · Account administration`}</p>
    </div></div>
    <div class="user-admin-grid">
      <section class="profile-card"><h2>Account details</h2><UserAccountForm user={user} directory={directory} reload={resource.reload} /></section>
      <section class="profile-card"><h2>Reset password</h2><UserPasswordResetForm user={user} reload={resource.reload} /></section>
      <section class="profile-card"><h2>Workspaces</h2><p class="profile-form-help">Workspace memberships are read-only here. Manage access on each workspace's Members page.</p><UserMembershipList user={user} /></section>
      <section class="profile-card"><h2>Account information</h2><dl class="profile-facts">
        <dt>Username</dt><dd>{user.name}</dd><dt>Created</dt><dd>{user.created_at}</dd><dt>Updated</dt><dd>{user.updated_at}</dd>
      </dl></section>
      <section class="profile-card user-delete-card"><h2>Delete account</h2><UserDeleteForm user={user} directory={directory} reload={resource.reload} /></section>
    </div>
  </div>;
}

export function UserCreatePage({ route: _route }: PageProps) {
  const app = useApp();
  const [password, setPassword] = useState("");
  const [confirmation, setConfirmation] = useState("");
  const [mismatch, setMismatch] = useState(false);
  const confirmationRef = useRef<HTMLInputElement>(null);
  const mutation = useMutation();
  const access = useResource(async () => {
    await app.refreshCaller();
    if (!app.mayManageUsers) throw new ApiError(403, "Only a user administrator may administer accounts.");
    return true;
  }, [app.mayManageUsers]);
  if (access.error !== undefined) return <ErrorMessage error={access.error} />;
  if (access.data === undefined) return <Loading />;
  return <div class="profile-view user-admin-view">
    <a href="#/users" class="detail-back-link">← Users</a>
    <div class="settings-heading"><h1>Add user</h1><p class="lede">Create an account and its initial roles.</p></div>
    <section class="profile-card user-create-card"><h2>Account</h2>
      <form class="user-admin-form user-create-form" aria-busy={mutation.busy || undefined} onSubmit={async (event) => {
        event.preventDefault();
        setMismatch(false);
        if (password !== confirmation) { setMismatch(true); confirmationRef.current?.focus(); return; }
        const form = event.currentTarget;
        let created: User | undefined;
        const body: UserCreate = {
          name: formValue(form, "name"), full_name: formValue(form, "full_name"), password,
          workspace_admin: new FormData(form).has("workspace_admin"),
          user_admin: new FormData(form).has("user_admin"),
        };
        const ok = await mutation.run(async () => { created = await api.createUser(body); });
        if (ok && created !== undefined) {
          app.notify(`@${created.name} was created.`);
          location.hash = userEditorHref(created.name);
        }
      }}>
        <Field label="Username"><input name="name" required maxLength={64} autoComplete="username" /></Field>
        <Field label="Full name"><input name="full_name" maxLength={500} autoComplete="name" /></Field>
        <Field label="Password"><input name="password" type="password" required maxLength={72} autoComplete="new-password" value={password} onInput={(event) => setPassword(event.currentTarget.value)} /></Field>
        <Field label="Confirm password"><input ref={confirmationRef} type="password" required maxLength={72} autoComplete="new-password" value={confirmation} onInput={(event) => setConfirmation(event.currentTarget.value)} /></Field>
        <label class="check-field user-role-field"><input name="workspace_admin" type="checkbox" /><span>Workspace administrator</span></label>
        <label class="check-field user-role-field"><input name="user_admin" type="checkbox" /><span>User administrator</span></label>
        <Button type="submit" class="primary-button" disabled={mutation.busy}>Create user</Button>
        <FormStatus
          busy={mutation.busy}
          busyText="Creating…"
          error={mismatch ? new Error("The passwords do not match.") : mutation.error}
        />
      </form>
    </section>
  </div>;
}

interface WorkspaceBundle {
  workspace: Workspace;
  activity: WorkspaceActivity[];
  members: Membership[];
  currentUser: User | null;
  canManage: boolean;
}

function useWorkspaceBundle(key: string) {
  const { identity } = useApp();
  return useResource<WorkspaceBundle>(async () => {
    const [workspace, activity, members, currentUser] = await Promise.all([
      api.workspace(key), api.workspaceActivity(key), api.workspaceMembers(key),
      identity === "" ? Promise.resolve(null) : api.user(identity),
    ]);
    let canManage = identity === "";
    if (!canManage) canManage = currentUser?.workspace_admin === true;
    return { workspace, activity: activity.rows, members: members.rows, currentUser, canManage };
  }, [key, identity]);
}

function WorkspaceEditForm({ workspace, reload, hidden }: { workspace: Workspace; reload: () => Promise<void>; hidden: boolean }) {
  const [name, setName] = useState(workspace.name);
  const mutation = useMutation();
  const formRef = useRef<HTMLFormElement>(null);
  useEffect(() => setName(workspace.name), [workspace]);
  useEffect(() => { if (!hidden) formRef.current?.querySelector<HTMLInputElement>("input")?.focus(); }, [hidden]);
  return <form ref={formRef} class="edit-panel workspace-edit-form" hidden={hidden} aria-busy={mutation.busy || undefined} onSubmit={async (event) => {
    event.preventDefault();
    const description = formValue(event.currentTarget, "description");
    if (await mutation.run(() => api.updateWorkspace(workspace.key, { name, description }))) await reload();
  }}>
    <h2>Edit workspace</h2>
    <Field label="Name"><input value={name} maxLength={500} onInput={(event) => setName(event.currentTarget.value)} /></Field>
    <Field label="Description (Markdown)"><MarkdownInput name="description" value={workspace.description} label="Workspace description (Markdown)" /></Field>
    <Button type="submit" class="primary-button" disabled={mutation.busy}>Save changes</Button>
    {mutation.error !== undefined && <ErrorMessage error={mutation.error} />}
  </form>;
}

function WorkspaceLifecycleCard({ workspace, activity, canManage, reload }: {
  workspace: Workspace; activity: WorkspaceActivity[]; canManage: boolean; reload: () => Promise<void>;
}) {
  const mutation = useMutation();
  const [confirmation, setConfirmation] = useState("");
  const mutate = async (operation: () => Promise<unknown>) => {
    if (await mutation.run(operation)) await reload();
  };
  return <section class="workspace-lifecycle-card" aria-busy={mutation.busy || undefined}>
    <h2>Lifecycle</h2>
    {workspace.state === "archived" ? <>
      <p>Issues, comments, attachments, transitions and relations are read-only while this workspace is archived. Issues remain in this workspace and cannot be transferred elsewhere.</p>
      {workspace.archived_at !== "" && <p class="muted">Archived{workspace.archived_by === "" ? "" : ` by @${workspace.archived_by}`} · <UpdatedTime timestamp={workspace.archived_at} /></p>}
      {canManage && <Button class="primary-button" disabled={mutation.busy} onClick={() => void mutate(() => api.restoreWorkspace(workspace.key))}>Restore workspace</Button>}
    </> : <>
      <p>Archive this workspace to remove it from everyday discovery and make its retained work read-only. Its issues keep their stable workspace-prefixed IDs.</p>
      {canManage && <form class="workspace-archive-form" onSubmit={(event) => {
        event.preventDefault(); if (confirmation === workspace.key) void mutate(() => api.archiveWorkspace(workspace.key));
      }}>
        <label>Type {workspace.key} to confirm</label>
        <input placeholder={workspace.key} aria-label={`Type ${workspace.key} to confirm`} value={confirmation} onInput={(event) => setConfirmation(event.currentTarget.value)} />
        <Button type="submit" class="danger-button archive-button" disabled={mutation.busy || confirmation !== workspace.key}>Archive workspace</Button>
      </form>}
    </>}
    {mutation.error !== undefined && <ErrorMessage error={mutation.error} />}
    {activity.length > 0 && <><h3>Lifecycle history</h3><ol class="workspace-lifecycle-history">
      {activity.map((entry) => <li>{entry.action === "archived" ? "Archived" : "Restored"}{entry.actor === "" ? "" : ` by @${entry.actor}`} · <UpdatedTime timestamp={entry.created_at} /></li>)}
    </ol></>}
  </section>;
}

function WorkspaceMembershipSection({ workspace, members, currentUser, reload }: {
  workspace: Workspace; members: Membership[]; currentUser: User | null; reload: () => Promise<void>;
}) {
  const { identity, notify } = useApp();
  const manageable = mayManageWorkspaceMembership(identity, currentUser, workspace.key, members);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<unknown>();
  const [newUser, setNewUser] = useState("");
  const [newAccess, setNewAccess] = useState<Membership["access"]>("regular");
  const change = async (operation: () => Promise<unknown>, success: string, options: {
    refreshNotFound?: boolean; refreshConflict?: boolean; redirect?: boolean;
  } = {}) => {
    setBusy(true); setError(undefined);
    try {
      await operation(); notify(success);
      if (options.redirect) location.hash = "#/workspaces"; else await reload();
    } catch (caught) {
      if (options.refreshNotFound && caught instanceof ApiError && caught.status === 404) {
        notify("Membership changed elsewhere. The current member list has been reloaded.", true); await reload();
      } else if (options.refreshConflict && caught instanceof ApiError && caught.status === 409) {
        notify("That user was added elsewhere. The current member list has been reloaded.", true); await reload();
      } else setError(caught);
    } finally { setBusy(false); }
  };
  return <section class="profile-card membership-card" aria-busy={busy || undefined}>
    <div class="membership-heading"><div><h2>Workspace members</h2><p class="membership-help">Membership grants access to this workspace. It is separate from each user's ignored-workspace preference.</p></div><span class="membership-count">{members.length}</span></div>
    {manageable ? <form class="compact-editor membership-editor" onSubmit={(event) => {
      event.preventDefault();
      const user = newUser.trim();
      const duplicate = membershipAdditionError(user, members);
      if (duplicate !== null) { setError(new Error(duplicate)); return; }
      void change(() => api.addWorkspaceMember(workspace.key, user, newAccess),
        `@${user} was added with ${newAccess} access to workspace ${workspace.key}.`, { refreshConflict: true });
    }}>
      <Autocomplete required maxLength={64} placeholder="Search users…" aria-label="User to add" value={newUser} onValue={setNewUser}
        load={async (query, signal) => membershipSuggestions((await api.users({ filter: query, limit: 8 }, signal)).rows, members)} />
      <select aria-label="Access to grant" value={newAccess} onChange={(event) => setNewAccess(event.currentTarget.value as Membership["access"])}>
        <option value="regular">Regular access</option><option value="admin">Administrator</option>
      </select>
      <Button type="submit" class="primary-button" disabled={busy}>Add member</Button>
    </form> : <p class="membership-help">Workspace administrators can change membership and access.</p>}
    {error !== undefined && <ErrorMessage error={error} />}
    {members.length === 0 ? <p class="empty">No stored members. Global workspace administrators still have access.</p>
      : <table class="listing-table membership-table"><thead><tr><th scope="col">User</th><th scope="col">Access</th><th scope="col">Actions</th></tr></thead>
        <tbody>{members.map((member) => <tr key={member.user}>
          <td data-label="User"><span class="user-name"><Avatar name={member.user} /><span class="user-identity">@{member.user}</span></span></td>
          <td data-label="Access">{manageable ? <select aria-label={`Access for @${member.user}`} value={member.access} disabled={busy} onChange={async (event) => {
            const select = event.currentTarget;
            const next = select.value as Membership["access"];
            if (next === member.access) return;
            if (!await confirmMutation("Change workspace access?", membershipChangeConfirmation(member, members, identity, next))) {
              select.value = member.access; return;
            }
            await change(() => api.setWorkspaceMember(workspace.key, member.user, next), `@${member.user} now has ${next} access to workspace ${workspace.key}.`);
          }}><option value="regular">Regular access</option><option value="admin">Administrator</option></select>
            : <span class="listing-badge">{member.access}</span>}</td>
          <td data-label="Actions">{manageable ? <Button class="danger-button membership-remove" aria-label={`Remove @${member.user} from workspace ${workspace.key}`} disabled={busy} onClick={async () => {
            if (!await confirmMutation("Remove workspace member?", membershipChangeConfirmation(member, members, identity, null), undefined, true)) return;
            const losesAccess = member.user === identity && currentUser?.workspace_admin !== true;
            await change(() => api.removeWorkspaceMember(workspace.key, member.user), `@${member.user} was removed from workspace ${workspace.key}.`, { refreshNotFound: true, redirect: losesAccess });
          }}>Remove</Button> : <span class="muted">—</span>}</td>
        </tr>)}</tbody></table>}
  </section>;
}

export function WorkspacePage({ route }: PageProps) {
  const key = decodeURIComponent(route.path[1] ?? "");
  const resource = useWorkspaceBundle(key);
  const [editorOpen, setEditorOpen] = useState(false);
  if (resource.error !== undefined) return <ErrorMessage error={resource.error} />;
  if (resource.data === undefined) return <Loading />;
  const { workspace, activity, members, currentUser, canManage } = resource.data;
  return <div class="workspace-view">
    {workspace.state === "archived" && <div class="workspace-archive-banner" role="status"><strong>Archived</strong>This workspace is retained as read-only history. Restore it to resume work inside the same workspace boundary.</div>}
    <div class="detail-heading"><div><div class="issue-key">{workspace.key}</div><h1>{workspace.name}</h1></div>
      {canManage && workspace.state === "active" && <Button onClick={() => setEditorOpen(!editorOpen)}>{editorOpen ? "Hide editor" : "Edit workspace"}</Button>}</div>
    <WorkspaceEditForm workspace={workspace} reload={resource.reload} hidden={!editorOpen} />
    <section class="workspace-detail-description"><h2>Description</h2>{workspace.description === "" ? <p class="empty">No description.</p> : <Markdown text={workspace.description} />}</section>
    <p class="workspace-facts">{workspace.active_issues} open issue{workspace.active_issues === 1 ? "" : "s"} · Updated <UpdatedTime timestamp={workspace.updated_at} /></p>
    <a class="action" href={`#/issues?workspace=${encodeURIComponent(workspace.key)}${workspace.state === "archived" ? "&include-archived=true&include-closed=true" : ""}`}>
      {workspace.state === "archived" ? "View this workspace's historical issues" : "View this workspace's issues"}
    </a>
    <WorkspaceLifecycleCard workspace={workspace} activity={activity} canManage={canManage} reload={resource.reload} />
    <WorkspaceMembershipSection workspace={workspace} members={members} currentUser={currentUser} reload={resource.reload} />
  </div>;
}

export function WorkspaceMembersPage({ route }: PageProps) {
  const { identity } = useApp();
  const key = decodeURIComponent(route.path[1] ?? "");
  const resource = useResource(async () => {
    if (identity === "") throw new Error("No authenticated user is available.");
    const [preferences, members, currentUser] = await Promise.all([
      api.workspacePreferences(), api.workspaceMembers(key), api.user(identity),
    ]);
    const preference = preferences.find((candidate) => candidate.workspace.key === key);
    if (preference === undefined) throw new ApiError(404, `no such workspace: ${key}`);
    return { preference, members: members.rows, currentUser };
  }, [key, identity]);
  if (resource.error !== undefined) return <ErrorMessage error={resource.error} />;
  if (resource.data === undefined) return <Loading />;
  const { preference, members, currentUser } = resource.data;
  return <div class="workspace-view membership-admin-view">
    <div class="detail-heading"><div><div class="issue-key">{preference.workspace.key}</div><h1>{preference.workspace.name}</h1>
      <p class="lede">{preference.ignored ? "Ignored workspace administration" : "Workspace administration"}</p></div>
      <a href="#/settings" class="secondary-button">Back to settings</a></div>
    <WorkspaceMembershipSection workspace={preference.workspace} members={members} currentUser={currentUser} reload={resource.reload} />
  </div>;
}

function messageFor(error: unknown): string {
  return error instanceof ApiError ? error.message : String(error);
}

function ProfileWorkspaceList({ user, workspaces }: { user: User; workspaces: Workspace[] }): JSX.Element {
  const memberships = new Map(
    user.workspaces.map((membership) => [membership.workspace, membership.access]),
  );

  return (
    <ul class="profile-workspaces">
      {workspaces.map((workspace) => {
        const query = new URLSearchParams({ workspace: workspace.key });
        const access = user.workspace_admin ? "admin" : memberships.get(workspace.key);
        return (
          <li class="profile-workspace" key={workspace.key}>
            <a class="profile-workspace-name" href={`#/issues?${query.toString()}`}>
              {workspace.key}
            </a>
            {workspace.name !== "" && <span class="profile-workspace-title">{workspace.name}</span>}
            {access !== undefined && <span class="listing-badge">{access}</span>}
          </li>
        );
      })}
      {workspaces.length === 0 && <li class="empty">No workspace access.</li>}
    </ul>
  );
}

function FullNameForm({ user, onUpdated }: { user: User; onUpdated: (user: User) => void }): JSX.Element {
  const mutation = useMutation();
  const [fullName, setFullName] = useState(user.full_name);
  const [message, setMessage] = useState("");

  const submit = async (event: JSX.TargetedSubmitEvent<HTMLFormElement>): Promise<void> => {
    event.preventDefault();
    setMessage("");
    let result: Awaited<ReturnType<typeof saveProfileFullName>> | undefined;
    const completed = await mutation.run(async () => {
      result = await saveProfileFullName(user, fullName, api.updateUser);
      if (!result.ok) throw new Error(result.message);
    });
    if (!completed || result === undefined || !result.ok) return;
    setFullName(result.user.full_name);
    setMessage(result.message);
    onUpdated(result.user);
  };

  return (
    <form class="profile-name-form" onSubmit={submit}>
      <label for="profile-full-name">Full name</label>
      <input
        id="profile-full-name"
        name="full_name"
        value={fullName}
        maxLength={500}
        autocomplete="name"
        onInput={(event) => setFullName(event.currentTarget.value)}
      />
      <p class="profile-form-help">Optional. Shown with @{user.name} in the user directory.</p>
      <Button class="profile-submit" type="submit" disabled={mutation.busy}>Save full name</Button>
      <p class={`profile-form-message${mutation.error === undefined ? "" : " form-error"}`} aria-live="polite">
        {mutation.error === undefined ? message : messageFor(mutation.error)}
      </p>
    </form>
  );
}

function PasswordForm({ user }: { user: User }): JSX.Element {
  const mutation = useMutation();
  const confirmation = useRef<HTMLInputElement>(null);
  const [message, setMessage] = useState("");
  const [mismatch, setMismatch] = useState(false);

  const submit = async (event: JSX.TargetedSubmitEvent<HTMLFormElement>): Promise<void> => {
    event.preventDefault();
    setMessage("");
    setMismatch(false);
    const form = event.currentTarget;
    const data = new FormData(form);
    const password = String(data.get("password") ?? "");
    if (password !== String(data.get("confirmation") ?? "")) {
      setMismatch(true);
      confirmation.current?.focus();
      return;
    }
    const completed = await mutation.run(() => api.updateUser(user.name, { password }));
    if (completed) {
      form.reset();
      setMessage("Password changed.");
    }
  };

  const error = mismatch ? "The passwords do not match."
    : mutation.error === undefined ? "" : messageFor(mutation.error);
  return (
    <form class="profile-password-form" onSubmit={submit}>
      <label for="profile-new-password">New password</label>
      <input
        id="profile-new-password"
        type="password"
        name="password"
        required
        maxLength={72}
        autocomplete="new-password"
      />
      <label for="profile-confirm-password">Confirm new password</label>
      <input
        ref={confirmation}
        id="profile-confirm-password"
        type="password"
        name="confirmation"
        required
        maxLength={72}
        autocomplete="new-password"
      />
      <Button class="profile-submit" type="submit" disabled={mutation.busy}>Change password</Button>
      <p class={`profile-form-message${error === "" ? "" : " form-error"}`} aria-live="polite">
        {error || message}
      </p>
    </form>
  );
}

function ProfileContent({ initialUser, workspaces }: { initialUser: User; workspaces: Workspace[] }): JSX.Element {
  const { refreshCaller } = useApp();
  const [user, setUser] = useState(initialUser);
  const shown = profileIdentity(user);

  useEffect(() => setUser(initialUser), [initialUser]);

  const updated = (next: User): void => {
    setUser(next);
    void refreshCaller();
  };

  return (
    <div class="profile-view">
      <div class="profile-heading">
        <Avatar name={user.name} className="profile-avatar" />
        <div>
          <h1>{shown.heading}</h1>
          <p class="lede">{shown.detail}</p>
        </div>
      </div>
      <section class="profile-card">
        <h2>Account status</h2>
        <div class="profile-roles">
          {accountRoles(user).map((role) => <span class="listing-badge" key={role}>{role}</span>)}
        </div>
        <dl class="profile-facts">
          <dt>Username</dt><dd>{user.name}</dd>
          <dt>Created</dt><dd>{user.created_at}</dd>
          <dt>Updated</dt><dd>{shown.updated}</dd>
        </dl>
      </section>
      <section class="profile-card">
        <h2>Profile</h2>
        <FullNameForm key={user.name} user={user} onUpdated={updated} />
      </section>
      <section class="profile-card">
        <h2>Workspace access</h2>
        <ProfileWorkspaceList user={user} workspaces={workspaces} />
      </section>
      <section class="profile-card">
        <h2>Password</h2>
        <PasswordForm key={user.name} user={user} />
      </section>
    </div>
  );
}

export function ProfilePage({ route }: PageProps): JSX.Element {
  void route;
  const { identity } = useApp();
  const resource = useResource(async () => {
    if (identity === "") throw new Error("No authenticated user is available.");
    const [user, workspaces] = await Promise.all([api.user(identity), api.workspaces()]);
    return { user, workspaces: workspaces.rows };
  }, [identity]);

  if (resource.error !== undefined) return <ErrorMessage error={resource.error} />;
  if (resource.data === undefined) return <Loading />;
  return <ProfileContent initialUser={resource.data.user} workspaces={resource.data.workspaces} />;
}

function WorkspacePreferenceRow({
  preference,
  onUpdated,
}: {
  preference: WorkspacePreference;
  onUpdated: (preference: WorkspacePreference) => void;
}): JSX.Element {
  const mutation = useMutation();
  const toggle = async (): Promise<void> => {
    let updated: WorkspacePreference | undefined;
    const completed = await mutation.run(async () => {
      updated = await api.setWorkspaceIgnored(preference.workspace.key, !preference.ignored);
    });
    if (completed && updated !== undefined) onUpdated(updated);
  };

  return (
    <li class={`workspace-preference-row${preference.ignored ? " ignored" : ""}`}>
      <span class="workspace-preference-identity">
        <code>{preference.workspace.key}</code>
        <span>{preference.workspace.name}</span>
      </span>
      <span class={`workspace-preference-state ${preference.ignored ? "ignored-state" : "active-state"}`}>
        {preference.ignored ? "Ignored" : "Active"}
      </span>
      <span class="workspace-preference-actions">
        <a
          class="secondary-button workspace-preference-members"
          href={`#/workspaces/${encodeURIComponent(preference.workspace.key)}/members`}
        >Members</a>
        <Button
          class="secondary-button workspace-preference-action"
          disabled={mutation.busy}
          onClick={() => void toggle()}
        >{preference.ignored ? "Re-enable" : "Ignore"}</Button>
      </span>
      {mutation.error !== undefined && <ErrorMessage error={mutation.error} />}
    </li>
  );
}

function IgnoredWorkspacesSettingsCard({ initialWorkspaces }: { initialWorkspaces: WorkspacePreference[] }): JSX.Element {
  const [workspaces, setWorkspaces] = useState(initialWorkspaces);
  const [filter, setFilter] = useState("");
  const visible = new Set(filterWorkspacePreferences(workspaces, filter));

  const update = (updated: WorkspacePreference): void => {
    setWorkspaces((current) => current.map((preference) =>
      preference.workspace.key === updated.workspace.key ? updated : preference));
  };

  return (
    <section class="profile-card ignored-workspaces-card">
      <div class="ignored-workspaces-heading">
        <div>
          <h2>Ignored workspaces</h2>
          <p>
            Ignored workspaces are hidden from listings, search, counts, and navigation. They always remain
            available here so you can re-enable them.
          </p>
        </div>
        <span class="ignored-summary">{workspacePreferenceSummary(workspaces)}</span>
      </div>
      <label class="workspace-preference-filter">
        <span class="visually-hidden">Find a workspace</span>
        <SearchInput
          className="workspace-preference-search"
          value={filter}
          onInput={setFilter}
          placeholder="Find a workspace by name or key"
        />
      </label>
      <ul class="workspace-preference-list">
        {workspaces.map((preference) => visible.has(preference) && (
          <WorkspacePreferenceRow
            key={preference.workspace.key}
            preference={preference}
            onUpdated={update}
          />
        ))}
      </ul>
      {visible.size === 0 && <p class="workspace-preference-empty empty">No authorized workspaces match your search.</p>}
    </section>
  );
}

export function SettingsPage({ route }: PageProps): JSX.Element {
  void route;
  const { identity } = useApp();
  const resource = useResource<WorkspacePreference[] | null>(async () => {
    if (identity === "") throw new Error("No authenticated user is available.");
    try {
      return await api.workspacePreferences();
    } catch (error) {
      // An open server has an attribution identity but may have no account row.
      if (error instanceof ApiError && error.status === 404) return null;
      throw error;
    }
  }, [identity]);
  const storage = preferenceStorage(window);
  const [paginationAutoHide, setPaginationAutoHide] = useState(() => readPaginationAutoHide(storage));

  if (resource.error !== undefined) return <ErrorMessage error={resource.error} />;
  if (resource.data === undefined) return <Loading />;
  return (
    <div class="profile-view settings-view">
      <div class="settings-heading">
        <h1>Settings</h1>
        <p class="lede">Your preferences across Agent Work Board</p>
      </div>
      <section class="profile-card">
        <h2>Listings</h2>
        <label class="settings-preference">
          <input
            type="checkbox"
            checked={paginationAutoHide}
            onChange={(event) => {
              const checked = event.currentTarget.checked;
              setPaginationAutoHide(checked);
              rememberPaginationAutoHide(storage, checked);
            }}
          />
          <span class="settings-preference-copy">
            <span class="settings-preference-title">Automatically hide pagination</span>
            <span class="settings-preference-description">
              Hide pagination controls when a listing has fewer than 10 entries. Saved in this browser.
            </span>
          </span>
        </label>
      </section>
      {resource.data !== null && <IgnoredWorkspacesSettingsCard initialWorkspaces={resource.data} />}
    </div>
  );
}
