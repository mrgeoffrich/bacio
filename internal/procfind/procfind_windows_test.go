//go:build windows

package procfind

import "testing"

func TestParseNetstat(t *testing.T) {
	out := []byte("\r\n" +
		"Active Connections\r\n" +
		"\r\n" +
		"  Proto  Local Address          Foreign Address        State           PID\r\n" +
		"  TCP    127.0.0.1:5401         0.0.0.0:0              LISTENING       48213\r\n" +
		"  TCP    0.0.0.0:5401           0.0.0.0:0              LISTENING       48213\r\n" +
		"  TCP    127.0.0.1:5320         0.0.0.0:0              LISTENING       777\r\n" +
		"  TCP    127.0.0.1:5401         10.0.0.1:55000         ESTABLISHED     900\r\n")
	got := parseNetstat(out, 5401)
	if len(got) != 1 || got[0] != 48213 {
		t.Fatalf("parseNetstat = %v, want [48213]", got)
	}
}

func TestParseNetstatNoMatch(t *testing.T) {
	out := []byte("  TCP    127.0.0.1:5320  0.0.0.0:0  LISTENING  777\r\n")
	if got := parseNetstat(out, 5401); len(got) != 0 {
		t.Errorf("parseNetstat = %v, want empty", got)
	}
}

func TestTasklistImageStubbed(t *testing.T) {
	orig := runCommand
	defer func() { runCommand = orig }()
	runCommand = func(name string, args ...string) ([]byte, error) {
		return []byte("\"bacio.exe\",\"48213\",\"Console\",\"1\",\"12,345 K\"\r\n"), nil
	}
	if got := tasklistImage(48213); got != "bacio.exe" {
		t.Errorf("tasklistImage = %q, want bacio.exe", got)
	}
}

func TestListenersOnPortStubbed(t *testing.T) {
	orig := runCommand
	defer func() { runCommand = orig }()
	runCommand = func(name string, args ...string) ([]byte, error) {
		switch name {
		case "netstat":
			return []byte("  TCP    127.0.0.1:5401  0.0.0.0:0  LISTENING  48213\r\n"), nil
		case "tasklist":
			return []byte("\"bacio.exe\",\"48213\",\"Console\",\"1\",\"12,345 K\"\r\n"), nil
		}
		t.Fatalf("unexpected command %q", name)
		return nil, nil
	}
	got, err := ListenersOnPort(5401)
	if err != nil {
		t.Fatalf("ListenersOnPort: %v", err)
	}
	if len(got) != 1 || got[0].PID != 48213 || got[0].Command != "bacio.exe" {
		t.Fatalf("got %+v, want one {48213 bacio.exe}", got)
	}
}
