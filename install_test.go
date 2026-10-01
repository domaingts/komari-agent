//go:build linux

package main

import (
	"archive/tar"
	"compress/gzip"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

type installerArchiveEntry struct {
	name     string
	contents string
	typeflag byte
	linkname string
}

func installerHelpers(t *testing.T) string {
	t.Helper()
	if _, err := exec.LookPath("bash"); err != nil {
		t.Skip("installer tests require Bash")
	}
	data, err := os.ReadFile("install.sh")
	if err != nil {
		t.Fatal(err)
	}
	// Never execute the service/dependency installation body, even if its
	// sourcing guard regresses. The tested helpers contain all release staging.
	helpers, _, ok := strings.Cut(string(data), "# Default values\n")
	if !ok {
		t.Fatal("could not isolate installer release helpers")
	}
	path := filepath.Join(t.TempDir(), "installer-helpers.sh")
	if err := os.WriteFile(path, []byte(helpers), 0600); err != nil {
		t.Fatal(err)
	}
	return path
}

func runInstallerHelpers(t *testing.T, helpers, script string, environment ...string) ([]byte, error) {
	t.Helper()
	cmd := exec.Command("bash", "-c", `source "$INSTALLER_HELPERS" || exit; `+script)
	cmd.Env = append(os.Environ(), "INSTALLER_HELPERS="+helpers)
	cmd.Env = append(cmd.Env, environment...)
	return cmd.CombinedOutput()
}

func TestInstallerReleaseDownloadURL(t *testing.T) {
	helpers := installerHelpers(t)
	for _, tc := range []struct {
		name, version, proxy, want string
	}{
		{"default", "", "", "https://github.com/domaingts/komari-agent/releases/latest/download/komari-agent-linux-amd64-v1.tar.gz"},
		{"latest", "latest", "", "https://github.com/domaingts/komari-agent/releases/latest/download/komari-agent-linux-amd64-v1.tar.gz"},
		{"version", "v1.0.0", "", "https://github.com/domaingts/komari-agent/releases/download/v1.0.0/komari-agent-linux-amd64-v1.tar.gz"},
		{"explicit proxy", "v1.0.0", "https://proxy.example/", "https://proxy.example/https://github.com/domaingts/komari-agent/releases/download/v1.0.0/komari-agent-linux-amd64-v1.tar.gz"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			output, err := runInstallerHelpers(t, helpers, `release_download_url "$VERSION" "$PROXY"`, "VERSION="+tc.version, "PROXY="+tc.proxy)
			if err != nil {
				t.Fatalf("construct release URL: %v\n%s", err, output)
			}
			if got := strings.TrimSpace(string(output)); got != tc.want {
				t.Fatalf("URL = %q, want %q", got, tc.want)
			}
		})
	}
}

func writeInstallerArchive(t *testing.T, path string, entries []installerArchiveEntry) {
	t.Helper()
	file, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	compressed := gzip.NewWriter(file)
	archive := tar.NewWriter(compressed)
	for _, entry := range entries {
		typeflag := entry.typeflag
		if typeflag == 0 {
			typeflag = tar.TypeReg
		}
		header := &tar.Header{Name: entry.name, Mode: 0644, Typeflag: typeflag, Linkname: entry.linkname}
		if typeflag == tar.TypeReg {
			header.Size = int64(len(entry.contents))
		}
		if err := archive.WriteHeader(header); err != nil {
			t.Fatal(err)
		}
		if typeflag == tar.TypeReg {
			if _, err := archive.Write([]byte(entry.contents)); err != nil {
				t.Fatal(err)
			}
		}
	}
	if err := archive.Close(); err != nil {
		t.Fatal(err)
	}
	if err := compressed.Close(); err != nil {
		t.Fatal(err)
	}
}

func installerFixtureAgent(help string, exitCode string) string {
	return `#!/bin/sh
if [ "$1" != "--help" ]; then exit 99; fi
printf 'checked\n' > "$VALIDATION_LOG"
printf '%s\n' '` + help + `'
exit ` + exitCode + "\n"
}

func TestInstallerStagesBeforeReplacingExistingInstallation(t *testing.T) {
	helpers := installerHelpers(t)
	validAgent := installerFixtureAgent("--endpoint --token --gpu", "0")
	for _, tc := range []struct {
		name           string
		entries        []installerArchiveEntry
		corruptArchive bool
		downloadFails  bool
		os, arch       string
		wantSuccess    bool
		wantDownload   bool
	}{
		{name: "valid release", entries: []installerArchiveEntry{{name: "agent", contents: validAgent}}, wantSuccess: true, wantDownload: true},
		{name: "only selected member is extracted", entries: []installerArchiveEntry{{name: "../../escaped", contents: "must not be extracted"}, {name: "agent", contents: validAgent}}, wantSuccess: true, wantDownload: true},
		{name: "download failure", downloadFails: true, wantDownload: true},
		{name: "corrupt archive", corruptArchive: true, wantDownload: true},
		{name: "missing agent", entries: []installerArchiveEntry{{name: "other", contents: validAgent}}, wantDownload: true},
		{name: "empty agent", entries: []installerArchiveEntry{{name: "agent"}}, wantDownload: true},
		{name: "symlink agent", entries: []installerArchiveEntry{{name: "agent", typeflag: tar.TypeSymlink, linkname: "../agent"}}, wantDownload: true},
		{name: "hardlink agent", entries: []installerArchiveEntry{{name: "agent", typeflag: tar.TypeLink, linkname: "../agent"}}, wantDownload: true},
		{name: "duplicate agent", entries: []installerArchiveEntry{{name: "agent", contents: validAgent}, {name: "agent", contents: validAgent}}, wantDownload: true},
		{name: "help execution failure", entries: []installerArchiveEntry{{name: "agent", contents: installerFixtureAgent("--endpoint --token", "42")}}, wantDownload: true},
		{name: "unexpected binary", entries: []installerArchiveEntry{{name: "agent", contents: installerFixtureAgent("unrelated help", "0")}}, wantDownload: true},
		{name: "upstream SSH binary", entries: []installerArchiveEntry{{name: "agent", contents: installerFixtureAgent("--endpoint --token --disable-web-ssh", "0")}}, wantDownload: true},
		{name: "upstream updater binary", entries: []installerArchiveEntry{{name: "agent", contents: installerFixtureAgent("--endpoint --token --disable-auto-update", "0")}}, wantDownload: true},
		{name: "upstream warning binary", entries: []installerArchiveEntry{{name: "agent", contents: installerFixtureAgent("--endpoint --token --show-warning", "0")}}, wantDownload: true},
		{name: "unsupported OS", os: "Darwin", arch: "x86_64"},
		{name: "unsupported architecture", os: "Linux", arch: "aarch64"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			root := t.TempDir()
			installDir := filepath.Join(root, "installation")
			binDir := filepath.Join(root, "mock-bin")
			for _, dir := range []string{installDir, binDir} {
				if err := os.Mkdir(dir, 0700); err != nil {
					t.Fatal(err)
				}
			}
			oldAgent := []byte("existing agent must remain until replacement is validated\n")
			agentPath := filepath.Join(installDir, "agent")
			originalPath := filepath.Join(root, "original-agent")
			for _, path := range []string{agentPath, originalPath} {
				if err := os.WriteFile(path, oldAgent, 0700); err != nil {
					t.Fatal(err)
				}
			}
			archivePath := filepath.Join(root, "release.tar.gz")
			if tc.corruptArchive {
				if err := os.WriteFile(archivePath, []byte("not a gzip archive"), 0600); err != nil {
					t.Fatal(err)
				}
			} else {
				writeInstallerArchive(t, archivePath, tc.entries)
			}
			for name, body := range map[string]string{
				"uname": `#!/bin/sh
case "$1" in
    -s) printf '%s\n' "$MOCK_OS" ;;
    -m) printf '%s\n' "$MOCK_ARCH" ;;
    *) exit 98 ;;
esac
`,
				"curl": `#!/bin/sh
printf '%s\n' "$@" > "$CURL_LOG"
if [ "$DOWNLOAD_FAILS" = 1 ]; then exit 22; fi
output=""
while [ "$#" -gt 0 ]; do
    case "$1" in
        --output) output="$2"; shift 2 ;;
        *) shift ;;
    esac
done
if [ -z "$output" ]; then exit 97; fi
cp "$MOCK_ARCHIVE" "$output"
`,
			} {
				if err := os.WriteFile(filepath.Join(binDir, name), []byte(body), 0700); err != nil {
					t.Fatal(err)
				}
			}
			osName, arch := tc.os, tc.arch
			if osName == "" {
				osName = "Linux"
			}
			if arch == "" {
				arch = "x86_64"
			}
			failure := "0"
			if tc.downloadFails {
				failure = "1"
			}
			serviceLog := filepath.Join(root, "service.log")
			curlLog := filepath.Join(root, "curl.log")
			validationLog := filepath.Join(root, "validation.log")
			output, err := runInstallerHelpers(t, helpers, `
uninstall_previous() {
    printf 'stop\n' >> "$SERVICE_LOG"
    cmp -s "$INSTALL_DIR/agent" "$ORIGINAL_AGENT" || return 96
    [ -f "$VALIDATION_LOG" ] || return 95
}
url=$(release_download_url latest "")
install_release_binary "$INSTALL_DIR" "$url"
`,
				"PATH="+binDir+string(os.PathListSeparator)+os.Getenv("PATH"),
				"INSTALL_DIR="+installDir, "ORIGINAL_AGENT="+originalPath,
				"MOCK_ARCHIVE="+archivePath, "DOWNLOAD_FAILS="+failure,
				"MOCK_OS="+osName, "MOCK_ARCH="+arch,
				"SERVICE_LOG="+serviceLog, "CURL_LOG="+curlLog, "VALIDATION_LOG="+validationLog)
			if tc.wantSuccess != (err == nil) {
				t.Fatalf("success = %v, want %v: %v\n%s", err == nil, tc.wantSuccess, err, output)
			}
			installed, readErr := os.ReadFile(agentPath)
			if readErr != nil {
				t.Fatal(readErr)
			}
			wantAgent := string(oldAgent)
			if tc.wantSuccess {
				wantAgent = validAgent
			}
			if string(installed) != wantAgent {
				t.Fatalf("installed binary changed unexpectedly: %q", installed)
			}
			info, statErr := os.Stat(agentPath)
			if statErr != nil {
				t.Fatal(statErr)
			}
			wantMode := os.FileMode(0700)
			if tc.wantSuccess {
				wantMode = 0755
			}
			if info.Mode().Perm() != wantMode {
				t.Fatalf("installed mode = %o, want %o", info.Mode().Perm(), wantMode)
			}
			serviceCalls, serviceErr := os.ReadFile(serviceLog)
			if tc.wantSuccess {
				if serviceErr != nil || string(serviceCalls) != "stop\n" {
					t.Fatalf("expected one service stop after validation: %v, %q", serviceErr, serviceCalls)
				}
			} else if !os.IsNotExist(serviceErr) {
				t.Fatalf("failed staging touched the existing service: %v, %q", serviceErr, serviceCalls)
			}
			curlCalls, curlErr := os.ReadFile(curlLog)
			if tc.wantDownload {
				if curlErr != nil || !strings.Contains(string(curlCalls), "--fail\n") || !strings.Contains(string(curlCalls), "https://github.com/domaingts/komari-agent/releases/latest/download/komari-agent-linux-amd64-v1.tar.gz") {
					t.Fatalf("unexpected release download: %v, %q", curlErr, curlCalls)
				}
			} else if !os.IsNotExist(curlErr) {
				t.Fatalf("unsupported platform attempted a download: %v, %q", curlErr, curlCalls)
			}
			entries, listErr := os.ReadDir(installDir)
			if listErr != nil {
				t.Fatal(listErr)
			}
			if len(entries) != 1 || entries[0].Name() != "agent" {
				t.Fatalf("staging files were not cleaned up: %v", entries)
			}
			if _, err := os.Stat(filepath.Join(root, "escaped")); !os.IsNotExist(err) {
				t.Fatalf("an unselected archive member escaped staging: %v", err)
			}
		})
	}
}

func TestInstallerRejectsDirectoryDestinations(t *testing.T) {
	helpers := installerHelpers(t)
	for _, symlink := range []bool{false, true} {
		name := "directory"
		if symlink {
			name = "symlink to directory"
		}
		t.Run(name, func(t *testing.T) {
			root := t.TempDir()
			installDir := filepath.Join(root, "installation")
			if err := os.Mkdir(installDir, 0700); err != nil {
				t.Fatal(err)
			}
			agentPath := filepath.Join(installDir, "agent")
			directoryPath := agentPath
			if symlink {
				directoryPath = filepath.Join(root, "existing-directory")
			}
			if err := os.Mkdir(directoryPath, 0700); err != nil {
				t.Fatal(err)
			}
			if symlink {
				if err := os.Symlink(directoryPath, agentPath); err != nil {
					t.Fatal(err)
				}
			}
			preservedPath := filepath.Join(directoryPath, "preserved")
			if err := os.WriteFile(preservedPath, []byte("leave this directory untouched"), 0600); err != nil {
				t.Fatal(err)
			}
			before, err := os.Lstat(agentPath)
			if err != nil {
				t.Fatal(err)
			}
			archivePath := filepath.Join(root, "release.tar.gz")
			writeInstallerArchive(t, archivePath, []installerArchiveEntry{{
				name: "agent", contents: installerFixtureAgent("--endpoint --token --gpu", "0"),
			}})
			serviceLog := filepath.Join(root, "service.log")
			curlLog := filepath.Join(root, "curl.log")
			output, err := runInstallerHelpers(t, helpers, `
uname() {
    case "$1" in
        -s) printf 'Linux\n' ;;
        -m) printf 'x86_64\n' ;;
        *) return 98 ;;
    esac
}
curl() {
    printf 'download\n' >> "$CURL_LOG"
    local output=""
    while [[ $# -gt 0 ]]; do
        case "$1" in
            --output) output="$2"; shift 2 ;;
            *) shift ;;
        esac
    done
    cp "$MOCK_ARCHIVE" "$output"
}
uninstall_previous() {
    printf 'stop\n' >> "$SERVICE_LOG"
}
url=$(release_download_url latest "")
install_release_binary "$INSTALL_DIR" "$url"
`, "INSTALL_DIR="+installDir, "MOCK_ARCHIVE="+archivePath,
				"SERVICE_LOG="+serviceLog, "CURL_LOG="+curlLog,
				"VALIDATION_LOG="+filepath.Join(root, "validation.log"))
			if err == nil {
				t.Fatalf("directory destination was accepted:\n%s", output)
			}
			if !strings.Contains(string(output), "destination is a directory") {
				t.Fatalf("unexpected failure: %v\n%s", err, output)
			}
			after, err := os.Lstat(agentPath)
			if err != nil {
				t.Fatal(err)
			}
			if !os.SameFile(before, after) || before.Mode() != after.Mode() {
				t.Fatal("destination was replaced or its permissions changed")
			}
			if symlink {
				target, err := os.Readlink(agentPath)
				if err != nil || target != directoryPath {
					t.Fatalf("directory symlink was changed: %q, %v", target, err)
				}
			}
			contents, err := os.ReadFile(preservedPath)
			if err != nil || string(contents) != "leave this directory untouched" {
				t.Fatalf("existing directory contents changed: %q, %v", contents, err)
			}
			entries, err := os.ReadDir(directoryPath)
			if err != nil || len(entries) != 1 || entries[0].Name() != "preserved" {
				t.Fatalf("binary was moved inside the directory: %v, %v", entries, err)
			}
			entries, err = os.ReadDir(installDir)
			if err != nil || len(entries) != 1 || entries[0].Name() != "agent" {
				t.Fatalf("staging files were left behind: %v, %v", entries, err)
			}
			for _, path := range []string{serviceLog, curlLog} {
				if _, err := os.Stat(path); !os.IsNotExist(err) {
					t.Fatalf("invalid destination caused installation actions at %s: %v", path, err)
				}
			}
		})
	}
}
