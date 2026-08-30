---
name: awb
description: Use Agent Work Board (awb) as a project's lightweight issue and work tracker—the role commonly filled by Jira, Linear, or GitHub Issues. Use when a repository uses awb or the user asks to find, claim, create, update, relate, attach evidence to, or close awb issues.
---

## Issue tracking with awb

Track work in `awb`, the project's lightweight issue and work tracker—the role
commonly filled by Jira, Linear, or GitHub Issues. Every command is
non-interactive.

**Start here:** `awb ready --compact` lists unblocked, unassigned, open issues,
highest priority first. That is the "what can I work on now" question.

```
awb ready --compact                    # what to pick up
awb claim <id>                         # take it (sets in_progress)
awb close <id> --reason "what you did" # finish it
awb release <id>                       # give it back untouched
```

**Create and record what you find:**

```
awb create "Title" --type bug --priority 1 --label parser
awb create "Follow-up" --discovered-from <id> --blocked-by <id>
awb dep add <id> --blocked-by <other>  # reads "id blocked-by other"
```

**Look things up:**

```
awb show <id> --json          # full issue: relations, blockers, links
awb list --compact --mine     # issues you hold
awb blocked --compact         # what is stuck, and on what
awb search parser --compact   # literal terms, whole-token matching
awb dep tree <id>             # the decomposition below an issue
```

**Attach files** (evidence: a log, a trace, a screenshot). An attachment is
addressed by its issue and its name, like a label; one name per issue:

```
awb attach add <id> ./trace.txt          # named trace.txt unless --name
awb attach list <id> --compact           # what is attached
awb attach get <id> trace.txt --output f # content out (stdout without it)
awb attach delete <id> trace.txt --force
```

**Vocabulary** (fixed; put anything else in labels):

- type: `epic` `feature` `bug` `task` `chore` (default `task`)
- status: `open` `in_progress` `closed` — changed only by create --assignee,
  claim, release, close and reopen, never by `awb update`
- priority: `0` (highest) to `4` (lowest), default `2`
- relations: `blocked-by` `has-parent` `discovered-from` `related`, each read
  "subject — relation — other". Only `blocked-by` affects readiness.
- labels and assignees: lowercase letters, digits, `-_./` only

**Output modes.** `--compact` is one line per issue and costs the least context:
`<id> P<priority> <status> <type> "<title>" [@assignee] [#label...] [!blocked]`
The title is a JSON string, so split on whitespace outside it. `--json` is the
stable full representation. The default table is for humans; do not parse it.

**Exit codes:** `0` ok, `1` runtime error, `2` usage error, `3` not found,
`4` constraint violation (a cycle, a duplicate, or an issue somebody else holds),
`5` forbidden (against a server: your account may see it but may not do that;
a project you have no access to is `3`, since it is not yours to know about).

An issue ID is `<project>-<hash>`; any unambiguous prefix, or a bare hash, works.
