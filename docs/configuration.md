# Configuration

awb resolves one invocation from command-line flags, environment variables,
directory context, a user configuration file, and built-in defaults.

## Precedence

The general order is:

1. command-line flags;
2. `AWB_*` environment variables;
3. the nearest `.awb.yaml` directory-context file;
4. the user configuration file;
5. built-in defaults.

Only workspace scope participates at every layer. The committed local file is
deliberately unable to select a database or attachment directory, supply
credentials, claim an identity, or change presentation.

Run `awb status` to see the resolved connection, identity, paths, local context,
environment overrides, and workspace counts.

## User configuration

The user file is `$XDG_CONFIG_HOME/awb/config.yaml`, or
`~/.config/awb/config.yaml` when `XDG_CONFIG_HOME` is unset. Every key is
optional:

```yaml
db: /home/you/.local/share/awb/awb.db
attachments: /srv/awb/attachments
user: alice
password: secret
identity: alice
workspace: app
color: auto
```

`db` may instead be an HTTP or HTTPS awb server URL. `user` and `password` are
used only in remote mode; direct access to a database file is authorized by the
filesystem. `identity` is the default assignee used by `claim`, `release`, and
`--mine` where applicable.

Set `AWB_CONFIG_FILE` to use a different user file. An explicitly named file
must exist; a typo is an error rather than a silent fallback to defaults. A
leading `~` is expanded in `AWB_CONFIG_FILE`, `AWB_DB`, and `AWB_ATTACHMENTS`.

## Environment variables

| Variable | Meaning |
| --- | --- |
| `AWB_CONFIG_FILE` | Alternate user configuration file |
| `AWB_DB` | SQLite path or awb server URL |
| `AWB_ATTACHMENTS` | Local attachment content directory |
| `AWB_USER` | Basic-auth username for remote mode |
| `AWB_PASSWORD` | Basic-auth password for remote mode |
| `AWB_IDENTITY` | Default assignee identity |
| `AWB_WORKSPACE` | Default workspace for creation and listings |
| `AWB_COLOR` | `auto`, `always`, or `never` |

`NO_COLOR` also disables color when it is non-empty. An explicit `--color`
setting takes priority.

The default database is `$XDG_DATA_HOME/awb/awb.db`, falling back to
`~/.local/share/awb/awb.db`. Attachment content defaults to an `attachments`
directory beside that database.

## Directory context

awb searches upward from the working directory for exactly `.awb.yaml`. The
first file found supplies context for that directory tree:

```yaml
workspace: app
label: frontend
```

- `workspace` becomes the default workspace for issue creation and listings.
- `label` is added to every issue created there, in addition to explicit
  `--label` values.

The file is designed to be committed. Unknown keys, including user-level keys,
are ignored. A directory can shape the work you see and create, but cannot
redirect data or credentials.

`--no-context` ignores both local values for one invocation. To widen a listing
beyond any configured default workspace, use `--all-workspaces`.

## Local and remote examples

Keep a separate local database:

```console
AWB_DB=~/work/private-awb.db awb status
```

Connect to a shared deployment:

```yaml
db: https://work.example/awb/
user: alice
password: secret
identity: alice
```

The database URL may include a reverse proxy's base path, but not user info, a
query string, or a fragment. Keep credentials in their dedicated settings so
they do not appear in process listings or shell history.

When `user` or a non-empty `password` is configured, awb sends an HTTP Basic
Authorization header on every remote request. It refuses to send that header to
a non-loopback `http://` URL, where anybody able to observe the connection could
reuse it. Use HTTPS for a shared server; loopback HTTP remains available for
local development. `--insecure-transport` accepts the exposure for one explicit
invocation when a separately protected cleartext connection is unavoidable.
The same rule is checked again on redirects, so an HTTPS server cannot silently
downgrade a credential-bearing request to non-loopback HTTP.
