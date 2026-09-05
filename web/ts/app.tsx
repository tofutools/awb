import { render } from "preact";
import { useEffect, useState } from "preact/hooks";
import { api } from "./api.js";
import { namedDestinations, workspaceScopedHref } from "./navigation.js";
import { accountMenuItems } from "./preferences.js";
import { parseRoute, type Route } from "./routing/route.js";
import { AppContext, Avatar, ErrorMessage, Popover } from "./components/ui.js";
import { Icon, type IconName } from "./components/icon.js";
import { Palette } from "./components/palette.js";
import { ListingPage } from "./pages/listing.js";
import { IssuePage, TreePage } from "./pages/issue.js";
import { BoardsPage } from "./pages/boards.js";
import {
  WorkspacesPage,
  WorkspacePage,
  WorkspaceMembersPage,
  UsersPage,
  UserPage,
  UserCreatePage,
  ProfilePage,
  SettingsPage,
} from "./pages/admin.js";
function App() {
  const [route, setRoute] = useState(parseRoute);
  const [caller, setCaller] = useState({ identity: "", mayManageUsers: false });
  const [notice, setNotice] = useState<{
    message: string;
    error: boolean;
  } | null>(null);
  const [ready, setReady] = useState(false);
  const refreshCaller = async () => {
    try {
      const caller = await api.identity();
      setCaller({
        identity: caller.identity,
        mayManageUsers: caller.may_manage_users,
      });
    } catch {
      setCaller({ identity: "", mayManageUsers: false });
    }
  };
  useEffect(() => {
    void refreshCaller().then(() => setReady(true));
    const update = () => setRoute(parseRoute());
    window.addEventListener("hashchange", update);
    window.addEventListener("awb:route", update);
    return () => {
      window.removeEventListener("hashchange", update);
      window.removeEventListener("awb:route", update);
    };
  }, []);
  return (
    <AppContext.Provider
      value={{
        ...caller,
        refreshCaller,
        notify: (message, error = false) => setNotice({ message, error }),
      }}
    >
      <header class="app-header">
        <a href="#/ready" class="brand">
          <img src="awb-mark.png" alt="" class="brand-mark" />
          Agent Work Board
        </a>
        <nav>
          {namedDestinations.map((d) => (
            <a
              key={d.id}
              class={(route.path[0] ?? "ready") === d.id ? "active" : ""}
              href={
                d.workspaceScoped
                  ? workspaceScopedHref(d.workspaceScoped, route.query)
                  : d.path
              }
            >
              <Icon name={d.id as IconName} />
              {d.label}
            </a>
          ))}
        </nav>
        <Palette route={route} />
        {caller.identity && (
          <span class="account-menu">
            <Popover
              label={`Open menu for @${caller.identity}`}
              className="identity"
              buttonLabel={
                <>
                  <Avatar name={caller.identity} />
                  <span class="identity-name">@{caller.identity}</span>
                </>
              }
            >
              {accountMenuItems.map((item) => (
                <a
                  key={item.href}
                  href={item.href}
                  class="account-menu-item"
                  role="menuitem"
                >
                  {item.label}
                </a>
              ))}
            </Popover>
          </span>
        )}
      </header>
      <main>
        {notice && (
          <p
            class={notice.error ? "app-notice app-notice-error" : "app-notice"}
            role={notice.error ? "alert" : "status"}
          >
            {notice.message}
          </p>
        )}
        {ready ? (
          <RouteView route={route} key={route.path.join("/")} />
        ) : (
          <p class="route-loading" role="status">
            Loading…
          </p>
        )}
      </main>
    </AppContext.Provider>
  );
}
/** Route identity changes only with the addressed page, never with its query
 * or a mutation. Lists keep their focused search control as results arrive. */
function RouteView({ route }: { route: Route }) {
  switch (route.path[0]) {
    case undefined:
    case "ready":
      return <ListingPage route={route} kind="ready" />;
    case "issues":
      return route.path.length > 1 ? (
        <IssuePage route={route} />
      ) : (
        <ListingPage route={route} kind="issues" />
      );
    case "blocked":
      return <ListingPage route={route} kind="blocked" />;
    case "boards":
      return <BoardsPage route={route} />;
    case "tree":
      return <TreePage route={route} />;
    case "workspaces":
      return route.path[2] === "members" ? (
        <WorkspaceMembersPage route={route} />
      ) : route.path[1] ? (
        <WorkspacePage route={route} />
      ) : (
        <WorkspacesPage route={route} />
      );
    case "users":
      return route.path[1] === "-" && route.path[2] === "new" ? (
        <UserCreatePage route={route} />
      ) : route.path[1] ? (
        <UserPage route={route} />
      ) : (
        <UsersPage route={route} />
      );
    case "profile":
      return <ProfilePage route={route} />;
    case "settings":
      return <SettingsPage route={route} />;
    default:
      return (
        <div class="error">
          <h1>No such page</h1>
          <a href="#/ready">Go to Ready</a>
        </div>
      );
  }
}
try {
  render(<App />, document.getElementById("app")!);
} catch (error) {
  render(<ErrorMessage error={error} />, document.getElementById("app")!);
}
