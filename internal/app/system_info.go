package app

import (
	"os"
	"os/exec"
	"runtime"
	"strconv"
	"strings"
	"time"
)

type systemInformation struct {
	OS           string
	Architecture string
	Locale       string
	Timezone     string
	Datetime     string
}

type commandRunner func(name string, args ...string) (string, error)

type systemInfoSources struct {
	goos           string
	arch           string
	env            func(string) string
	run            commandRunner
	osReleasePaths []string
}

func defaultSystemInfoSources() systemInfoSources {
	return systemInfoSources{
		goos:           runtime.GOOS,
		arch:           runtime.GOARCH,
		env:            os.Getenv,
		run:            runCommand,
		osReleasePaths: osReleasePaths,
	}
}

func buildSystemInformationPrompt(info systemInformation) string {
	builder := strings.Builder{}
	builder.WriteString("# System Information\n")
	builder.WriteString("- OS: ")
	builder.WriteString(info.OS)
	builder.WriteString("\n- Architecture: ")
	builder.WriteString(info.Architecture)
	builder.WriteString("\n- Locale: ")
	builder.WriteString(info.Locale)
	builder.WriteString("\n- Timezone: ")
	builder.WriteString(info.Timezone)
	builder.WriteString("\n- Datetime: ")
	builder.WriteString(info.Datetime)
	return builder.String()
}

func collectSystemInformation(now time.Time) systemInformation {
	return collectSystemInformationWith(defaultSystemInfoSources(), now)
}

func collectSystemInformationWith(src systemInfoSources, now time.Time) systemInformation {
	return systemInformation{
		OS:           detectOSVersionWith(src),
		Architecture: src.arch,
		Locale:       detectLocaleWith(src),
		Timezone:     detectTimezone(now),
		Datetime:     now.Format(time.RFC3339),
	}
}

func detectOSVersionWith(src systemInfoSources) string {
	switch src.goos {
	case "windows":
		build := detectWindowsBuildNumber(src.run)
		name := detectWindowsCaption(src.run)
		if name == "" {
			name = detectWindowsProductName(src.run)
		}
		return normalizeWindowsVersionName(name, build)
	case "linux":
		if pretty := readOSReleasePrettyNameFrom(src.osReleasePaths); pretty != "" {
			return pretty
		}
	}
	return src.goos
}

func detectLocaleWith(src systemInfoSources) string {
	for _, key := range []string{"LC_ALL", "LC_MESSAGES", "LANG"} {
		if value := strings.TrimSpace(src.env(key)); value != "" {
			return value
		}
	}
	if src.goos == "windows" {
		if culture := detectWindowsCulture(src.run); culture != "" {
			return culture
		}
	}
	return "unknown"
}

func detectWindowsCaption(run commandRunner) string {
	output, err := run("wmic", "os", "get", "Caption", "/value")
	if err != nil {
		return ""
	}
	for _, line := range strings.Split(output, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.EqualFold(line, "Caption") {
			continue
		}
		if strings.HasPrefix(strings.ToLower(line), "caption=") {
			return strings.TrimSpace(strings.TrimPrefix(line, "Caption="))
		}
		return line
	}
	return ""
}

func detectWindowsProductName(run commandRunner) string {
	output, err := run("powershell", "-NoProfile", "-Command", "(Get-ItemProperty 'HKLM:\\SOFTWARE\\Microsoft\\Windows NT\\CurrentVersion').ProductName")
	if err != nil {
		return ""
	}
	for _, line := range strings.Split(output, "\n") {
		line = strings.TrimSpace(line)
		if line != "" {
			return line
		}
	}
	return ""
}

func detectWindowsCulture(run commandRunner) string {
	output, err := run("powershell", "-NoProfile", "-Command", "(Get-Culture).Name")
	if err != nil {
		return ""
	}
	for _, line := range strings.Split(output, "\n") {
		line = strings.TrimSpace(line)
		if line != "" {
			return line
		}
	}
	return ""
}

func detectWindowsBuildNumber(run commandRunner) int {
	for _, property := range []string{"CurrentBuild", "CurrentBuildNumber"} {
		output, err := run("powershell", "-NoProfile", "-Command", "(Get-ItemProperty 'HKLM:\\SOFTWARE\\Microsoft\\Windows NT\\CurrentVersion')."+property)
		if err != nil {
			continue
		}
		for _, line := range strings.Split(output, "\n") {
			line = strings.TrimSpace(line)
			if line == "" {
				continue
			}
			if n, err := strconv.Atoi(line); err == nil {
				return n
			}
		}
	}
	return 0
}

func normalizeWindowsVersionName(name string, build int) string {
	trimmed := strings.TrimSpace(name)
	if trimmed == "" {
		if build >= 22000 {
			return "Windows 11"
		}
		return "windows"
	}
	if build >= 22000 && strings.Contains(trimmed, "Windows 10") {
		return strings.Replace(trimmed, "Windows 10", "Windows 11", 1)
	}
	return trimmed
}

func detectTimezone(now time.Time) string {
	name, _ := now.Zone()
	if strings.TrimSpace(name) == "" {
		return "UTC"
	}
	return name
}

var osReleasePaths = []string{"/etc/os-release", "/usr/lib/os-release"}

func readOSReleasePrettyNameFrom(paths []string) string {
	for _, path := range paths {
		data, err := os.ReadFile(path)
		if err != nil {
			continue
		}
		lines := strings.Split(string(data), "\n")
		for _, line := range lines {
			line = strings.TrimSpace(line)
			if strings.HasPrefix(line, "PRETTY_NAME=") {
				value := strings.TrimPrefix(line, "PRETTY_NAME=")
				value = strings.Trim(value, `"`)
				if value != "" {
					return value
				}
			}
		}
	}
	return ""
}

func runCommand(name string, args ...string) (string, error) {
	cmd := exec.Command(name, args...)
	out, err := cmd.Output()
	return string(out), err
}
