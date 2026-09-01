package session

import (
	"encoding/base64"
	"regexp"
	"strings"
	"testing"

	_ "github.com/stepanfeduniak/pixel-fleet/internal/apps/builtin"
)

// Every ordinary session is a login shell. This is the whole premise of the
// terminal-only model, and it is one careless fallback away from breaking:
// apps.Resolve("") returns the registry's first-registered app, which today
// is claude, so an empty Agent used to exec claude rather than a shell.
func TestOrdinarySessionLaunchesAShell(t *testing.T) {
	local := BuildShellCommand(BuildOpts{
		Machine:    "home",
		Path:       "~/proj",
		WindowName: "work",
	})
	if !strings.Contains(local, "SHELL") {
		t.Errorf("local launch does not exec a shell: %s", local)
	}
	if strings.Contains(local, "claude") || strings.Contains(local, "codex") {
		t.Errorf("local launch execs an agent: %s", local)
	}

	remote := BuildShellCommand(BuildOpts{
		Machine:           "gpu-01",
		Path:              "~/proj",
		WindowName:        "work",
		RemoteSessionName: "cs-work-deadbeef",
	})
	launch := decodeFirstPayload(t, remote)
	if !strings.Contains(launch, "SHELL") {
		t.Errorf("remote launch script does not exec a shell:\n%s", launch)
	}
	if strings.Contains(launch, "exec claude") || strings.Contains(launch, "exec codex") {
		t.Errorf("remote launch script execs an agent:\n%s", launch)
	}
}

// A system-app window still names its app and must keep launching it.
func TestSystemAppWindowStillLaunchesItsApp(t *testing.T) {
	cmd := BuildShellCommand(BuildOpts{
		Machine:    "home",
		Agent:      "skills-viewer",
		WindowName: "skills",
	})
	if strings.Contains(cmd, "SHELL") {
		t.Errorf("system app window fell back to a shell: %s", cmd)
	}
}

// The remote bootstrap is assembled with fmt.Sprintf. A verb left without an
// argument does not fail the build — it ships "%!s(MISSING)" into a shell
// script that then runs on the user's machine.
func TestRemoteBootstrapHasNoFormattingErrors(t *testing.T) {
	for _, opts := range []BuildOpts{
		{Machine: "gpu-01", Path: "~/p", WindowName: "w", RemoteSessionName: "cs-w-1", Clipboard: true},
		{Machine: "gpu-01", Path: "~/p", WindowName: "w", RemoteSessionName: "cs-w-1", Clipboard: false},
	} {
		cmd := BuildShellCommand(opts)
		if strings.Contains(cmd, "%!") {
			t.Errorf("format verb left unfilled (Clipboard=%v):\n%s", opts.Clipboard, cmd)
		}
	}
}

// decodeFirstPayload returns the inner launch script (the first base64 blob),
// as opposed to decodeBootstrap which returns the outer bootstrap.
func decodeFirstPayload(t *testing.T, cmd string) string {
	t.Helper()
	re := regexp.MustCompile(`echo ([A-Za-z0-9+/=]{16,}) \| base64 -d`)
	m := re.FindAllStringSubmatch(cmd, -1)
	if len(m) == 0 {
		t.Fatalf("no base64 payload in command: %s", cmd)
	}
	// The outer bootstrap embeds the launch script, so decode outer-first.
	outer, err := base64.StdEncoding.DecodeString(m[len(m)-1][1])
	if err != nil {
		t.Fatalf("decode bootstrap: %v", err)
	}
	inner := re.FindStringSubmatch(string(outer))
	if inner == nil {
		t.Fatalf("no launch script inside the bootstrap:\n%s", outer)
	}
	raw, err := base64.StdEncoding.DecodeString(inner[1])
	if err != nil {
		t.Fatalf("decode launch script: %v", err)
	}
	return string(raw)
}
