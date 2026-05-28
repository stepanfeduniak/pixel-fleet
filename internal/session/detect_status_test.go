package session

import "testing"

// Fixtures below are trimmed from real `tmux capture-pane -p` output of live
// sessions taken on 2026-05-28. The earlier detector was Claude-only and even
// for Claude treated "esc to interrupt" as a waiting signal — the regression
// case at the bottom of this file pins that behavior down for good.

// Claude actively generating: footer carries "esc to interrupt", and an
// activity line with the "…  · ↓  tokens" combo. From the `local` window.
const claudeWorkingPane = `
* Drizzling… (19s · ↓ 778 tokens)
─────────────────────────────────────────────────────────────────────
❯
─────────────────────────────────────────────────────────────────────
  ⏵⏵ auto mode on (shift+tab to cycle) · esc to interrupt
`

// Claude finished, waiting for the user to send the next message. Note the
// past-tense "Sautéed for 6m 11s" summary which must NOT be flagged working,
// and the absence of "esc to interrupt" in the footer. From `Debug RTC`.
const claudeWaitingPane = `
Want me to commit + push this on main like the RTC work, or leave it uncommitted for review?
✻ Sautéed for 6m 11s
─────────────────────────────────────────────────────────────────────
❯ commit and push to main
─────────────────────────────────────────────────────────────────────
  ⏵⏵ auto mode on (shift+tab to cycle) · ← for agents
`

// Claude permission modal — tool approval.
const claudePermissionPane = `
● Bash(rm -rf /tmp/foo)
  Do you want to proceed?
  1. Yes
  2. No
  Esc to cancel
`

// Codex idle/waiting after a completed turn. From `Orchestartor`. The "›" at
// the start of the input line is the discriminator; the line below it is the
// model+cwd footer.
const codexWaitingPane = `
─ Worked for 3m 15s ─────────────────────────────────────────────────
› Summarize recent commits
  gpt-5.5 default fast · ~/research_interface_inloop/working_repositories/noyes-orchestrator
`

// Codex mid-turn. Synthesized from the chrome — a present-tense verb with
// an ellipsis is what flips status to working.
const codexWorkingPane = `
• Ran git diff --stat
  └  src/foo.py | 4 ++
Thinking…
`

// Plain shell. No LLM markers; status should not be inferred from log content.
// From `follower` (a crashed Python script). Without per-agent dispatch this
// would land on unpredictable Claude heuristics.
const terminalPane = `
ConnectionError: Failed to open OpenCVCamera(/dev/v4l/by-path/...).
inloop@inloop-EQ:~/in-loop/real_robot_gym$
`

// SSH-level error — independent of agent.
const sshErrorPane = `
ssh: connect to host beelink-2-de port 22: Connection refused
[cs] viewer disconnected (ssh exit 255 after 1s). Reconnecting...
`

func TestDetectStatus(t *testing.T) {
	cases := []struct {
		name   string
		pane   string
		agent  string
		expect Status
	}{
		{"claude_working_drizzling", claudeWorkingPane, "claude", StatusWorking},
		{"claude_waiting_at_prompt", claudeWaitingPane, "claude", StatusWaitingInput},
		{"claude_permission_modal", claudePermissionPane, "claude", StatusWaitingInput},
		{"claude_empty_legacy_agent_works_too", claudeWorkingPane, "", StatusWorking},

		{"codex_waiting_at_prompt", codexWaitingPane, "codex", StatusWaitingInput},
		{"codex_working_thinking", codexWorkingPane, "codex", StatusWorking},

		{"terminal_always_idle_even_with_error_text", terminalPane, "terminal", StatusIdle},

		{"ssh_error_regardless_of_agent_claude", sshErrorPane, "claude", StatusError},
		{"ssh_error_regardless_of_agent_codex", sshErrorPane, "codex", StatusError},
		{"ssh_error_regardless_of_agent_terminal", sshErrorPane, "terminal", StatusError},

		{"empty_output_is_unknown", "", "claude", StatusUnknown},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := DetectStatus(c.pane, c.agent)
			if got != c.expect {
				t.Fatalf("DetectStatus(agent=%q): got %v, want %v", c.agent, got, c.expect)
			}
		})
	}
}

// TestClaudeEscToInterruptIsWorking pins down the specific bug the previous
// detector had: the footer hint "esc to interrupt" appears WHILE Claude is
// actively generating (the user can press esc to cancel the response), not
// when it's waiting for the next user message. The old code returned
// StatusWaitingInput here; we now return StatusWorking.
func TestClaudeEscToInterruptIsWorking(t *testing.T) {
	pane := `
some prior tool output
─────────────────────────────────────────────────────────────────────
❯
─────────────────────────────────────────────────────────────────────
  ⏵⏵ auto mode on (shift+tab to cycle) · esc to interrupt
`
	if got := DetectStatus(pane, "claude"); got != StatusWorking {
		t.Fatalf("regression: 'esc to interrupt' must classify Claude as Working, got %v", got)
	}
}

// TestClaudeFinishedSummaryIsNotWorking guards against another easy mistake:
// a past-tense "Sautéed for 6m 11s" / "Brewed for 33s" line is post-completion
// chrome, not an active spinner. It must not trip the working heuristic.
func TestClaudeFinishedSummaryIsNotWorking(t *testing.T) {
	pane := `
the assistant's last response
✻ Sautéed for 6m 11s
─────────────────────────────────────────────────────────────────────
❯
─────────────────────────────────────────────────────────────────────
  ? for shortcuts · ← for agents
`
	if got := DetectStatus(pane, "claude"); got != StatusWaitingInput {
		t.Fatalf("'Sautéed for Xs' is a completion summary, not a working signal: got %v", got)
	}
}
