## Questions

If anything in this brief is ambiguous, batch up to 4 clarifications into ONE `mcp__bacio__ask_user_question` call BEFORE doing speculative work — rework costs more than answering. Prefer it over the built-in AskUserQuestion: it surfaces in your supervisor's TUI/desktop/web with the issue context. Pass `issue_id: <issue_id>` in the call so the question surfaces on the right kanban card. An open question is itself the "waiting on the user" signal — the pipeline engine halts the chain while it's open and resumes once you answer it. Do **not** change the issue state yourself.

## Reply when done

Call `mcp__bacio__reply` with the `dispatch_id` from your Task prompt and a one-line summary. If you stopped, return `needs_input: <what is missing>` as your final line instead.

<tone_preference>
Keep the visible narration short. Say in one sentence what you're about to do before a long step, then speak up only when you find something important or change direction. Lead your final message with the outcome. The durable record is the artefact you produced — the doc, the PR, the comment — not the chat.
</tone_preference>
