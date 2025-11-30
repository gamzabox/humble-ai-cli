package app

import (
	"testing"
	"time"
)

func TestBuildSystemInformationPromptFormatsLines(t *testing.T) {
	t.Parallel()

	info := systemInformation{
		OS:           "Ubuntu 22.04.4 LTS",
		Architecture: "amd64",
		Locale:       "ko_KR.UTF-8",
		Timezone:     "Asia/Seoul",
		Datetime:     "2025-10-16T16:20:30+09:00",
	}

	got := buildSystemInformationPrompt(info)
	want := "# System Information\n" +
		"- OS: Ubuntu 22.04.4 LTS\n" +
		"- Architecture: amd64\n" +
		"- Locale: ko_KR.UTF-8\n" +
		"- Timezone: Asia/Seoul\n" +
		"- Datetime: 2025-10-16T16:20:30+09:00"

	if got != want {
		t.Fatalf("unexpected system information prompt:\n%s", got)
	}
}

type commandResponse struct {
	output string
	err    error
}

type fakeRunner struct {
	responses []commandResponse
	calls     []struct {
		name string
		args []string
	}
}

func (r *fakeRunner) run(name string, args ...string) (string, error) {
	r.calls = append(r.calls, struct {
		name string
		args []string
	}{name: name, args: append([]string(nil), args...)})
	if len(r.responses) == 0 {
		return "", nil
	}
	resp := r.responses[0]
	r.responses = r.responses[1:]
	return resp.output, resp.err
}

func TestCollectSystemInformationWithWindowsPrefersWMIAndPowershellCulture(t *testing.T) {
	t.Parallel()

	runner := &fakeRunner{
		responses: []commandResponse{
			{output: "22621\n"},
			{output: "Caption=Microsoft Windows 11 Pro\r\n\r\n"},
			{output: "en-US\r\n"},
		},
	}
	src := systemInfoSources{
		goos: "windows",
		arch: "amd64",
		env: func(string) string {
			return ""
		},
		run:            runner.run,
		osReleasePaths: nil,
	}

	now := time.Date(2025, 10, 16, 16, 20, 30, 0, time.UTC)
	info := collectSystemInformationWith(src, now)

	if info.OS != "Microsoft Windows 11 Pro" {
		t.Fatalf("expected WMI caption, got %q", info.OS)
	}
	if info.Locale != "en-US" {
		t.Fatalf("expected locale from powershell culture, got %q", info.Locale)
	}
	if len(runner.calls) != 3 {
		t.Fatalf("expected three command calls, got %d", len(runner.calls))
	}
}

func TestCollectSystemInformationWithWindowsFallsBackToProductName(t *testing.T) {
	t.Parallel()

	runner := &fakeRunner{
		responses: []commandResponse{
			{output: "19044\n"},
			{err: assertError("wmic not available")},
			{output: "Microsoft Windows 10 Pro\r\n"},
			{output: "ko-KR"},
		},
	}
	src := systemInfoSources{
		goos: "windows",
		arch: "amd64",
		env: func(string) string {
			return ""
		},
		run:            runner.run,
		osReleasePaths: nil,
	}

	now := time.Date(2025, 10, 16, 16, 20, 30, 0, time.UTC)
	info := collectSystemInformationWith(src, now)

	if info.OS != "Microsoft Windows 10 Pro" {
		t.Fatalf("expected ProductName fallback, got %q", info.OS)
	}
	if info.Locale != "ko-KR" {
		t.Fatalf("expected locale from powershell culture, got %q", info.Locale)
	}
}

func TestDetectLocalePrefersEnvOnWindows(t *testing.T) {
	t.Parallel()

	runner := &fakeRunner{
		responses: []commandResponse{
			{output: "22621"},
			{output: "Caption=Windows Server 2022"},
		},
	}
	env := map[string]string{
		"LANG": "fr-FR",
	}

	src := systemInfoSources{
		goos:           "windows",
		arch:           "amd64",
		env:            func(key string) string { return env[key] },
		run:            runner.run,
		osReleasePaths: nil,
	}

	now := time.Date(2025, 10, 16, 16, 20, 30, 0, time.UTC)
	info := collectSystemInformationWith(src, now)

	if info.Locale != "fr-FR" {
		t.Fatalf("expected locale from env, got %q", info.Locale)
	}
	if len(runner.calls) != 2 {
		t.Fatalf("expected build + WMI call, got %d", len(runner.calls))
	}
}

func TestNormalizeWindowsVersionNameUpgradesWindows10NameWhenBuildIs11(t *testing.T) {
	t.Parallel()

	name := normalizeWindowsVersionName("Microsoft Windows 10 Pro", 22621)
	if name != "Microsoft Windows 11 Pro" {
		t.Fatalf("expected Windows 11 normalized name, got %q", name)
	}
	if normalizeWindowsVersionName("", 22621) != "Windows 11" {
		t.Fatalf("expected Windows 11 for empty name with build>=22000")
	}
	if normalizeWindowsVersionName("Windows 10 Home", 19044) != "Windows 10 Home" {
		t.Fatalf("expected original name when build < 22000")
	}
}

type staticError string

func (e staticError) Error() string { return string(e) }

func assertError(msg string) error {
	return staticError(msg)
}
