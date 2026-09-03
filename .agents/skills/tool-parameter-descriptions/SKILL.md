---
name: tool-parameter-descriptions
description: Write or review concise MCP tool parameter descriptions that add only information needed to construct correct calls.
---

# Tool Parameter Descriptions

Use these guidelines when adding or reviewing MCP tool input schemas.

## Standard

A parameter description must add information that cannot be recovered from the parameter name, JSON type, required status, or tool description.

Keep a description when it communicates at least one of:

- Accepted values, syntax, units, bounds, or defaults.
- Dependencies or mutual exclusion with other parameters.
- Behavior when omitted or set to a sentinel value such as zero.
- Replacement, destructive, or other side effects.
- Identifier type or source when similar identifiers are easily confused.
- Non-obvious filtering or matching behavior.

Otherwise, omit it. In particular, do not:

- Restate the parameter name, such as `build_number: "Build number"`.
- Say that a parameter is optional when the schema already expresses that.
- Repeat information already provided by the tool description.
- Add generic domain explanation that does not affect the argument value.

Prefer short phrases over complete sentences. Put machine-readable constraints such as enums, bounds, and defaults in the schema when supported; use prose only for information the schema cannot express clearly.

## Review

Read the handler as well as the parameter type. Check defaults, validation, identifier formats, coupled parameters, pagination behavior, and side effects before deciding whether a description is needed.

Use this deletion test: if removing the description would not make a materially different or invalid tool call more likely, remove it.

Examples:

- Omit `Description of the cluster` from `description`.
- Replace `Build number` with `Sequential build number, not the build UUID` when that distinction matters.
- Use `Maximum matches to return; 0 returns all` for a limit with non-obvious zero behavior.
- Preserve warnings such as an update replacing an entire map.

Update schema tests for behaviorally important descriptions and run the relevant Go tests. Use LLM evals for changes intended to improve tool selection or argument construction; unit tests cannot establish model effectiveness.
