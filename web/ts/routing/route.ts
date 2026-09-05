import { legacyIssueSearchHref } from "../navigation.js";
import { listingFilterMaxLength } from "../listings.js";
export interface Route { path: string[]; query: URLSearchParams }
export function routeHref(route: Route, query = route.query): string {
  const suffix = query.toString();
  return `#/${route.path.join("/")}${suffix ? `?${suffix}` : ""}`;
}
export function parseRoute(): Route {
  const [path, query] = location.hash.replace(/^#\/?/, "").split("?", 2);
  const route = { path: path.split("/").filter(Boolean), query: new URLSearchParams(query) };
  if (route.path[0] === "search") {
    history.replaceState(null, "", legacyIssueSearchHref(route.query));
    return parseRoute();
  }
  const filter = route.query.get("filter");
  if (filter && filter.length > listingFilterMaxLength) {
    route.query.set("filter", filter.slice(0, listingFilterMaxLength));
    history.replaceState(null, "", routeHref(route));
  }
  return route;
}
/** Replacing a live filter keeps typing in one history entry and notifies the
 * same route state used by Back/Forward, without replacing its component. */
export function replaceRoute(route: Route, query = route.query): void {
  history.replaceState(null, "", routeHref(route, query));
  window.dispatchEvent(new Event("awb:route"));
}
export function facetHref(route: Route, name: string, value: string): string {
  const query = new URLSearchParams(route.query);
  const selected = query.getAll(name);
  query.delete(name); query.delete("page");
  for (const item of selected.filter(item => item !== value)) query.append(name, item);
  if (!selected.includes(value)) query.append(name, value);
  return routeHref(route, query);
}
