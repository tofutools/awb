# Getting started

This guide starts locally, where awb needs only its binary and a SQLite file.
The same data can be served to a browser or remote CLI later.

## Install

Install with Go:

```console
go install github.com/tofutools/awb@latest
```

The binary contains the web application. There is no separate frontend package
or runtime service to install.

## Explore the demo

```console
awb init
awb demo
awb serve
```

Open <http://127.0.0.1:7777/>. `awb demo` creates a dedicated `demo` workspace
with sample issues covering the whole vocabulary. It refuses to replace an
existing demo unless `--force` explicitly allows that destructive reset.

## Create a workspace

For real work, create a stable workspace boundary:

```console
awb workspace create app --name "My application" \
  --description "Issues for the application and its release work."
```

The key `app` becomes the prefix of every issue ID in the workspace. It cannot
be renamed and issues cannot move between workspaces, so choose a short durable
key. The display name and description remain editable.

Put the default workspace in the repository root:

```yaml
# .awb.yaml
workspace: app
```

awb walks upward from the current directory to find this file. Commit it so
agents and humans in each checkout resolve the same workspace.

## Create and relate work

```console
$ awb create "Fix empty-input parser crash" --type bug --priority 1 --label parser
app-a3f9c1

$ awb create "Add an empty-input regression test" \
    --blocked-by app-a3f9c1 \
    --discovered-from app-a3f9c1 \
    --label parser
app-77e0b2
```

Relation flags always read as “the new issue — relation — the named issue”. In
this example, the test issue is blocked by and was discovered from the bug.

Descriptions and comments are Markdown. Use files for substantial text:

```console
awb update app-a3f9c1 --description-file investigation.md
awb comment add app-a3f9c1 --body-file findings.md
awb attach add app-a3f9c1 failing-input.txt
```

## Pick up and finish work

`ready` asks what nobody has taken and can start now:

```console
$ awb ready --compact
app-a3f9c1 P1 open bug "Fix empty-input parser crash" #parser
```

Claiming joins the assignee list and changes the issue to `in_progress`
atomically:

```console
awb claim app-a3f9c1
awb list --mine --compact
```

Close it with a reason when the work is complete:

```console
awb close app-a3f9c1 --reason "Reject the empty token stream before parsing"
```

The reason and status transition appear together in the issue activity stream.
Because blocked state is derived, the regression-test issue is now returned by
`awb ready` without being edited.

## Teach an agent

Install awb's bundled skill into the supported agent harnesses:

```console
awb agent-guide install-skills
```

Or print the compact instruction block for inspection or inclusion in a
workspace instruction file:

```console
awb agent-guide
awb agent-guide --write AGENTS.md
```

Writing is marker-delimited and idempotent: running it again replaces the awb
block. Skill installation is independent and does not modify repository files.

Continue with the [command-line guide](cli.md), or start the browser experience
in the [web UI guide](web-ui.md).
