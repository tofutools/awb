# Documentation

Agent Work Board is one issue tracker with three surfaces: a non-interactive
CLI, an OpenAPI-described HTTP API, and a bundled web UI. All three operate on
the same workspaces, issues, relations, comments, and attachments.

Start with the guide that matches what you are doing:

| Goal | Guide |
| --- | --- |
| Evaluate awb or create the first workspace | [Getting started](getting-started.md) |
| Teach an agent or automate commands | [Command-line guide](cli.md) |
| Use boards, issue pages, or administration in a browser | [Web UI](web-ui.md) |
| Select a database, identity, or directory scope | [Configuration](configuration.md) |
| Share awb, connect a remote CLI, or operate a deployment | [Server and API](server.md) |
| Build or change awb | [Development](development.md) |
| Understand the design boundaries | [Architecture](../spec/ARCHITECTURE.md) |

The command's own help is the detailed reference for the installed version:

```console
awb --help
awb create --help
awb workspace archive --help
```

The project is in beta. The CLI and HTTP API may change before 1.0; released
databases remain upgradeable through migrations.
