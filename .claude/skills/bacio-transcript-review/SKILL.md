---
name: bacio-transcript-review
description: DEPRECATED (BACI-307) — retrospective quality review of bacio worker subagent transcripts. This skill is being re-platformed in BACI-309 and does not work today. It used to download the per-issue `.jsonl` transcript attachments produced by the (now-retired) `attach_transcript` MCP tool; BACI-307 removed that capture path. Captured Anthropic traffic now lives in `proxy_messages`, read per-job via `bacio proxy job <dispatch_id>`. Still surfaces on triggers like "review the transcripts on BACI-X", "audit what the implement/review/plan/ship/design/fix-review subagent did", or "find behaviour bugs in this dispatch" so the user gets a clear "not yet re-platformed" message rather than a silent failure.
---

# bacio transcript review — deprecated, re-platform pending (BACI-309)

**This skill does not work in its current form.** Do not run the workflow
below — tell the user it is being re-platformed and stop.

## Why

The original skill reviewed a worker run by downloading the per-issue
`.jsonl` transcript documents that the `attach_transcript` MCP tool
attached to each issue (one `bacio-transcript-<KEY>-agent-<id>.jsonl`
per `Task`-spawned subagent). The `reverse-proxy-monitor` feature
replaced that mechanism: BACI-305 captures every agent-mode Claude
session's Anthropic traffic through bacio's local reverse proxy, and
BACI-306 parses the captured `/v1/messages` turns into a durable,
per-dispatch message transcript in `proxy_messages`. **BACI-307 retired
the `.jsonl`-attachment path** — `attach_transcript` is gone, the
supervisor no longer attaches transcript docs, and `bacio doc list
--type transcript` will return nothing for runs dispatched after the
cutover. The download-and-distil workflow this skill was built on no
longer has a source.

## Where transcripts live now

Per-job captured traffic is read from the proxy-message store, not from
doc attachments:

```bash
bacio proxy job <dispatch_id>            # per-dispatch parsed message detail
# REST equivalent:
GET /proxy/jobs/{dispatch_id}/transcript
```

## What happens next

**BACI-309** re-platforms this skill onto the captured-message source
(blocked by **BACI-308**, which wires the Monitor-screen drill-down on
the BACI-306 transcript). Until that lands:

- If the user asks for a transcript review, explain that the skill is
  mid-re-platform (BACI-309) and that captured traffic is available via
  `bacio proxy job <dispatch_id>` if they want to inspect a run by hand.
- Do **not** attempt the old `bacio doc download` / digest-script
  workflow — it will find no transcripts for recent runs.

The bundled `scripts/` and `references/` are left in place for BACI-309
to build on; they are not wired into any working flow today.
