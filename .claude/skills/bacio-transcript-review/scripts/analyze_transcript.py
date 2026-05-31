#!/usr/bin/env python3
"""
analyze_transcript.py — distil a bacio worker's captured Anthropic traffic into
a compact JSON digest the reviewer can reason over.

Source of truth is the reverse-proxy capture (BACI-305/306/308), not the retired
`.jsonl` transcript attachments. Two input modes:

  --job <dispatch_id>     (preferred) shells out to `bacio proxy job <id> -o json`
                          and consumes the assembled per-job transcript
                          (model.AnthropicTranscript: ordered user/assistant
                          messages of text/thinking/tool_use/tool_result blocks,
                          summed usage, auxiliary turns). This is the parsed path
                          and needs a BACI-306+ `bacio` to have populated
                          `proxy_messages` for the dispatch.

  --raw-ids <id,...>      (fallback) when `proxy job` returns "not found" — i.e.
                          the capture index exists but the parser hasn't run yet
                          — fetch each capture's raw `.http` via `bacio proxy raw
                          <id>` and reconstruct the same transcript shape from the
                          request messages[] + response SSE stream. Pass the
                          dispatch's capture ids (oldest-first) from Step 1 of the
                          skill (`bacio proxy captures --dispatch <id> -o json`,
                          sorted by started_at / id ascending).

The transcript shape both modes feed into `analyse_transcript`:

  { "model": ..., "dispatch_id": ..., "usage": {...},
    "messages": [ {"role": "user"|"assistant", "content": [ <block>, ... ]}, ... ],
    "auxiliary": [ {"blocks": [...], "usage": {...}}, ... ] }

where a block is one of:
  {"type":"text","text":...}                              (assistant)
  {"type":"thinking","thinking":...}                      (assistant)
  {"type":"tool_use","id":...,"name":...,"input":{...}}   (assistant)
  {"type":"tool_result","tool_use_id":...,"content":...,"is_error":bool}  (user)

The proxy transcript has NO per-message cwd / gitBranch / timestamp envelope
(those were `.jsonl` fields). Worktree-confinement is therefore inferred from
`Edit`/`Write` `file_path` prefixes and `bacio worktree init`/`rm` arguments
instead of a per-line cwd — slightly weaker but sufficient for the confinement
finding. Per-call timing is unavailable; only the per-job `usage` total survives.

`mode` and `dispatch_id` come from the CLI args (the skill reads them off the
capture rows in Step 1), not from a payload regex — the proxy rows carry both
directly, which is strictly more reliable than the old first-line parse.

Output is JSON to stdout (or --out <path>). Stable schema — see SKILL.md for how
the reviewer consumes each field.
"""

from __future__ import annotations

import argparse
import json
import re
import subprocess
import sys
from collections import Counter, defaultdict
from pathlib import Path


# ---------------------------------------------------------------------------
# CLI shell-outs to bacio (read-only, DB-direct — no running server needed)
# ---------------------------------------------------------------------------

# Sentinel `bacio proxy job`/`capture`/`raw` print when a dispatch / capture has
# no parsed rows or on-disk file yet (the parser hasn't run over its captures).
NOT_FOUND_MARKER = "not found"


def _run_bacio(args: list[str], bacio: str) -> tuple[int, str, str]:
    """Run `<bacio> <args...>`, returning (returncode, stdout, stderr). Never
    raises on a non-zero exit — the caller inspects the code/text (a "not found"
    is an expected, non-exceptional outcome on the parsed path)."""
    proc = subprocess.run(
        [bacio, *args],
        capture_output=True,
        text=True,
        encoding="utf-8",
        errors="replace",
    )
    return proc.returncode, proc.stdout, proc.stderr


def fetch_job_transcript(dispatch_id: str, bacio: str) -> dict | None:
    """`bacio proxy job <id> -o json` → the assembled transcript dict, or None
    when the dispatch has no parsed captures yet (the caller falls back to raw)."""
    code, out, err = _run_bacio(["proxy", "job", str(dispatch_id), "-o", "json"], bacio)
    blob = (out + err).strip()
    if code != 0 or not out.strip():
        if NOT_FOUND_MARKER in blob.lower():
            return None
        raise RuntimeError(f"bacio proxy job {dispatch_id} failed: {blob}")
    return json.loads(out)


def fetch_raw_capture(capture_id: str, bacio: str) -> str | None:
    """`bacio proxy raw <id>` → the raw .http bytes, or None when the capture has
    no on-disk file (pruned / write failed)."""
    code, out, err = _run_bacio(["proxy", "raw", str(capture_id)], bacio)
    blob = (out + err).strip()
    if code != 0 or not out.strip():
        if NOT_FOUND_MARKER in blob.lower():
            return None
        raise RuntimeError(f"bacio proxy raw {capture_id} failed: {blob}")
    return out


# ---------------------------------------------------------------------------
# Raw .http fallback parser — mirrors internal/anthropic/parse.go + assemble.go
# ---------------------------------------------------------------------------
#
# We reconstruct the same transcript shape `bacio proxy job` produces, so the
# digest core (analyse_transcript) is source-agnostic. Only the fields the digest
# needs are extracted — this is a reviewer's distiller, not a faithful port.

# Markers are matched line-anchored so a marker string appearing *inside* the
# captured content (a worker discussing the proxy format, say) doesn't mis-split.
# The Go parser cuts on the bare substring; line-anchoring is strictly safer.
REQUEST_MARKER_RE = re.compile(r"(?m)^==== REQUEST ====[ \t]*$")
RESPONSE_MARKER_RE = re.compile(r"(?m)^==== RESPONSE ====[ \t]*$")
# Recorder truncation markers (internal/api/proxy_recorder.go) — a body past
# proxy.MaxCapturedBody is written verbatim with one of these instead of the
# inflated bytes, so it can't be JSON/SSE-parsed. The markers are bracketed and
# appended after the body bytes; we match the literal bracketed forms (not the
# bare phrase) so a worker that merely *discusses* truncation in its captured
# content doesn't trip the check.
TRUNCATION_HINTS = (
    "[... request body truncated at ",
    "[... response body truncated at ",
    "[gzip body truncated",
)


def _re_cut(pat: re.Pattern, s: str) -> tuple[str, str]:
    """Split s at the first match of pat into (before, after). after is "" when
    the pattern doesn't match (a request-only capture)."""
    m = pat.search(s)
    if not m:
        return s, ""
    return s[: m.start()], s[m.end():]


def split_capture(raw: str) -> tuple[str, str]:
    """Partition a raw .http capture into (request_body, response_body). Headers
    are dropped — the parser reads only the bodies."""
    raw = raw.replace("\r\n", "\n")
    req_part, resp_part = _re_cut(RESPONSE_MARKER_RE, raw)
    req_part = REQUEST_MARKER_RE.sub("", req_part, count=1).lstrip("\n")
    _, _, req_body = req_part.partition("\n\n")
    _, _, resp_body = resp_part.lstrip("\n").partition("\n\n")
    return req_body, resp_body


def looks_truncated(body: str) -> bool:
    """A body is truncated only when the recorder's bracketed marker sits at the
    tail (it's appended after the cut-off bytes). Checking the tail — not the
    whole body — avoids a false positive when the *captured content itself*
    quotes a truncation marker (a worker reviewing this very subsystem does)."""
    return any(h in body[-200:] for h in TRUNCATION_HINTS)


def parse_raw_capture(raw: str) -> dict | None:
    """Parse one raw .http capture into a {request, turn} pair:

      { "model": ..., "system_fp": ..., "message_count": int,
        "has_output_config": bool, "messages": [<AnthropicMessage>, ...],
        "turn": {"blocks": [...], "usage": {...}}, "truncated": bool }

    Returns None when the request body can't be parsed (truncated / malformed).
    Truncation is judged on the actual parse outcome: if the JSON loads, the
    capture is complete no matter what marker-like text its content quotes."""
    req_body, resp_body = split_capture(raw)
    req_body = req_body.strip()
    if not req_body:
        return None
    try:
        req = json.loads(req_body)
    except json.JSONDecodeError:
        # Body didn't parse — a genuine truncation (marker at the tail) is the
        # expected cause; anything else is malformed. Either way it's unusable.
        return None

    # The request parsed, so it is whole. The response SSE may still be a
    # truncated gzip body the recorder couldn't inflate — flag that for the
    # digest without dropping the (complete) request side.
    truncated = looks_truncated(resp_body)

    messages = req.get("messages") or []
    system = req.get("system")
    out_cfg = req.get("output_config")
    return {
        "model": req.get("model") or "",
        "system_fp": _system_fp(system),
        "message_count": len(messages),
        "has_output_config": bool(out_cfg) and out_cfg not in (None, "null"),
        "messages": messages,
        "turn": decode_sse(resp_body),
        "truncated": truncated,
    }


# Claude Code 2.1.158+ prepends a per-request billing header block to the system
# array (`x-anthropic-billing-header: cc_version=...; cc_entrypoint=...`) whose
# value drifts request-to-request. It must be excluded from the thread
# fingerprint or every turn looks like a fresh thread. The BACI-306 Go classifier
# (internal/anthropic/assemble.go) hashes the whole system block and so currently
# mis-splits these threads — a known gap the parsed `proxy job` path inherits;
# the raw fallback works around it here (out-of-scope to fix the Go side).
BILLING_HEADER_HINT = "x-anthropic-billing-header"


def _stable_system(system):
    """Drop the volatile leading billing-header block so the fingerprint reflects
    the durable system prompt, not the per-request billing token."""
    if isinstance(system, list):
        return [
            blk for blk in system
            if not (isinstance(blk, dict)
                    and BILLING_HEADER_HINT in (blk.get("text") or ""))
        ]
    return system


def _system_fp(system) -> str:
    """Stable fingerprint of the request's system block, used only for equality
    across captures of one thread (not the Go SHA-256). The volatile billing
    header is stripped first; identical durable system bytes compare equal, a
    different probe (title-gen sidechain) doesn't."""
    stable = _stable_system(system)
    if stable in (None, "", []):
        return ""
    return json.dumps(stable, sort_keys=True, ensure_ascii=False)


def decode_sse(resp_body: str) -> dict:
    """Reconstruct one assistant turn from a response SSE stream — ordered
    text/thinking/tool_use blocks + merged usage. Mirrors decodeSSE in
    internal/anthropic/parse.go: input_json_delta fragments accumulate per block
    index; a malformed event line is skipped rather than sinking the turn."""
    usage: dict[str, int] = {}
    model = ""
    blocks: dict[int, dict] = {}
    order: list[int] = []

    for chunk in resp_body.split("\n\n"):
        data = _sse_data(chunk)
        if not data:
            continue
        try:
            ev = json.loads(data.strip())
        except json.JSONDecodeError:
            continue
        etype = ev.get("type")
        if etype == "message_start":
            msg = ev.get("message") or {}
            _merge_usage(usage, msg.get("usage"))
            model = msg.get("model") or model
        elif etype == "content_block_start":
            idx = ev.get("index", 0)
            cb = ev.get("content_block") or {}
            p = {"type": cb.get("type") or "", "_tool": False, "_json": ""}
            if cb.get("type") == "tool_use":
                p["_tool"] = True
                p["id"] = cb.get("id") or ""
                p["name"] = cb.get("name") or ""
            elif cb.get("type") == "text":
                p["text"] = cb.get("text") or ""
            elif cb.get("type") == "thinking":
                p["thinking"] = cb.get("thinking") or ""
            blocks[idx] = p
            order.append(idx)
        elif etype == "content_block_delta":
            idx = ev.get("index", 0)
            p = blocks.get(idx)
            if p is None:
                p = {"type": "", "_tool": False, "_json": ""}
                blocks[idx] = p
                order.append(idx)
            delta = ev.get("delta") or {}
            dt = delta.get("type")
            if dt == "text_delta":
                p["type"] = "text"
                p["text"] = (p.get("text") or "") + (delta.get("text") or "")
            elif dt == "thinking_delta":
                p["type"] = "thinking"
                p["thinking"] = (p.get("thinking") or "") + (delta.get("thinking") or "")
            elif dt == "input_json_delta":
                p["_tool"] = True
                p["_json"] += delta.get("partial_json") or ""
        elif etype == "message_delta":
            _merge_usage(usage, ev.get("usage"))

    out_blocks = []
    for idx in order:
        p = blocks.get(idx)
        if p is None:
            continue
        if p["_tool"]:
            blk = {"type": "tool_use", "id": p.get("id", ""), "name": p.get("name", "")}
            j = p["_json"].strip()
            if j:
                try:
                    blk["input"] = json.loads(j)
                except json.JSONDecodeError:
                    blk["input"] = {}
            out_blocks.append(blk)
        elif p["type"] == "text":
            out_blocks.append({"type": "text", "text": p.get("text", "")})
        elif p["type"] == "thinking":
            out_blocks.append({"type": "thinking", "thinking": p.get("thinking", "")})
    return {"model": model, "blocks": out_blocks, "usage": usage}


def _sse_data(block: str) -> str:
    """Concatenate the `data:` payload of one SSE block (one event per block;
    `data:` may span lines per the SSE spec, so we join defensively)."""
    parts = []
    for line in block.split("\n"):
        if line.startswith("data:"):
            parts.append(line[len("data:"):].strip())
    return "".join(parts)


def _merge_usage(dst: dict, src) -> None:
    """Fold an SSE usage object into the turn usage — later non-zero value per
    field (message_delta's output_tokens supersedes message_start's 0)."""
    if not isinstance(src, dict):
        return
    for key in ("input_tokens", "output_tokens",
                "cache_creation_input_tokens", "cache_read_input_tokens"):
        v = src.get(key)
        if v:
            dst[key] = v
    details = src.get("output_tokens_details")
    if isinstance(details, dict) and details.get("thinking_tokens"):
        dst["thinking_tokens"] = details["thinking_tokens"]


def assemble_from_raw(parsed: list[dict], dispatch_id: str | None) -> dict:
    """Assemble per-capture {request, turn} parses (oldest-first) into one job
    transcript — the same shape `bacio proxy job` produces. Mirrors Classify +
    AssembleTranscript: a capture is primary unless it carries an output_config
    probe or switches (system fingerprint / model); a primary capture contributes
    its request-message delta (the user/tool_result turns appended since the prior
    primary count) followed by its assistant turn. Usage sums across primaries."""
    messages: list[dict] = []
    auxiliary: list[dict] = []
    usage: dict[str, int] = {}
    model = ""
    truncated_any = False

    prev_fp = ""
    prev_model = ""
    prev_count = 0

    for pc in parsed:
        if pc is None:
            continue
        truncated_any = truncated_any or pc.get("truncated", False)

        # Auxiliary: a sibling thread (different durable system fingerprint /
        # model) within the dispatch — a title-gen / sidechain probe. NOTE: we do
        # NOT gate on output_config here. The BACI-306 Go classifier treats an
        # output_config probe as auxiliary, but Claude Code 2.1.158+ sends
        # output_config ({"effort": ...}) on every main-conversation turn, so the
        # flag no longer discriminates; the durable-system fingerprint does.
        if prev_fp and (pc["system_fp"] != prev_fp or pc["model"] != prev_model):
            auxiliary.append(pc["turn"])
            continue

        # Primary. Compute the request-message delta vs the prior primary count.
        if not prev_fp:
            delta = pc["messages"]
        elif pc["message_count"] > prev_count:
            delta = pc["messages"][prev_count:]
        else:
            # Compaction / resend — treat as a fresh segment (whole messages[]).
            delta = pc["messages"]

        delta = _drop_leading_assistant(delta)
        messages.extend(delta)
        messages.append({"role": "assistant", "content": pc["turn"]["blocks"]})
        _sum_usage(usage, pc["turn"]["usage"])
        if not model:
            model = pc["turn"].get("model") or pc["model"]

        prev_fp = pc["system_fp"]
        prev_model = pc["model"]
        prev_count = pc["message_count"]

    return {
        "dispatch_id": int(dispatch_id) if dispatch_id and str(dispatch_id).isdigit() else None,
        "model": model,
        "messages": messages,
        "usage": usage,
        "auxiliary": auxiliary,
        "_raw_fallback": True,
        "_truncated": truncated_any,
    }


def _drop_leading_assistant(tail: list[dict]) -> list[dict]:
    """Drop a leading assistant echo from a request delta — the client re-sends
    the prior turn's assistant response, which the prior capture's turn already
    represents; persisting it again double-counts it (requestDelta in
    assemble.go)."""
    i = 0
    while i < len(tail) and (tail[i] or {}).get("role") == "assistant":
        i += 1
    return tail[i:]


def _sum_usage(dst: dict, src) -> None:
    """Accumulate one turn's usage into the running job total (across turns,
    unlike _merge_usage which takes the later value within a single turn)."""
    if not isinstance(src, dict):
        return
    for key in ("input_tokens", "output_tokens", "cache_creation_input_tokens",
                "cache_read_input_tokens", "thinking_tokens"):
        v = src.get(key)
        if v:
            dst[key] = dst.get(key, 0) + v


# ---------------------------------------------------------------------------
# Per-domain extractors (unchanged in spirit from the .jsonl distiller)
# ---------------------------------------------------------------------------

# Recognise a `bacio <subcommand>` invocation inside a Bash command string.
# Captures the subcommand path (e.g. "agent claim", "worktree init", "issue
# state"). Stops at the first flag, redirection, or pipe.
BACIO_CALL_RE = re.compile(r"(?:^|[\s;|&`(])bacio((?:\s+[a-z][\w-]*){1,3})")

# Heuristic: an absolute /.../.claude/worktrees/<slug> is a worker worktree root.
# Capture so we can prefix-test edits against it without a manifest file.
WORKTREE_ROOT_RE = re.compile(r"(/[^\s'\"]+?/\.claude/worktrees/[^\s'\"/]+)")


def parse_bacio_subcommand(cmd: str):
    """Return (subcommand_path, args_after) for the *first* bacio invocation in a
    bash command string, or None. Subcommand is normalised to a space-separated
    string ("agent claim", "issue state")."""
    m = BACIO_CALL_RE.search(cmd)
    if not m:
        return None
    sub = m.group(1).strip()
    sub_path = " ".join(sub.split())
    after = cmd[m.end():].strip()
    return sub_path, after


def classify_worktree_root(path: str) -> str | None:
    """If a path looks like it sits in a worker worktree, return its root; else
    None. Works on both an Edit file_path and a `worktree init`/`rm` argument."""
    if not path:
        return None
    m = WORKTREE_ROOT_RE.match(path) or WORKTREE_ROOT_RE.search(path)
    return m.group(1) if m else None


# Tool-result inspection: did Claude Code's PreToolUse hook (or any source) deny
# the call? Hooks emit a deny payload that lands either as is_error=True with a
# deny phrase, or as text containing the canonical permission-decision string.
DENY_HINTS = (
    "permissionDecision",
    "permission_denied",
    "permission denied by hook",
    "blocked by hook",
)


def is_hook_deny(text: str) -> bool:
    return any(h in text for h in DENY_HINTS)


# ---------------------------------------------------------------------------
# Content-block helpers (transcript model, not .jsonl envelope)
# ---------------------------------------------------------------------------

def iter_blocks(msg: dict):
    content = msg.get("content") if isinstance(msg, dict) else None
    if isinstance(content, list):
        for b in content:
            if isinstance(b, dict):
                yield b


def tool_result_text(block: dict) -> str:
    """Flatten a tool_result block's content (string or block list) to a single
    string for error / hook-deny inspection."""
    content = block.get("content")
    if isinstance(content, str):
        return content
    if isinstance(content, list):
        parts = []
        for c in content:
            if isinstance(c, dict):
                t = c.get("text") or c.get("content") or ""
                if isinstance(t, str):
                    parts.append(t)
        return "\n".join(parts)
    return ""


# ---------------------------------------------------------------------------
# Main analysis — source-agnostic over the transcript model
# ---------------------------------------------------------------------------

def analyse_transcript(transcript: dict, mode: str | None, dispatch_id: str | None) -> dict:
    """Walk an assembled transcript (parsed or raw-fallback) into the digest. The
    transcript is the model.AnthropicTranscript shape: ordered user/assistant
    messages of text/thinking/tool_use/tool_result blocks, summed usage, and
    auxiliary turns."""
    tool_counts = Counter()
    bacio_calls = []           # ordered {sub, args, full_command, msg_index}
    edit_calls = []            # Edit/Write/MultiEdit/NotebookEdit details
    bash_calls = []
    failed_bash = []
    hook_denies = []
    pending_tool_uses = {}     # tool_use_id -> (name, msg_index, input)
    mcp_calls = []
    task_creates = []
    issue_state_calls = []
    worktree_init = []
    worktree_rm = []
    db_overrides = []
    env_overrides = []
    ask_user_question_calls = []
    reply_calls = []
    feature_comment_calls = []
    issue_comment_calls = []
    tag_calls = []
    claim_calls = []
    release_calls = []
    plan_doc_calls = []

    seen_bash_sequence = []    # (msg_index, normalised_cmd)
    worktree_roots_seen = []   # roots inferred from worktree init/rm / edits

    messages = transcript.get("messages") or []

    for mi, msg in enumerate(messages):
        role = msg.get("role")

        if role == "assistant":
            for block in iter_blocks(msg):
                if block.get("type") != "tool_use":
                    continue
                name = block.get("name") or ""
                tool_counts[name] += 1
                inp = block.get("input") or {}
                if not isinstance(inp, dict):
                    inp = {}
                tu_id = block.get("id")
                pending_tool_uses[tu_id] = (name, mi, inp)

                if name == "Bash":
                    cmd = inp.get("command") or ""
                    bash_calls.append({"msg_index": mi, "command": cmd})
                    seen_bash_sequence.append((mi, _normalise_cmd(cmd)))

                    parsed = parse_bacio_subcommand(cmd)
                    if parsed:
                        sub, after = parsed
                        bacio_calls.append({
                            "msg_index": mi, "sub": sub, "args": after,
                            "full_command": cmd,
                        })
                        if "--db " in cmd or "--db=" in cmd:
                            db_overrides.append({"msg_index": mi, "command": cmd})
                        if "--env " in cmd or "--env=" in cmd:
                            env_overrides.append({"msg_index": mi, "command": cmd})

                        if sub == "agent claim":
                            claim_calls.append({"msg_index": mi, "args": after})
                        elif sub == "agent release":
                            release_calls.append({"msg_index": mi, "args": after})
                        elif sub == "issue state":
                            issue_state_calls.append({"msg_index": mi, "args": after})
                        elif sub == "worktree init":
                            worktree_init.append({"msg_index": mi, "args": after})
                        elif sub == "worktree rm":
                            worktree_rm.append({"msg_index": mi, "args": after})
                            wr = classify_worktree_root(after)
                            if wr and wr not in worktree_roots_seen:
                                worktree_roots_seen.append(wr)
                        elif sub == "tag add":
                            tag_calls.append({"msg_index": mi, "args": after})
                        elif sub == "comment add":
                            issue_comment_calls.append({"msg_index": mi, "args": after})
                        elif sub == "feature comment add":
                            feature_comment_calls.append({"msg_index": mi, "args": after})
                        elif sub.startswith("doc ") and (
                            "--type plan" in cmd or "--type designs" in cmd
                        ):
                            plan_doc_calls.append({"msg_index": mi, "sub": sub, "args": after})

                elif name in ("Edit", "Write", "MultiEdit", "NotebookEdit"):
                    fp = inp.get("file_path") or inp.get("path") or ""
                    edit_calls.append({"msg_index": mi, "tool": name, "file_path": fp})
                    wr = classify_worktree_root(fp)
                    if wr and wr not in worktree_roots_seen:
                        worktree_roots_seen.append(wr)

                elif name == "TaskCreate":
                    task_creates.append({
                        "msg_index": mi,
                        "subject": (inp.get("subject") or "")[:120],
                        "description": (inp.get("description") or "")[:200],
                    })

                elif name == "mcp__bacio__ask_user_question":
                    ask_user_question_calls.append({"msg_index": mi, "input": _trim(inp)})
                elif name == "mcp__bacio__reply":
                    reply_calls.append({"msg_index": mi, "input": _trim(inp)})
                elif name and name.startswith("mcp__bacio__"):
                    mcp_calls.append({"msg_index": mi, "name": name, "input": _trim(inp)})

        elif role == "user":
            for block in iter_blocks(msg):
                if block.get("type") != "tool_result":
                    continue
                tu_id = block.get("tool_use_id")
                tu = pending_tool_uses.pop(tu_id, None)
                txt = tool_result_text(block)
                is_err = bool(block.get("is_error"))

                if is_hook_deny(txt):
                    hook_denies.append({
                        "msg_index": mi,
                        "tool": tu[0] if tu else None,
                        "tool_msg_index": tu[1] if tu else None,
                        "input": _trim(tu[2]) if tu else None,
                        "deny_excerpt": _excerpt(txt, 400),
                    })

                if tu and tu[0] == "Bash" and is_err:
                    cmd = (tu[2] or {}).get("command", "")
                    failed_bash.append({
                        "msg_index": mi, "tool_msg_index": tu[1], "command": cmd,
                        "output_excerpt": _excerpt(txt, 300),
                    })

    repeated_cmd_groups = _detect_repeated(seen_bash_sequence)

    # Infer the worker's worktree root: prefer a root seen on an Edit/Write
    # file_path or a worktree init/rm arg (the confinement anchor). The proxy
    # transcript has no per-message cwd, so this is the available signal.
    worktree_root = worktree_roots_seen[0] if worktree_roots_seen else None

    edits_outside = []
    if worktree_root:
        for e in edit_calls:
            fp = e.get("file_path") or ""
            if fp and not fp.startswith(worktree_root):
                edits_outside.append(e)

    usage = transcript.get("usage") or {}
    aux = transcript.get("auxiliary") or []

    digest = {
        "source": "raw_fallback" if transcript.get("_raw_fallback") else "proxy_job",
        "truncated": bool(transcript.get("_truncated")),
        "message_count": len(messages),
        "auxiliary_turn_count": len(aux),
        "dispatch": {
            "issue_key": None,           # set by SKILL.md from the capture rows
            "mode": mode,
            "dispatch_id": dispatch_id or (
                str(transcript["dispatch_id"]) if transcript.get("dispatch_id") else None
            ),
            "model": transcript.get("model") or None,
        },
        "usage": {
            "input_tokens": usage.get("input_tokens", 0),
            "output_tokens": usage.get("output_tokens", 0),
            "cache_creation_input_tokens": usage.get("cache_creation_input_tokens", 0),
            "cache_read_input_tokens": usage.get("cache_read_input_tokens", 0),
            "thinking_tokens": usage.get("thinking_tokens", 0),
        },
        "worktree": {
            "root": worktree_root,
            "roots_seen": worktree_roots_seen[:5],
        },
        "tool_counts": dict(tool_counts.most_common()),
        "bacio_calls": bacio_calls,
        "claim_calls": claim_calls,
        "release_calls": release_calls,
        "issue_state_calls_midrun": issue_state_calls,
        "tag_calls": tag_calls,
        "worktree_init": worktree_init,
        "worktree_rm": worktree_rm,
        "db_overrides": db_overrides,
        "env_overrides": env_overrides,
        "edits": {
            "count": len(edit_calls),
            "all": edit_calls[:80],
            "outside_worktree": edits_outside,
        },
        "bash": {
            "total": len(bash_calls),
            "failed_count": len(failed_bash),
            "failed_samples": failed_bash[:15],
            "repeated_groups": repeated_cmd_groups[:10],
        },
        "mcp": {
            "reply": reply_calls,
            "ask_user_question": ask_user_question_calls,
            "other_bacio_mcp": mcp_calls,
        },
        "comments": {
            "issue_comment_add": issue_comment_calls,
            "feature_comment_add": feature_comment_calls,
        },
        "plan_doc_calls": plan_doc_calls,
        "task_creates": task_creates[:30],
        "hook_denies": hook_denies,
    }
    return digest


# ---------------------------------------------------------------------------
# Small utilities
# ---------------------------------------------------------------------------

def _trim(d, limit=400):
    """Render a dict input compactly, capping any string field."""
    if not isinstance(d, dict):
        return d
    out = {}
    for k, v in d.items():
        if isinstance(v, str) and len(v) > limit:
            out[k] = v[:limit] + f"... [+{len(v)-limit} chars]"
        else:
            out[k] = v
    return out


def _excerpt(s: str, limit: int) -> str:
    s = s or ""
    return s if len(s) <= limit else (s[:limit] + f"... [+{len(s)-limit} chars]")


def _normalise_cmd(cmd: str) -> str:
    """Collapse whitespace and strip trailing redirections so 'foo 2>&1' and
    'foo' compare equal for repeat detection."""
    cmd = re.sub(r"\s+", " ", cmd or "").strip()
    cmd = re.sub(r"\s*2>&1$", "", cmd)
    return cmd


def _detect_repeated(seq, min_repeat=3):
    """Group identical normalised commands. Useful for spotting retry loops."""
    by_cmd = defaultdict(list)
    for idx, cmd in seq:
        by_cmd[cmd].append(idx)
    out = []
    for cmd, idxs in by_cmd.items():
        if len(idxs) >= min_repeat:
            out.append({"command": cmd, "count": len(idxs), "msg_indexes": idxs})
    out.sort(key=lambda x: -x["count"])
    return out


# ---------------------------------------------------------------------------
# Transcript acquisition (the two input modes)
# ---------------------------------------------------------------------------

def load_transcript(args) -> dict:
    """Resolve the transcript per the CLI args: --job (parsed, with automatic raw
    fallback when --raw-ids is also supplied), or --raw-ids alone."""
    bacio = args.bacio

    if args.job:
        tr = fetch_job_transcript(args.job, bacio)
        if tr is not None:
            return tr
        # Parsed path empty (parser hasn't run). Fall back to raw if we were
        # handed the capture ids; otherwise the caller must supply them.
        if not args.raw_ids:
            raise SystemExit(
                f"no parsed transcript for dispatch {args.job} "
                f"(`bacio proxy job` returned not-found). Re-run with --raw-ids "
                f"<id,...> (the dispatch's capture ids from `bacio proxy "
                f"captures --dispatch {args.job} -o json`) to use the raw fallback."
            )

    ids = _split_ids(args.raw_ids)
    if not ids:
        raise SystemExit("no input: pass --job <dispatch_id> or --raw-ids <id,...>")
    parsed = []
    for cid in ids:
        raw = fetch_raw_capture(cid, bacio)
        if raw is None:
            continue
        parsed.append(parse_raw_capture(raw))
    if not any(parsed):
        raise SystemExit("no parseable raw captures for the given ids")
    return assemble_from_raw(parsed, args.job or (ids[0] if ids else None))


def _split_ids(s: str | None) -> list[str]:
    if not s:
        return []
    return [tok for tok in re.split(r"[,\s]+", s.strip()) if tok]


# ---------------------------------------------------------------------------
# CLI
# ---------------------------------------------------------------------------

def main(argv=None):
    p = argparse.ArgumentParser(
        description="Distil a bacio worker's captured Anthropic traffic into a digest"
    )
    p.add_argument("--job", help="dispatch_id — fetch via `bacio proxy job` (parsed path)")
    p.add_argument("--raw-ids", help="comma/space-separated capture ids for the raw fallback (oldest-first)")
    p.add_argument("--mode", help="worker mode (plan/implement/review/ship/...) off the capture rows")
    p.add_argument("--bacio", default="bacio", help="path to the bacio binary (default: bacio on PATH)")
    p.add_argument("--out", help="write digest JSON here instead of stdout")
    args = p.parse_args(argv)

    if not args.job and not args.raw_ids:
        p.error("one of --job or --raw-ids is required")

    transcript = load_transcript(args)
    digest = analyse_transcript(transcript, args.mode, args.job)

    text = json.dumps(digest, indent=2, ensure_ascii=False)
    if args.out:
        Path(args.out).write_text(text, encoding="utf-8")
    else:
        print(text)
    return 0


if __name__ == "__main__":
    sys.exit(main())
