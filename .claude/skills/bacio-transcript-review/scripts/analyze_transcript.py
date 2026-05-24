#!/usr/bin/env python3
"""
analyze_transcript.py — distil a bacio worker .jsonl transcript into a
compact JSON digest the reviewer can reason over.

A transcript is one JSON object per line. The shapes we care about:

  user message   { "type": "user",
                   "message": { "role": "user",
                                "content": <str> | [ {type:"text"|"tool_result", ...} ] },
                   "cwd": ..., "gitBranch": ..., "sessionId": ...,
                   "agentId": ..., "timestamp": ... }

  assistant      { "type": "assistant",
                   "message": { "role": "assistant",
                                "content": [ {type:"text"|"tool_use", name, input}, ... ] } }

The first user line is the dispatch payload (e.g. "<issue_id>BACI-x</issue_id>
<mode>ship</mode><dispatch_id>123</dispatch_id>"). cwd on every line tells us
which worktree the agent is in. tool_use carries the action; the matching
tool_result lands in the next user line with the same tool_use_id.

Output is JSON to stdout (or --out <path>). Stable schema — see
SKILL.md for how the reviewer consumes each field.
"""

from __future__ import annotations

import argparse
import json
import re
import sys
from collections import Counter, defaultdict
from pathlib import Path


# ---------------------------------------------------------------------------
# Parsing helpers
# ---------------------------------------------------------------------------

DISPATCH_RE = re.compile(
    r"<issue_id>(?P<issue>[^<]+)</issue_id>\s*"
    r"<mode>(?P<mode>[^<]+)</mode>\s*"
    r"<dispatch_id>(?P<dispatch_id>[^<]+)</dispatch_id>"
)

# Recognise a `bacio <subcommand>` invocation inside a Bash command string.
# Captures the subcommand path (e.g. "agent claim", "worktree init", "issue
# state", "doc upsert"). Stops at the first flag, redirection, or pipe.
BACIO_CALL_RE = re.compile(r"(?:^|[\s;|&`(])bacio((?:\s+[a-z][\w-]*){1,3})")

# Heuristic: an absolute /Users/.../bacio[-anything]/.claude/worktrees/<slug>
# is a worker worktree root. Capture so we can prefix-test edits / cwd against
# it without depending on a manifest file.
WORKTREE_ROOT_RE = re.compile(r"(/[^\s'\"]+?/\.claude/worktrees/[^\s'\"/]+)")


def load_lines(path: Path):
    """Yield parsed JSON objects. Skip blank lines and malformed lines (a
    truncated transcript can end mid-line)."""
    with path.open("r", encoding="utf-8", errors="replace") as f:
        for lineno, raw in enumerate(f, start=1):
            raw = raw.strip()
            if not raw:
                continue
            try:
                yield lineno, json.loads(raw)
            except json.JSONDecodeError:
                # Truncation footer or partial line — record and stop, anything
                # after a malformed line is untrustworthy.
                return


def first_text_content(msg):
    """Return the first text chunk from a message's content (str or list)."""
    content = msg.get("content") if isinstance(msg, dict) else None
    if isinstance(content, str):
        return content
    if isinstance(content, list):
        for c in content:
            if isinstance(c, dict) and c.get("type") == "text":
                return c.get("text") or ""
    return ""


def iter_content_blocks(msg):
    content = msg.get("content") if isinstance(msg, dict) else None
    if isinstance(content, list):
        for c in content:
            if isinstance(c, dict):
                yield c


def tool_result_text(block):
    """Flatten a tool_result block's content into a single string."""
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
# Per-domain extractors
# ---------------------------------------------------------------------------

def parse_bacio_subcommand(cmd: str):
    """Return (subcommand_path, args_after) for the *first* bacio invocation
    in a bash command string, or None if not a bacio call. Subcommand is
    normalised to a space-separated string ("agent claim", "issue state")."""
    m = BACIO_CALL_RE.search(cmd)
    if not m:
        return None
    sub = m.group(1).strip()
    # Trim to known max depth (3) — anything past is likely a value, not a verb.
    parts = sub.split()
    sub_path = " ".join(parts)
    after = cmd[m.end():].strip()
    return sub_path, after


def classify_worktree_root(cwd: str) -> str | None:
    """If cwd looks like a worker worktree, return its root path; else None."""
    if not cwd:
        return None
    m = WORKTREE_ROOT_RE.match(cwd)
    if not m:
        # cwd is already inside a worktree subdir
        m = WORKTREE_ROOT_RE.search(cwd)
    return m.group(1) if m else None


# Tool-result inspection: did Claude Code's PreToolUse hook (or any source)
# deny the call? Hooks emit a "deny" payload that lands either as
# is_error=True with a deny phrase in the text, or as text containing the
# canonical "permissionDecision".
DENY_HINTS = (
    "permissionDecision",
    "permission_denied",
    "permission denied by hook",
    "blocked by hook",
)


def is_hook_deny(text: str) -> bool:
    return any(h in text for h in DENY_HINTS)


# ---------------------------------------------------------------------------
# Main analysis
# ---------------------------------------------------------------------------

def analyse(path: Path) -> dict:
    issue = mode = dispatch_id = None
    agent_id = session_id = version = None
    cwd_observed = []
    branch_observed = []
    first_ts = last_ts = None

    tool_counts = Counter()
    bacio_calls = []           # ordered list of {sub, args, line, cwd}
    edit_calls = []            # Edit/Write/MultiEdit/NotebookEdit details
    bash_calls = []            # all Bash commands (full string)
    failed_bash = []
    hook_denies = []
    pending_tool_uses = {}     # tool_use_id -> (name, line, input)
    mcp_calls = []             # mcp__bacio__* calls
    task_creates = []          # TaskCreate input subjects (first few)
    issue_state_calls = []     # direct `bacio issue state ...` mid-run
    worktree_init = []         # cwd + args when seen
    worktree_rm = []
    db_overrides = []          # any bacio call with --db <path>
    env_overrides = []         # any bacio call with --env <path>
    ask_user_question_calls = []
    attach_transcript_calls = []
    reply_calls = []
    feature_comment_calls = []
    issue_comment_calls = []
    tag_calls = []             # bacio tag add ...
    claim_calls = []
    release_calls = []
    plan_doc_calls = []

    repeated_cmd_groups = []   # filled at end
    seen_bash_sequence = []    # (lineno, normalised_cmd)

    total_lines = 0

    for lineno, obj in load_lines(path):
        total_lines += 1
        ts = obj.get("timestamp")
        if ts:
            first_ts = first_ts or ts
            last_ts = ts
        cwd = obj.get("cwd") or ""
        branch = obj.get("gitBranch")
        if cwd:
            cwd_observed.append(cwd)
        if branch:
            branch_observed.append(branch)
        agent_id = agent_id or obj.get("agentId")
        session_id = session_id or obj.get("sessionId")
        version = version or obj.get("version")

        msg_type = obj.get("type")
        msg = obj.get("message") or {}

        # First-line dispatch parse — the dispatch payload is plain text on
        # the very first user message of the transcript.
        if issue is None:
            text = first_text_content(msg)
            m = DISPATCH_RE.search(text or "")
            if m:
                issue = m.group("issue")
                mode = m.group("mode")
                dispatch_id = m.group("dispatch_id")

        if msg_type == "assistant":
            for block in iter_content_blocks(msg):
                if block.get("type") != "tool_use":
                    continue
                name = block.get("name") or ""
                tool_counts[name] += 1
                inp = block.get("input") or {}
                tool_use_id = block.get("id")
                pending_tool_uses[tool_use_id] = (name, lineno, inp, cwd)

                # Per-tool extractors
                if name == "Bash":
                    cmd = inp.get("command") or ""
                    bash_calls.append({"line": lineno, "cwd": cwd, "command": cmd})
                    seen_bash_sequence.append((lineno, _normalise_cmd(cmd)))

                    parsed = parse_bacio_subcommand(cmd)
                    if parsed:
                        sub, after = parsed
                        bacio_calls.append({
                            "line": lineno, "cwd": cwd, "sub": sub, "args": after,
                            "full_command": cmd,
                        })
                        if "--db " in cmd or "--db=" in cmd:
                            db_overrides.append({"line": lineno, "command": cmd})
                        if "--env " in cmd or "--env=" in cmd:
                            env_overrides.append({"line": lineno, "command": cmd})

                        if sub == "agent claim":
                            claim_calls.append({"line": lineno, "args": after})
                        elif sub == "agent release":
                            release_calls.append({"line": lineno, "args": after})
                        elif sub == "issue state":
                            issue_state_calls.append({"line": lineno, "args": after})
                        elif sub == "worktree init":
                            worktree_init.append({"line": lineno, "cwd": cwd, "args": after})
                        elif sub == "worktree rm":
                            worktree_rm.append({"line": lineno, "cwd": cwd, "args": after})
                        elif sub == "tag add":
                            tag_calls.append({"line": lineno, "args": after})
                        elif sub == "comment add":
                            issue_comment_calls.append({"line": lineno, "args": after})
                        elif sub == "feature comment add":
                            feature_comment_calls.append({"line": lineno, "args": after})
                        elif sub.startswith("doc ") and (
                            "--type plan" in cmd or "--type designs" in cmd
                        ):
                            plan_doc_calls.append({"line": lineno, "sub": sub, "args": after})

                elif name in ("Edit", "Write", "MultiEdit", "NotebookEdit"):
                    fp = inp.get("file_path") or inp.get("path") or ""
                    edit_calls.append({
                        "line": lineno, "tool": name, "file_path": fp, "cwd": cwd,
                    })

                elif name == "TaskCreate":
                    task_creates.append({
                        "line": lineno,
                        "subject": (inp.get("subject") or "")[:120],
                        "description": (inp.get("description") or "")[:200],
                    })

                elif name == "mcp__bacio__ask_user_question":
                    ask_user_question_calls.append({"line": lineno, "input": _trim(inp)})
                elif name == "mcp__bacio__attach_transcript":
                    attach_transcript_calls.append({"line": lineno, "input": _trim(inp)})
                elif name == "mcp__bacio__reply":
                    reply_calls.append({"line": lineno, "input": _trim(inp)})
                elif name and name.startswith("mcp__bacio__"):
                    mcp_calls.append({"line": lineno, "name": name, "input": _trim(inp)})

        elif msg_type == "user":
            # Tool results from earlier tool_use calls. Inspect for errors,
            # hook denies, and failed bash exit codes.
            for block in iter_content_blocks(msg):
                if block.get("type") != "tool_result":
                    continue
                tu_id = block.get("tool_use_id")
                tu = pending_tool_uses.pop(tu_id, None)
                txt = tool_result_text(block)
                is_err = bool(block.get("is_error"))

                if is_hook_deny(txt):
                    hook_denies.append({
                        "line": lineno,
                        "tool": tu[0] if tu else None,
                        "tool_line": tu[1] if tu else None,
                        "input": _trim(tu[2]) if tu else None,
                        "deny_excerpt": _excerpt(txt, 400),
                    })

                if tu and tu[0] == "Bash" and is_err:
                    cmd = (tu[2] or {}).get("command", "")
                    failed_bash.append({
                        "line": lineno, "tool_line": tu[1], "command": cmd,
                        "output_excerpt": _excerpt(txt, 300),
                    })

    # Post-pass: detect repeated bash commands close together.
    repeated_cmd_groups = _detect_repeated(seen_bash_sequence)

    # Derive worktree root from cwd observations. The first cwd that looks
    # like a worker worktree wins.
    worktree_root = None
    for cwd in cwd_observed:
        wr = classify_worktree_root(cwd)
        if wr:
            worktree_root = wr
            break

    # Did any edit land outside the worktree root?
    edits_outside = []
    if worktree_root:
        for e in edit_calls:
            fp = e.get("file_path") or ""
            if fp and not fp.startswith(worktree_root):
                edits_outside.append(e)

    # bacio calls that ran from outside the worktree without --env. A bacio
    # call is "outside" when its cwd is set and doesn't sit under the worker's
    # worktree root.
    bacio_outside_worktree = []
    if worktree_root:
        for call in bacio_calls:
            ccwd = call.get("cwd") or ""
            if not ccwd:
                continue
            if ccwd.startswith(worktree_root):
                continue
            full = call.get("full_command", "")
            if "--env" in full:
                continue
            bacio_outside_worktree.append(call)

    digest = {
        "transcript_path": str(path),
        "total_lines": total_lines,
        "dispatch": {
            "issue_key": issue,
            "mode": mode,
            "dispatch_id": dispatch_id,
            "agent_id": agent_id,
            "session_id": session_id,
            "claude_code_version": version,
        },
        "timing": {
            "first_ts": first_ts,
            "last_ts": last_ts,
        },
        "worktree": {
            "root": worktree_root,
            "cwd_first": cwd_observed[0] if cwd_observed else None,
            "cwd_distinct": _distinct(cwd_observed)[:10],
            "branch_distinct": _distinct(branch_observed)[:5],
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
        "bacio_calls_outside_worktree_no_env": bacio_outside_worktree,
        "edits": {
            "count": len(edit_calls),
            "all": edit_calls[:80],          # cap, listed for review; outside list is the priority
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
            "attach_transcript": attach_transcript_calls,
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


def _distinct(seq):
    seen = []
    for x in seq:
        if x not in seen:
            seen.append(x)
    return seen


def _normalise_cmd(cmd: str) -> str:
    """Collapse whitespace and strip trailing redirections so 'foo 2>&1' and
    'foo' compare equal for repeat detection."""
    cmd = re.sub(r"\s+", " ", cmd or "").strip()
    cmd = re.sub(r"\s*2>&1$", "", cmd)
    return cmd


def _detect_repeated(seq, min_repeat=3):
    """Group identical normalised commands. Useful for spotting retry loops."""
    by_cmd = defaultdict(list)
    for line, cmd in seq:
        by_cmd[cmd].append(line)
    out = []
    for cmd, lines in by_cmd.items():
        if len(lines) >= min_repeat:
            out.append({"command": cmd, "count": len(lines), "lines": lines})
    out.sort(key=lambda x: -x["count"])
    return out


# ---------------------------------------------------------------------------
# CLI
# ---------------------------------------------------------------------------

def main(argv=None):
    p = argparse.ArgumentParser(description="Distil a bacio worker transcript")
    p.add_argument("transcript", help="Path to .jsonl transcript")
    p.add_argument("--out", help="Write digest JSON here instead of stdout")
    args = p.parse_args(argv)

    path = Path(args.transcript)
    if not path.exists():
        print(f"transcript not found: {path}", file=sys.stderr)
        return 2

    digest = analyse(path)
    text = json.dumps(digest, indent=2, ensure_ascii=False)
    if args.out:
        Path(args.out).write_text(text)
    else:
        print(text)
    return 0


if __name__ == "__main__":
    sys.exit(main())
