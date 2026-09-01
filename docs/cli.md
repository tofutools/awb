# Command-line guide

The CLI is the primary agent interface and a complete human interface. It uses
the same commands against a local SQLite file and a remote awb server.

## Output contracts

awb has three output modes:

| Mode | Intended caller | Contract |
| --- | --- | --- |
| Default | Human at a terminal | Responsive tables and rendered Markdown; not stable for parsing |
| `--compact` | Agent context and shell inspection | Terse deterministic records, normally one line per issue |
| `--json` | Programs and agents needing complete data | Stable full representation |

`--json` and `--compact` are mutually exclusive. Errors go to standard error as
one line in default and compact mode, or as an error object under `--json`.

Exit status is the machine-readable error class:

| Status | Meaning |
| --- | --- |
| `0` | Success |
| `1` | Runtime or environmental failure |
| `2` | Invalid usage or input |
| `3` | Record not found |
| `4` | Conflict with stored state |
| `5` | Forbidden |

Work commands do not prompt. Destructive operations require an explicit
`--force`. Password entry for `user add` and `user update --password` is the
intentional exception: a terminal prompts with echo disabled, while scripts
pipe the password through standard input or supply a bcrypt hash where the
command supports one.

The full-screen interaction is always requested with `--interactive`. It is
available on issue list, ready, blocked, search, workspace list, and user list,
and refuses to start unless both input and output are terminals.

## Finding work

The main listing commands answer different questions:

```console
awb ready --compact                 # open, unblocked, unassigned
awb list --mine --compact           # issues assigned to the configured identity
awb blocked --compact               # active blocked issues and their blockers
awb search parser regression        # title and description contain every term
```

`ready` is intentionally opinionated. Its default order respects manual board
position first, then falls back to priority and update time for automatically
positioned work. It does not accept assignee filters because its question is
“what can nobody in particular pick up next?” Use `list --mine` to find your
own work, or `ready --sort priority` when strict priority order is desired.

Listings can be scoped by workspace, label, type, priority, status, assignee,
and update time. A configured workspace applies by default; use
`--all-workspaces` for a cross-workspace listing. Closed issues are normally
hidden unless explicitly included.

Search terms are literal whole tokens. Matching is case- and
diacritic-insensitive, without wildcard syntax, stemming, or prefix matching.

## Issue lifecycle

Create prints the new ID; most other successful mutations are silent unless a
structured output mode asks for their result.

```console
awb create "Implement cache eviction" --type feature --priority 1 \
  --label storage --description-file proposal.md
awb claim app-a3f9c1
awb release app-a3f9c1
awb close app-a3f9c1 --reason "Merged and verified"
awb reopen app-a3f9c1
```

The transitions preserve a few deliberate invariants:

- creating with one or more `--assignee` values also starts the issue;
- claiming adds an assignee and changes the status to `in_progress` atomically;
- releasing removes one assignee, reopening the issue when the last leaves;
- closing preserves the assignee list;
- reopening clears every assignee;
- a close reason is one typed activity entry committed with the transition.

`awb move` is the lower-level board operation. It changes status, optional epic
parent, and optional manual position atomically. Most agents should prefer the
named lifecycle commands unless they are deliberately reproducing a board move.

Hard deletion is not recovery or archiving:

```console
awb delete app-a3f9c1 --force
```

It removes the issue, activity, attachments, and relations. Archive an entire
workspace when retained read-only history is the actual goal.

## Relations and readiness

Every relation reads “subject — relation — other”:

```console
awb dep add app-a3f9c1 --blocked-by app-77e0b2
awb dep rm  app-a3f9c1 --blocked-by app-77e0b2
awb dep tree app-a3f9c1
```

The three directed relation graphs (`blocked-by`, `has-parent`, and
`discovered-from`) cannot contain cycles. `related` is symmetric and
unconstrained. Only `blocked-by` affects the derived blocked state and
`awb ready`.

Relations may cross workspace boundaries. On an authenticated server, a
visible issue may name a related issue in a workspace the caller cannot open.
That limited disclosure keeps its readiness truthful without exposing the
other issue's content.

## Descriptions, comments, and activity

Descriptions and comments preserve the Markdown source exactly. At a terminal,
default output renders descriptions; when redirected, awb writes their source.
JSON and compact output never replace source text with terminal rendering.

For safe external editing, fetch a description with its version receipt:

```console
awb description get app-a3f9c1 --output issue.md
# edit issue.md
awb update app-a3f9c1 --description-file issue.md
```

The receipt sits beside the output and makes the update conditional. If another
caller changed the issue meanwhile, the update refuses instead of overwriting
their work. Workspace descriptions have the same workflow under
`awb workspace description`.

Comments are append-only:

```console
awb comment add app-a3f9c1 --body "Reproduced on Linux."
awb comment add app-a3f9c1 --body-file investigation.md
awb activity app-a3f9c1 --compact
```

The activity stream combines comments with structured change entries, newest
first. Successful changes record an entry in the same transaction; failures
and no-op mutations record none. This is a work log, not an immutable audit
ledger: deleting an issue deletes its activity.

## Attachments

```console
awb attach add app-a3f9c1 ./trace.txt
awb attach list app-a3f9c1 --compact
awb attach get app-a3f9c1 trace.txt --output ./downloaded-trace.txt
awb attach delete app-a3f9c1 trace.txt --force
```

An attachment is addressed by issue ID and name and is immutable. One issue
cannot hold two attachments under the same name. Content is limited to 32 MiB,
streamed in both directions, and stored outside SQLite as a file named by its
SHA-256. Identical content is stored once; the database retains metadata and
references.

## Workspaces

```console
awb workspace list
awb workspace show app
awb workspace archive app
awb workspace restore app
awb workspace activity app
```

Archiving retains the workspace, stable issue IDs, links, membership, and
history, while normal discovery omits it and work mutations are refused.
Restoring reactivates the same boundary. Hard deletion refuses while issues
remain unless `--cascade` is also explicitly requested.

## Local and remote parity

Set the database to an HTTP or HTTPS URL to use the remote backend:

```console
AWB_DB=https://work.example/awb AWB_USER=alice awb ready --json
```

The command tree does not have a second remote implementation. Commands call
one backend interface; the local implementation uses SQLite transactions and
the remote implementation uses the API. See [Server and API](server.md) for
authentication and deployment.

## Shell completion and status

Generate completion through Cobra's standard command:

```console
awb completion bash
awb completion zsh
awb completion fish
```

`awb status` is the diagnostic starting point. It reports local versus remote
mode, the database or server and UI URLs, identity, configuration sources,
relevant environment overrides, attachment location, and per-workspace counts.

For the complete current flag reference, use `awb <command> --help`.
