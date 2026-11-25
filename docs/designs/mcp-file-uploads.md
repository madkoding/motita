# MCP file uploads (design only)

Status: not implemented. The MCP specification (revision 2025-11-25) has no way to hand a
server a file outside the model's context, so there is nothing to build on yet.

## Goal

Pass a PDF, a spreadsheet or an image to an MCP tool without its bytes passing through the
model's context (no tokens spent on them, no copy in the transcript).

## What exists today

- `mcp_call` carries arguments as JSON, so a file would travel inline (base64) inside the
  model's own output: the cost this feature is meant to avoid.
- A tool that takes a path or a URL works already, when the server can read it.

## Proposed shape once the protocol offers it

1. The model names a file by workspace path in the arguments, as `{"$file": "reports/q3.pdf"}`;
   it never reads or writes the bytes.
2. motita resolves the path inside the workspace (the same confinement as `read_file`), asks the
   person (rule `mcp`, the question names the file, its size and the server), and sends the
   bytes over the transport the protocol defines.
3. The size is capped and the path may not leave the workspace; a file that matches a secret
   pattern (`.env`, keys) is refused.
4. The server's answer comes back as for any other call.

## Open questions

- Which mechanism the protocol standardises (a resource upload, a pre-signed URL, a new content
  type). The design above only fixes what motita decides: confinement, approval, size cap.
- Whether the stdio transport can carry large files at all, or the feature needs the HTTP
  transport, which motita does not support yet.

Revisit when the specification publishes the mechanism.
