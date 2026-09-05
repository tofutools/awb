export type IconName =
  | "attachment"
  | "blocked"
  | "boards"
  | "change"
  | "clock"
  | "edit"
  | "info"
  | "issues"
  | "workspaces"
  | "ready"
  | "relation"
  | "search"
  | "tag"
  | "users";
const paths: Record<IconName, string> = {
  attachment:
    '<path d="m21.4 11.6-8.5 8.5a6 6 0 0 1-8.5-8.5l9.2-9.2a4 4 0 0 1 5.7 5.7l-9.2 9.2a2 2 0 0 1-2.8-2.8l8.5-8.5"></path>',
  blocked:
    '<circle cx="12" cy="12" r="9"></circle><path d="m5.7 5.7 12.6 12.6"></path>',
  boards:
    '<rect x="3" y="4" width="5" height="16" rx="1"></rect><rect x="10" y="4" width="5" height="11" rx="1"></rect><rect x="17" y="4" width="4" height="14" rx="1"></rect>',
  change:
    '<path d="M7 7h11l-3-3m3 3-3 3"></path><path d="M17 17H6l3 3m-3-3 3-3"></path>',
  clock: '<circle cx="12" cy="12" r="9"></circle><path d="M12 7v5l3 2"></path>',
  edit: '<path d="M4 20h4l11-11a2.8 2.8 0 0 0-4-4L4 16v4z"></path><path d="m13.5 6.5 4 4"></path>',
  info: '<circle cx="12" cy="12" r="9"></circle><path d="M12 11v5"></path><path d="M12 8h.01"></path>',
  issues:
    '<path d="M6 3h8l4 4v14H6z"></path><path d="M14 3v5h5M9 13h6M9 17h6"></path>',
  workspaces: '<path d="M3 6h7l2 2h9v11H3z"></path>',
  ready:
    '<path d="m5.5 5.1-3.5 6.9v6a2 2 0 0 0 2 2h16a2 2 0 0 0 2-2v-6l-3.5-6.9A2 2 0 0 0 16.7 4H7.3a2 2 0 0 0-1.8 1.1z"></path><path d="M2 12h6l2 3h4l2-3h6"></path>',
  relation:
    '<path d="M10 13a5 5 0 0 0 7.1 0l2-2A5 5 0 0 0 12 3.9L10.9 5"></path><path d="M14 11a5 5 0 0 0-7.1 0l-2 2A5 5 0 0 0 12 20.1l1.1-1.1"></path>',
  search: '<circle cx="11" cy="11" r="7"></circle><path d="m16 16 4 4"></path>',
  tag: '<path d="M20 13 13 20 4 11V4h7z"></path><circle cx="8.5" cy="8.5" r="1"></circle>',
  users:
    '<circle cx="9" cy="8" r="3"></circle><path d="M3 20v-2a5 5 0 0 1 5-5h2a5 5 0 0 1 5 5v2"></path><path d="M16 5a3 3 0 0 1 0 6M17 14a5 5 0 0 1 4 4v2"></path>',
};
export function Icon({ name }: { name: IconName }) {
  return (
    <svg
      viewBox="0 0 24 24"
      aria-hidden="true"
      class="icon"
      dangerouslySetInnerHTML={{ __html: paths[name] }}
    />
  );
}
