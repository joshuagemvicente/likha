# Feature: strict global Markdown skills

**Status:** planned. **Phase:** 3. **Product requirements:** FR-33.

## Format and discovery

Load only `<stateDir>/skills/<name>/SKILL.md` from the private global directory.
No repository/compatibility roots, remote catalogs, recursive discovery, scripts,
resource includes, or executable extensions ship here. Resolve paths safely;
reject symlinks/escapes and malformed or duplicate identities visibly without
preventing normal startup. Skill name must match its directory and use lowercase
letters/digits with interior hyphens, up to 64 characters.

```markdown
---
name: inspect-go
description: Inspect Go package boundaries before changing a public API.
---
Read the package entry points and callers. Explain the existing boundary before
proposing an edit. Use the normal repository tools and approval workflow.
```

Allow only `name` and `description` frontmatter. Bound description to 1 KiB,
require a nonempty UTF-8 Markdown body, and apply the shared 32-KiB body cap.
Reject `allowed-tools`, provider/model, permission, script, hook, import, and
other unsupported fields instead of implying compatibility. An oversized body
does not load. Regular Markdown links remain text; loading fetches nothing.

Advertise up to 32 valid skill name/description/origin entries, not their bodies.
If discovery exceeds the catalog cap, show a visible over-cap error and expose
the affected identities in `/skills`; do not silently choose a hidden subset
for model invocation. Require the user to reduce the set before advertising it.
Re-read safe metadata at idle/run boundaries; freeze the run catalog. If an
invoked file changed identity/metadata since discovery, refuse and request a
catalog refresh rather than loading an unexpected replacement.

## Invocation and authority

Main-only `skill` accepts required `name`. A valid call loads that discovered
body as attributed instruction text in a tool result. Preserve origin/name
and disclose that the configured model provider receives the text. Bound loaded
content under both skill and model-output caps. Skills cannot add tools, execute
code, change the model/provider, or approve effects; runtime policy remains
authoritative even if the body demands otherwise.

`/skills` lists/inspects the catalog while idle; `/skill <name> [request]` loads
the selected body with visible provenance and starts a normal main-agent turn
containing the user's request. Do not create arbitrary `/<skill-name>` commands,
execute shell interpolation, or substitute command arguments into code.
The standard `//` escape and inactive-during-run command rules remain intact.
Explore children lack the skill tool; only explicitly briefed task text enters
their fresh context, with the same read-only ceiling.

## Persistence and acceptance

Persist invoked instruction text as an attributed completed interaction for
faithful conversation resume; do not reload a changed disk file merely because
an old skill call appears in history. Advertised metadata and current runtime
catalog remain per-run context. No historical skill grants permissions.
Avoid duplicating a user-invoked skill body twice in one new request.

- Discover/list an empty and valid catalog, invoke from model and user, and
  observe that bodies load on demand rather than in every request.
- Supply invalid/duplicate/oversized/changed/unsafe files and over-cap discovery;
  show named errors without tool registration or hidden subset selection.
- Include instruction text requesting shell/write/permission changes; normal
  gates still apply and explore remains read-only.
- Resume a previously loaded skill after disk changes; historical text remains
  historical, and no file/script/network load happens automatically.

Custom slash command templates remain deferred under
[custom-commands](../custom-commands/spec.md); `/skills` belongs to real skills.
