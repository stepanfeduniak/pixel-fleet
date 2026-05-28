package session

import "testing"

// Fixtures below are trimmed from real `tmux capture-pane -p` output of live
// sessions taken on 2026-05-28. The earlier detector was Claude-only and even
// for Claude treated "esc to interrupt" as a waiting signal — the regression
// tests at the bottom of this file pin that down.
//
// On 2026-05-28 we also tightened the Idle vs WaitingInput distinction:
// the agent's input prompt ("❯" / "› ") is ALWAYS visible between turns, so
// it cannot tell those two apart. The new rule walks the conversation
// bottom-up (skipping chrome) and only reports WaitingInput when the
// assistant's last few sentences end with a question — i.e. there's an
// actual QnA the user needs to answer.

// ────────────────────────────────────────────────────────────────────────────
// Claude fixtures
// ────────────────────────────────────────────────────────────────────────────

// Claude actively generating: footer carries "esc to interrupt", plus an
// activity line with the "…  · ↓  tokens" combo. From the `local` window.
const claudeWorkingPane = `
* Drizzling… (19s · ↓ 778 tokens)
─────────────────────────────────────────────────────────────────────
❯
─────────────────────────────────────────────────────────────────────
  ⏵⏵ auto mode on (shift+tab to cycle) · esc to interrupt
`

// Claude finished a turn and asked the user a question. From the v2.1.x
// `Debug RTC` pane on 2026-05-28. The assistant's question is one of the
// last conversation lines; "✻ Sautéed for 6m 11s" is chrome, the survey
// overlay below is not strictly chrome but doesn't end with "?".
const claudeWaitingAfterQuestionPane = `
the assistant's preceding analysis paragraph
  Do you want me to restart now to pick up the self-healing _save too, or leave it alone since the dir is intact?
✻ Baked for 24s
● How is Claude doing this session? (optional)
  1: Bad    2: Fine   3: Good   0: Dismiss
─────────────────────────────────────────────────────────────────────
❯ restart it
─────────────────────────────────────────────────────────────────────
  ⏵⏵ auto mode on (shift+tab to cycle) · ← for agents
`

// Claude finished a turn with a statement and is just sitting at the prompt.
// Derived from the `Real robot` pane on 2026-05-28.
const claudeIdleAfterStatementPane = `
● Both cameras are now enumerated (usb-0:1.3 and usb-0:1.4). Re-run ./scripts/start_follower_rtc.sh — it should get past camera open this time.
✻ Cogitated for 8s
─────────────────────────────────────────────────────────────────────
❯ re-run the follower
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

// ────────────────────────────────────────────────────────────────────────────
// Codex fixtures
// ────────────────────────────────────────────────────────────────────────────

// Codex finished a turn with a statement and is sitting at the input prompt.
// From the `Orchestartor` pane on 2026-05-28. The "› Summarize recent commits"
// and the model+cwd footer below it are chrome; "Changes are local and not
// pushed." is the assistant's last conversation line. No question → Idle.
const codexIdleAfterStatementPane = `
  - benchmark smoke run with --inference-duration-s 0.5: completed
  Changes are local and not pushed.
─ Worked for 3m 15s ─────────────────────────────────────────────────
› Summarize recent commits
  gpt-5.5 default fast · ~/research_interface_inloop/working_repositories/noyes-orchestrator
`

// Codex finished a turn by asking the user a question. The "?" on the last
// conversation line is the WaitingInput signal.
const codexWaitingAfterQuestionPane = `
• Drafted the migration script and ran the dry-run.
  Should I commit this and open a PR now, or wait for the staging run to finish?
─ Worked for 1m 22s ─────────────────────────────────────────────────
› (empty)
  gpt-5.5 default fast · ~/repo
`

// Codex mid-turn. Present-tense verb with ellipsis trips Working.
const codexWorkingThinkingPane = `
• Ran git diff --stat
  └  src/foo.py | 4 ++
Thinking…
`

// Codex's radio-button tool-approval modal — verbatim from the live
// `Source policy fix` pane on 2026-05-28. The actual question
// ("Would you like to run the following command?") sits 7+ lines above the
// modal's footer, past the idle-vs-waiting lookback. The "Press enter to
// confirm" footer is the unambiguous marker.
const codexPermissionRadioPane = `
• Ran rmdir node_modules/.tmp node_modules
  └ rmdir: failed to remove 'node_modules': Directory not empty
◦ Running rm node_modules/node_modules
  Would you like to run the following command?
  Reason: Allow removing the temporary node_modules symlink I created in the frontend worktree before relinking it correctly for verification.
  $ rm node_modules/node_modules
› 1. Yes, proceed (y)
  2. Yes, and don't ask again for commands that start with ` + "`rm node_modules/node_modules`" + ` (p)
  3. No, and tell Codex what to do differently (esc)
  Press enter to confirm or esc to cancel
`

// Codex mid-turn, alternative chrome: the "• Working (Xs • esc to interrupt)"
// status line that the gpt-5.5-era TUI shows during streaming. Seen on the
// live `Source policy fix` pane on 2026-05-28.
const codexWorkingEscToInterruptPane = `
    1675 +        )
    1676 +
• Working (4m 03s • esc to interrupt)
› Find and fix a bug in @filename
  gpt-5.5 default fast · ~/repo
`

// ────────────────────────────────────────────────────────────────────────────
// Terminal + cross-cutting fixtures
// ────────────────────────────────────────────────────────────────────────────

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

// ────────────────────────────────────────────────────────────────────────────
// Test cases
// ────────────────────────────────────────────────────────────────────────────

func TestDetectStatus(t *testing.T) {
	cases := []struct {
		name   string
		pane   string
		agent  string
		expect Status
	}{
		// Claude
		{"claude_working_drizzling", claudeWorkingPane, "claude", StatusWorking},
		{"claude_waiting_after_question", claudeWaitingAfterQuestionPane, "claude", StatusWaitingInput},
		{"claude_idle_after_statement", claudeIdleAfterStatementPane, "claude", StatusIdle},
		{"claude_permission_modal", claudePermissionPane, "claude", StatusWaitingInput},
		{"claude_empty_legacy_agent_works_too", claudeWorkingPane, "", StatusWorking},

		// Codex
		{"codex_idle_after_statement", codexIdleAfterStatementPane, "codex", StatusIdle},
		{"codex_waiting_after_question", codexWaitingAfterQuestionPane, "codex", StatusWaitingInput},
		{"codex_working_thinking", codexWorkingThinkingPane, "codex", StatusWorking},
		{"codex_working_esc_to_interrupt", codexWorkingEscToInterruptPane, "codex", StatusWorking},
		{"codex_permission_radio_modal", codexPermissionRadioPane, "codex", StatusWaitingInput},

		// Terminal
		{"terminal_always_idle_even_with_error_text", terminalPane, "terminal", StatusIdle},

		// Cross-agent
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

// ────────────────────────────────────────────────────────────────────────────
// Pinned regressions
// ────────────────────────────────────────────────────────────────────────────

// TestClaudeEscToInterruptIsWorking pins down the original PR #10 bug: the
// footer hint "esc to interrupt" appears WHILE Claude is actively generating
// (the user can press esc to cancel the response), not when it's waiting for
// the next user message. The old code returned StatusWaitingInput here.
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

// TestClaudeFinishedSummaryIsNotWorking guards against the "Sautéed for Xs"
// summary being read as an active spinner. With the Idle/Waiting split the
// expected result tightened from "not Working" to specifically Idle (the
// fixture's last conversation line is a statement, not a question).
func TestClaudeFinishedSummaryIsNotWorking(t *testing.T) {
	pane := `
the assistant's last response sat just above the chrome
✻ Sautéed for 6m 11s
─────────────────────────────────────────────────────────────────────
❯
─────────────────────────────────────────────────────────────────────
  ? for shortcuts · ← for agents
`
	if got := DetectStatus(pane, "claude"); got != StatusIdle {
		t.Fatalf("'Sautéed for Xs' is a completion summary, not a working signal; expected Idle, got %v", got)
	}
}

// TestInputPromptAloneIsNotWaiting pins the new rule directly. A bare "❯" or
// "› " prompt with no pending question in the assistant's last lines must
// classify as Idle, not WaitingInput. This is the inconsistency users were
// hitting where every just-finished session showed the ◆ waiting icon.
func TestInputPromptAloneIsNotWaiting(t *testing.T) {
	t.Run("claude_finished_with_statement", func(t *testing.T) {
		if got := DetectStatus(claudeIdleAfterStatementPane, "claude"); got != StatusIdle {
			t.Fatalf("bare ❯ prompt after a statement must be Idle, got %v", got)
		}
	})
	t.Run("codex_finished_with_statement", func(t *testing.T) {
		if got := DetectStatus(codexIdleAfterStatementPane, "codex"); got != StatusIdle {
			t.Fatalf("bare › prompt after a statement must be Idle, got %v", got)
		}
	})
}

// TestEndsLikeQuestionAcceptsTrailers covers a quirk worth pinning: the
// assistant sometimes wraps the trailing "?" with a closing bracket or quote.
func TestEndsLikeQuestionAcceptsTrailers(t *testing.T) {
	yes := []string{
		"Should we ship?",
		"Should we ship?)",
		"Did you mean \"foo\"?\"",
		"Continue?]",
		"Continue?'",
		"  trailing whitespace ok?  ",
	}
	for _, s := range yes {
		if !endsLikeQuestion(s) {
			t.Errorf("expected question: %q", s)
		}
	}
	no := []string{
		"Done.",
		"Question? Then more text.",
		"...intact (optional)",
		"",
		"  ",
	}
	for _, s := range no {
		if endsLikeQuestion(s) {
			t.Errorf("expected NOT a question: %q", s)
		}
	}
}
