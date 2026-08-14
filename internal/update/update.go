package update

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
)

const repo = "thenickygee/mirage"

func checkGH() error {
	if _, err := exec.LookPath("gh"); err != nil {
		return fmt.Errorf("'gh' CLI is required but not found. Install it: https://cli.github.com")
	}
	return nil
}

func latestVersion() (string, error) {
	out, err := exec.Command("gh", "release", "view", "--repo", repo, "--json", "tagName", "-q", ".tagName").Output()
	if err != nil {
		return "", fmt.Errorf("failed to check latest release: %w", err)
	}
	return strings.TrimSpace(string(out)), nil
}

func CheckForUpdate(currentVersion string) (string, bool) {
	if currentVersion == "dev" {
		return "", false
	}
	if err := checkGH(); err != nil {
		return "", false
	}
	latest, err := latestVersion()
	if err != nil {
		return "", false
	}
	if latest != "" && latest != "v"+currentVersion && latest != currentVersion {
		return latest, true
	}
	return "", false
}

func Run(currentVersion string) {
	if err := checkGH(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}

	latest, err := latestVersion()
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}

	current := currentVersion
	if !strings.HasPrefix(current, "v") {
		current = "v" + current
	}
	if latest == current {
		fmt.Println("Already up to date.")
		return
	}

	goos := runtime.GOOS
	goarch := runtime.GOARCH
	pattern := fmt.Sprintf("*%s_%s*", goos, goarch)

	tmpDir, err := os.MkdirTemp("", "mirage-update-")
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error creating temp dir: %v\n", err)
		os.Exit(1)
	}
	defer func() { _ = os.RemoveAll(tmpDir) }()

	fmt.Printf("Downloading %s...\n", latest)
	cmd := exec.Command("gh", "release", "download", "--repo", repo, "--pattern", pattern, "--dir", tmpDir)
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		fmt.Fprintf(os.Stderr, "Error downloading release: %v\n", err)
		os.Exit(1)
	}

	// Find and extract the tar.gz.
	entries, _ := os.ReadDir(tmpDir)
	var archive string
	for _, e := range entries {
		if strings.HasSuffix(e.Name(), ".tar.gz") {
			archive = filepath.Join(tmpDir, e.Name())
			break
		} else if strings.HasSuffix(e.Name(), ".zip") {
			archive = filepath.Join(tmpDir, e.Name())
			break
		}
	}
	if archive == "" {
		fmt.Fprintln(os.Stderr, "Error: no archive found in downloaded release")
		os.Exit(1)
	}

	extractDir := filepath.Join(tmpDir, "extracted")
	_ = os.MkdirAll(extractDir, 0o755)

	if strings.HasSuffix(archive, ".tar.gz") {
		cmd = exec.Command("tar", "xzf", archive, "-C", extractDir)
	} else {
		cmd = exec.Command("unzip", "-o", archive, "-d", extractDir)
	}
	if err := cmd.Run(); err != nil {
		fmt.Fprintf(os.Stderr, "Error extracting archive: %v\n", err)
		os.Exit(1)
	}

	// Find the binary.
	newBinary := filepath.Join(extractDir, "mirage")
	if runtime.GOOS == "windows" {
		newBinary += ".exe"
	}
	if _, err := os.Stat(newBinary); err != nil {
		fmt.Fprintf(os.Stderr, "Error: binary not found in extracted archive\n")
		os.Exit(1)
	}

	// Replace the current binary.
	currentBinary, err := os.Executable()
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error finding current binary: %v\n", err)
		os.Exit(1)
	}
	currentBinary, _ = filepath.EvalSymlinks(currentBinary)

	// Copy new binary over old one.
	data, err := os.ReadFile(newBinary)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error reading new binary: %v\n", err)
		os.Exit(1)
	}
	if err := os.WriteFile(currentBinary, data, 0o755); err != nil {
		fmt.Fprintf(os.Stderr, "Error writing binary: %v\n", err)
		os.Exit(1)
	}

	fmt.Printf("Updated mirage to %s\n", latest)
}
