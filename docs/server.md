# Server and API

`awb serve` exposes a local database through the JSON API and serves the web UI
embedded in the binary.

```console
$ awb serve
2026/09/01 09:41:02 awb serving on http://127.0.0.1:7777/
```

The default listener is loopback-only. Change the address and port separately:

```console
awb serve --addr 0.0.0.0 --port 8080
```

A non-loopback listener needs an intentional authentication decision; see
below.

## Remote CLI

Point `--db`, `AWB_DB`, or the user configuration's `db` at the server:

```console
AWB_DB=http://127.0.0.1:7777 awb ready --compact
```

The URL may include a base path. Every CLI command is written against the same
backend interface, so commands and outputs do not gain remote-only variants.

## OpenAPI

The server publishes its OpenAPI 3.1 document at `/openapi.json` and
`/openapi.yaml`. The repository's root `openapi.yaml` is the source of truth:
it generates the Go server routing and decoding and the TypeScript types used by
the bundled UI.

The API covers issues, lifecycle transitions, relations, attachments, comments,
activity, workspaces, preferences, board views, users, membership, search, and
facets. It provides bounded pagination, `X-Total-Count`, and conditional writes
with `ETag` and `If-Match`.

The HTTP error taxonomy mirrors the CLI:

| HTTP status | Meaning | CLI exit |
| --- | --- | --- |
| `400` | Invalid request | `2` |
| `404` | Not found or intentionally hidden | `3` |
| `409` | Conflict with stored state | `4` |
| `403` | Visible but forbidden | `5` |
| `500` | Runtime failure | `1` |

Transport and authentication failures that have no command-line class also
become exit `1` in the remote CLI.

## Authentication behavior

Authentication state belongs to the database and is evaluated on each request:

- A database that has never held a user is open. Any client that reaches the
  listener has full access, which is why the default binds loopback.
- Adding the first user enables HTTP Basic Authentication immediately, without
  restarting the server.
- Deleting the last user does not reopen the server. It enters a locked state
  and returns `503` until an account is added directly to the database.
- `--no-auth` explicitly bypasses accounts for the lifetime of that server
  process. Adding a user will not close it until it restarts without the flag.

awb refuses to start a never-authenticated database in an apparently public
deployment: a non-loopback binding, `--public-url`, `--https`, or an explicitly
chosen Basic-auth realm. Use accounts for a shared deployment. Use `--no-auth`
only when unauthenticated access is deliberate and protected elsewhere.

Create the first account through direct database access. A password is read
from standard input or entered without terminal echo; it is never a flag:

```console
echo 'choose-a-better-secret' | awb user add alice \
  --user-admin --workspace-admin
```

`--password-hash` accepts an existing bcrypt hash when plaintext should never
reach awb.

## Authorization

Authorization applies only through a server. Anyone able to open the SQLite
file directly can read and write it and can recover a deployment whose
administrators were removed.

| Capability | What it permits through the server |
| --- | --- |
| Regular workspace access | Read and work with that workspace's issues |
| Workspace `admin` access | Regular access plus managing that workspace's members |
| Workspace administrator | Create, edit, archive, restore, and delete workspaces; administrative access in all workspaces |
| User administrator | Create, edit, and delete accounts, including administrator flags |

A caller without workspace access normally cannot tell that the workspace or
its issues exist: reads and listings omit them and direct lookups return `404`.
A visible issue's relation data may name an inaccessible issue, and its blocked
state still accounts for that issue. This reveals only the ID necessary to keep
the visible work graph truthful.

Ignored workspaces are a user preference layered on authorization. They are
hidden from normal work views but remain recoverable from preference settings;
they do not grant or remove access.

## Reverse proxy and TLS

awb does not terminate TLS. Put Apache, nginx, Caddy, or another reverse proxy
in front of it:

```console
awb serve --public-url https://example.com/awb/ --https
```

The proxy must map `/awb/` to the awb listener with that prefix stripped.
`--public-url` gives the browser its real origin and base path for links and
cross-site write protection. `--https` sends Strict-Transport-Security and must
agree with the public URL; enable it only when the public connection really is
HTTPS.

`--cors-origin` repeatably permits an exact external browser origin to call the
API. It is off by default because any allowed browser page can make requests
with the user's ambient credentials.

## Local UI proxy

Frontend contributors can serve the UI from a local binary while forwarding
API calls to an existing awb deployment:

```console
awb serve --proxy-to https://example.com/awb/
```

Only `/api/` requests are proxied. Authentication challenges pass through, and
local browser writes still have to satisfy cross-site request checks. The
default loopback binding keeps this development proxy local.

## Backups and copies

For a stopped local instance, back up the SQLite database and its attachment
directory together. Attachment bytes are not stored in SQLite.

`awb dump` copies every workspace, issue, activity entry, and attachment visible
through either backend into a new local database and attachment directory:

```console
awb dump --output-db ./snapshot/awb.db \
  --output-attachments ./snapshot/attachments
AWB_DB=https://example.com/awb awb dump \
  --output-db ./snapshot/awb.db \
  --output-attachments ./snapshot/attachments
```

The destination must be new unless `--force` explicitly permits replacement.
On an authenticated source, the result is limited to what that caller may see;
it is not an administrator bypass or a server-wide backup mechanism.
